package notify

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Este arquivo é um teste de invariante: **qualquer** texto de erro que surja de uma implementação de canal não pode carregar credenciais.
//
// Por que separar em um arquivo próprio: os primeiros casos dos canais cobriam apenas o caminho de sucesso e os erros de negócio da plataforma,
// sem olhar de forma alguma para as falhas da camada de transporte. E são justamente os erros de transporte (conexão recusada/falha de DNS/timeout) os mais perigosos —
// o *url.Error retornado pelo http.Client.Do coloca a **URL completa** no texto do erro, e as credenciais destes canais
// estão na URL. A credencial escorria por essa string até quatro saídas:
//
//	notification_deliveries.last_error  → gravada em texto claro no banco
//	resposta de GET /api/notify/deliveries → contorna a máscara da configuração do canal, ecoada para o navegador
//	log do servidor                     → frequentemente enviado para retenção externa
//	resposta 502 da interface de teste de envio → aparece direto no front-end
//
// Por isso aqui não se testa apenas uma função: envia-se, canal por canal, uma requisição que necessariamente falha, e se assevera que o texto do erro
// não contém aquela credencial.

// credentialCases cobre todas as formas de canal em que "a credencial está na URL":
// DingTalk/WeCom na query, Feishu no último trecho do path, Telegram no trecho do meio do path.
var credentialCases = []struct {
	name   string
	ch     Channel
	cfg    map[string]any
	secret string
}{
	{
		name:   "access_token do DingTalk na query",
		ch:     dingTalkChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/robot/send?access_token=" + leakProbeToken},
		secret: leakProbeToken,
	},
	{
		name:   "key do WeCom na query",
		ch:     weComChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/cgi-bin/webhook/send?key=" + leakProbeToken},
		secret: leakProbeToken,
	},
	{
		name:   "hook id do Feishu no último trecho do path",
		ch:     feishuChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/open-apis/bot/v2/hook/" + leakProbeToken},
		secret: leakProbeToken,
	},
	{
		name:   "bot token do Telegram no trecho do meio do path",
		ch:     telegramChannel{},
		cfg:    map[string]any{"bot_token": leakProbeToken, "chat_id": "1", "base_url": "http://127.0.0.1:1"},
		secret: leakProbeToken,
	},
	{
		name:   "chave de assinatura do DingTalk",
		ch:     dingTalkChannel{},
		cfg:    map[string]any{"webhook": "http://127.0.0.1:1/robot/send", "secret": leakProbeToken},
		secret: leakProbeToken,
	},
}

// leakProbeToken é um valor sentinela que jamais poderia ser uma credencial real, usado para procurá-lo no texto do erro.
const leakProbeToken = "LEAKPROBE0123456789abcdef"

// TestChannelErrorsNeverLeakCredentials é a invariante central.
func TestChannelErrorsNeverLeakCredentials(t *testing.T) {
	for _, tc := range credentialCases {
		t.Run(tc.name, func(t *testing.T) {
			// Par que necessariamente falha: ninguém escuta em 127.0.0.1:1, o que percorre o caminho de conexão recusada.
			_, err := tc.ch.Send(context.Background(), tc.cfg, Message{
				Items: []Item{{FindingID: 1, Severity: "high", Name: "泄露探针"}},
			})
			if err == nil {
				t.Fatal("deveria dar erro para um endereço inalcançável")
			}
			assertNoSecret(t, err.Error(), tc.secret)
		})
	}
}

// TestChannelErrorsNeverLeakCredentialsInPermanentPath cobre o ramo de falha permanente:
// falha na validação da URL, erro de negócio da plataforma etc. também repassam o texto do erro para fora, e igualmente não podem carregar credencial.
func TestChannelErrorsNeverLeakCredentialsInPermanentPath(t *testing.T) {
	cases := []struct {
		name string
		ch   Channel
		cfg  map[string]any
	}{
		// O endereço contém credencial mas o formato é inválido → aciona o ramo validateHTTPURL / url.Parse.
		{"endereço do DingTalk inválido", dingTalkChannel{}, map[string]any{"webhook": "file:///" + leakProbeToken}},
		{"endereço do WeCom inválido", weComChannel{}, map[string]any{"webhook": "gopher://" + leakProbeToken}},
		{"endereço do Feishu inválido", feishuChannel{}, map[string]any{"webhook": "ftp://" + leakProbeToken + "/hook"}},
		{"endereço da API do Telegram inválido", telegramChannel{}, map[string]any{"bot_token": "tok", "chat_id": "1", "base_url": "file://" + leakProbeToken}},
		{"endereço do Webhook genérico inválido", webhookChannel{}, map[string]any{"url": "javascript:" + leakProbeToken}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.ch.Send(context.Background(), tc.cfg, Message{Items: []Item{{Severity: "high"}}})
			if err == nil {
				t.Fatal("configuração inválida deveria dar erro")
			}
			assertNoSecret(t, err.Error(), leakProbeToken)
		})
	}
}

func assertNoSecret(t *testing.T, text, secret string) {
	t.Helper()
	if strings.Contains(text, secret) {
		t.Fatalf("o texto do erro vazou a credencial %q:\n    %s", secret, text)
	}
}

func TestRedactRequestTargetKeepsOnlySchemeAndHost(t *testing.T) {
	cases := map[string]string{
		"https://oapi.dingtalk.com/robot/send?access_token=S1":    "https://oapi.dingtalk.com/…",
		"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=S2": "https://qyapi.weixin.qq.com/…",
		"https://open.feishu.cn/open-apis/bot/v2/hook/S3":         "https://open.feishu.cn/…",
		"https://api.telegram.org/botS4/sendMessage":              "https://api.telegram.org/…",
		"http://10.0.0.5:8080/hook":                               "http://10.0.0.5:8080/…",
	}
	for in, want := range cases {
		got := redactRequestTarget(in)
		if got != want {
			t.Errorf("redactRequestTarget(%q) = %q, esperado %q", in, got, want)
		}
		// O resultado ofuscado em si não pode conter nenhum trecho de path/query do endereço original.
		if parts := strings.SplitN(in, "://", 2); len(parts) == 2 {
			if hostAndRest := strings.SplitN(parts[1], "/", 2); len(hostAndRest) == 2 && hostAndRest[1] != "" {
				if strings.Contains(got, hostAndRest[1]) {
					t.Errorf("depois da ofuscação ainda há trecho de path/query %q: %q", hostAndRest[1], got)
				}
			}
		}
	}
	// Uma entrada não interpretável jamais ecoa a string original.
	for _, bad := range []string{"", "://", "not a url", "http://"} {
		if got := redactRequestTarget(bad); strings.Contains(got, bad) && bad != "" {
			t.Errorf("a entrada não interpretável %q foi ecoada como %q", bad, got)
		}
	}
}

// TestRedactTransportErrorStripsURL mira diretamente este tipo concreto, o *url.Error:
// é o tipo de retorno do http.Client.Do e também a primeira cena do vazamento.
func TestRedactTransportErrorStripsURL(t *testing.T) {
	inner := errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	uerr := &url.Error{
		Op:  "Post",
		URL: "https://api.telegram.org/bot" + leakProbeToken + "/sendMessage",
		Err: inner,
	}
	got := redactTransportError(uerr)
	assertNoSecret(t, got, leakProbeToken)
	if !strings.Contains(got, "api.telegram.org") {
		t.Errorf("deveria manter o host para investigação, obtido %q", got)
	}
	if !strings.Contains(got, "connection refused") {
		t.Errorf("deveria manter a causa subjacente para investigação, obtido %q", got)
	}
	// O Op também deve ser mantido (se foi POST ou GET faz diferença na investigação).
	if !strings.Contains(got, "Post") {
		t.Errorf("deveria manter o nome da operação, obtido %q", got)
	}
}

// TestRedactURLsInTextHandlesFallback é o caminho de rede de segurança: nos erros personalizados que não são *url.Error
// (como o erro retornado pela política de redirecionamento), o endereço também deve ser removido.
func TestRedactURLsInTextHandlesFallback(t *testing.T) {
	in := fmt.Sprintf("redirecionamento entre hosts recusado (a.example → http://b.example/bot%s/send)", leakProbeToken)
	got := redactURLsInText(in)
	assertNoSecret(t, got, leakProbeToken)
	if !strings.Contains(got, "http://b.example/…") {
		t.Errorf("deveria substituir o endereço pela forma ofuscada, obtido %q", got)
	}
	// Texto sem endereço é mantido como está.
	if plain := "dial tcp: connection refused"; redactURLsInText(plain) != plain {
		t.Error("texto sem endereço não deveria ser alterado")
	}
}

// TestCrossHostRedirectRefused cobre "credencial na URL + seguir salto entre hosts = entregar a credencial".
// Os dois servidores do httptest escutam em portas diferentes de 127.0.0.1; portas diferentes significam Host diferente,
// o que constitui justamente um salto entre hosts.
func TestCrossHostRedirectRefused(t *testing.T) {
	var hit bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/robot/send?access_token="+leakProbeToken, http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()

	_, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": redirector.URL + "/robot/send?access_token=" + leakProbeToken},
		Message{Items: []Item{{Severity: "high"}}})
	if err == nil {
		t.Fatal("o redirecionamento entre hosts deveria ser recusado")
	}
	if hit {
		t.Fatal("o destino do salto foi acessado — a credencial vazou junto com o redirecionamento")
	}
	assertNoSecret(t, err.Error(), leakProbeToken)
}

// TestSameHostRedirectAllowed é o caso inverso: o salto no mesmo host (como acrescentar uma barra no final) deve continuar funcionando,
// senão o fluxo normal de trabalho seria barrado junto.
func TestSameHostRedirectAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robot/send" {
			// Salto no mesmo host e na mesma porta.
			http.Redirect(w, r, "/robot/send/", http.StatusTemporaryRedirect)
			return
		}
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer srv.Close()

	if _, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"},
		Message{Items: []Item{{Severity: "high"}}}); err != nil {
		t.Fatalf("o redirecionamento no mesmo host não deveria ser recusado: %v", err)
	}
}
