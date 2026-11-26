package notify

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// Este arquivo cobre dois reforços relacionados:
//   ① o endereço de entrega não pode usar o servidor como trampolim para atacar a rede interna / metadados de nuvem (SSRF)
//   ② a mensagem de erro da validação do endereço não pode vazar a credencial contida no endereço
//
// Sobre o ambiente de teste: muitos casos deste pacote usam receptores falsos httptest em 127.0.0.1, que a guarda barra por padrão.
// Por isso o TestMain ativa AllowLocalTargetsEnv de forma unificada, enquanto cada caso de SSRF abaixo
// o limpa explicitamente, para asseverar o comportamento de **recusa por padrão**.

func TestMain(m *testing.M) {
	// Permite que os casos comuns conectem ao receptor falso local; os casos de SSRF o esvaziam temporariamente por conta própria.
	_ = os.Setenv(AllowLocalTargetsEnv, "1")
	os.Exit(m.Run())
}

// TestDialGuardRejectsLoopbackByDefault é a asserção central da proteção contra SSRF:
// na configuração padrão, a entrega a um endereço de loopback deve ser recusada pela **camada de conexão**.
func TestDialGuardRejectsLoopbackByDefault(t *testing.T) {
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		_, _ = io.WriteString(w, `{"errcode":0}`)
	}))
	defer srv.Close()

	t.Setenv(AllowLocalTargetsEnv, "") // fechar a saída de emergência = comportamento padrão
	_, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"}, Message{Items: []Item{{Severity: "high"}}})
	if err == nil {
		t.Fatal("por padrão não deveria permitir a entrega a endereço de loopback")
	}
	if hit {
		t.Fatal("a requisição já chegou ao serviço local — a guarda não funcionou")
	}
	// A mensagem de erro deve orientar o usuário sobre como liberar (o relay SMTP local é uma configuração legítima).
	if !strings.Contains(err.Error(), AllowLocalTargetsEnv) {
		t.Errorf("a mensagem de recusa deveria explicar como liberar explicitamente: %v", err)
	}
}

// TestDialGuardAllowsLoopbackWhenOptedIn é o caso inverso: depois de liberar explicitamente deve funcionar,
// senão implantações legítimas como o postfix local / relay da rede interna seriam inviabilizadas por uma proibição total.
func TestDialGuardAllowsLoopbackWhenOptedIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	defer srv.Close()

	t.Setenv(AllowLocalTargetsEnv, "1")
	if _, err := (dingTalkChannel{}).Send(context.Background(),
		map[string]any{"webhook": srv.URL + "/robot/send"}, Message{Items: []Item{{Severity: "high"}}}); err != nil {
		t.Fatalf("depois de liberar explicitamente deveria ser possível entregar: %v", err)
	}
}

func TestIsBlockedDialIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.1.2.3", "::1",
		"169.254.169.254", // endpoint de metadados de nuvem — a principal razão de existir desta função
		"169.254.1.1", "fe80::1",
		"0.0.0.0", "::",
		"224.0.0.1", "ff02::1",
		"::ffff:127.0.0.1", // a forma IPv4-mapped precisa ser convertida de volta antes do julgamento, senão é uma brecha
		"",
	}
	for _, s := range blocked {
		if !isBlockedDialIP(net.ParseIP(s)) {
			t.Errorf("%s deveria ser recusado", s)
		}
	}
	// As redes privadas RFC1918 são **liberadas de propósito**: Mattermost / relay SMTP próprios na rede interna são usos legítimos comuns.
	// Esta asserção fixa essa escolha — se no futuro alguém acrescentar de passagem um julgamento de rede privada, ela falha,
	// forçando uma decisão consciente (e não inviabilizar em silêncio um conjunto de implantações).
	allowed := []string{"10.0.0.5", "172.16.3.4", "192.168.1.10", "8.8.8.8", "2606:4700::1111"}
	for _, s := range allowed {
		if isBlockedDialIP(net.ParseIP(s)) {
			t.Errorf("%s deveria ser liberado (rede privada é um destino de entrega legítimo comum)", s)
		}
	}
}

// TestValidateHTTPURLRejectsLiteralPrivateTargets cobre o aviso antecipado da fase de configuração:
// um IP literal deve ser recusado já ao salvar, em vez de esperar a primeira entrega falhar.
func TestValidateHTTPURLRejectsLiteralPrivateTargets(t *testing.T) {
	t.Setenv(AllowLocalTargetsEnv, "")
	for _, raw := range []string{
		"http://127.0.0.1:8080/hook",
		"http://169.254.169.254/latest/meta-data/",
		"http://[::1]:8080/hook",
	} {
		if err := validateHTTPURL(raw); err == nil {
			t.Errorf("%s deveria ser recusado na fase de configuração", raw)
		}
	}
	// Endereços públicos e privados passam normalmente (a rede privada fica para a fase de conexão, onde não é barrada).
	for _, raw := range []string{"https://oapi.dingtalk.com/robot/send", "http://10.0.0.9/hook"} {
		if err := validateHTTPURL(raw); err != nil {
			t.Errorf("%s deveria passar na validação: %v", raw, err)
		}
	}
}

// TestValidateHTTPURLErrorNeverLeaksCredentials é o ramo que a auditoria apontou como omitido por mim na rodada anterior.
//
// Quando o url.Parse **falha**, retorna um *url.Error cujo Error() contém o endereço original completo. Na rodada anterior eu só
// ofusquei o erro retornado pelo http.Client.Do, esquecendo deste ponto; e o caso de "caminho de falha permanente"
// que foi acrescentado na época (file://, gopher://, ftp://) na verdade o url.Parse consegue interpretar com sucesso, indo pelo ramo de scheme,
// então tudo verde também não provava que este caminho era seguro — era uma falsa garantia.
func TestValidateHTTPURLErrorNeverLeaksCredentials(t *testing.T) {
	cases := []string{
		"http://127.0.0.1/%zz?access_token=" + leakProbeToken,         // escape percentual inválido
		"https://a.example.com:port/x?access_token=" + leakProbeToken, // porta não numérica
		"http://[::1?access_token=" + leakProbeToken,                  // colchetes desemparelhados
	}
	for _, raw := range cases {
		// Primeiro confirma que esta entrada **de fato** faz o url.Parse falhar. Sem este passo, o caso poderia ir para outro ramo
		// sem que se percebesse (a falsa garantia da rodada anterior surgiu assim).
		if _, err := url.Parse(raw); err == nil {
			t.Errorf("%q deveria falhar na interpretação, senão este caso não cobre o ramo alvo", raw)
			continue
		}
		err := validateHTTPURL(raw)
		if err == nil {
			t.Errorf("%q deveria falhar na validação", raw)
			continue
		}
		assertNoSecret(t, err.Error(), leakProbeToken)
	}
	// Confirma que o embrulho da camada do canal também não deixou o endereço escapar.
	t.Setenv(AllowLocalTargetsEnv, "")
	err := (dingTalkChannel{}).Validate(map[string]any{"webhook": cases[0]})
	if err == nil {
		t.Fatal("endereço inválido deveria falhar na validação")
	}
	assertNoSecret(t, err.Error(), leakProbeToken)
}

// TestEmailDialGuardRejectsLoopbackByDefault cobre a guarda de conexão do canal SMTP.
//
// O canal de e-mail já usou um net.Dialer puro, a única brecha em toda a proteção contra SSRF: preencher o host com
// 169.254.169.254 ou 127.0.0.1 conectava direto, e quando o handshake do smtp.NewClient falha, ele embrulha no erro a
// linha devolvida pelo outro lado, ecoada via last_error pela interface de histórico de entregas — exatamente a primitiva de leitura semi-cega
// que os outros canais já fecharam; a diferença de tempo entre "conexão recusada" e "timeout" também poderia ser usada para sondar portas.
//
// O TestMain deste pacote ativa o AllowLocalTargetsEnv globalmente (muitos casos usam receptores falsos em 127.0.0.1),
// então este caso precisa limpá-lo por conta própria — senão passaria com a guarda presente ou ausente,
// e foi justamente por isso que a brecha não foi detectada por nenhum teste na época.
func TestEmailDialGuardRejectsLoopbackByDefault(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, nil)

	t.Setenv(AllowLocalTargetsEnv, "") // fechar a saída de emergência = comportamento padrão
	_, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil {
		t.Fatal("por padrão não deveria permitir a entrega de e-mail a endereço de loopback")
	}
	// A conexão nem deveria ser estabelecida: a guarda barra no gancho Control, e o EHLO nunca chega a ser enviado.
	if f.sawCommand("EHLO") || f.sawCommand("HELO") {
		t.Fatal("a sessão SMTP já foi estabelecida — a guarda não funcionou")
	}
	// A mensagem de erro deve orientar o usuário sobre como liberar (o relay postfix local é uma configuração legítima).
	if !strings.Contains(err.Error(), AllowLocalTargetsEnv) {
		t.Errorf("a mensagem de recusa deveria explicar como liberar explicitamente: %v", err)
	}
}

// TestEmailDialGuardAllowsLoopbackWhenOptedIn é o caso inverso emparelhado: depois de liberar explicitamente
// deve ser possível entregar normalmente. Relay SMTP próprio na rede interna / local é uma implantação muito comum, e a guarda não pode ser uma proibição total.
func TestEmailDialGuardAllowsLoopbackWhenOptedIn(t *testing.T) {
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, nil)

	t.Setenv(AllowLocalTargetsEnv, "1")
	if _, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg()); err != nil {
		t.Fatalf("depois de liberar explicitamente, o SMTP local deveria conseguir entregar: %v", err)
	}
	if !f.sawCommand("EHLO") {
		t.Fatal("EHLO não foi visto — a sessão não foi realmente estabelecida")
	}
}
