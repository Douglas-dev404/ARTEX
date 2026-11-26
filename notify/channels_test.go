package notify

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
)

// singleMsg constrói uma mensagem única com aspas e quebra de linha. Usa deliberadamente um título/resumo contendo `"` e `\n`:
// essa é exatamente a entrada que a interpolação de template tem mais facilidade de transformar em JSON inválido.
func singleMsg() Message {
	return Message{
		Items: []Item{{
			FindingID: 42,
			Name:      `登录处 "SQL注入" 风险`,
			VulnClass: "SQL注入",
			Severity:  "high",
			Summary:   "参数 id\n未过滤 导致注入",
			Assets:    []string{"a.example.com", "b.example.com"},
			DetailURL: "https://artex.local/function/findings/detail?id=42",
		}},
	}
}

// batchMsg constrói uma mensagem consolidada de um lote.
func batchMsg(n int) Message {
	m := Message{Batch: true, WindowMinutes: 30, HomeURL: "https://artex.local/function/findings"}
	for i := 0; i < n; i++ {
		m.Items = append(m.Items, Item{
			FindingID: int64(i + 1),
			Name:      "漏洞" + itoa(i+1),
			VulnClass: "XSS",
			Severity:  "medium",
			Summary:   "反射型跨站脚本",
			Assets:    []string{"target.example.com"},
		})
	}
	return m
}

// capturePost sobe um receptor falso e devolve ao assert o corpo e os cabeçalhos da requisição recebida.
func capturePost(t *testing.T, respBody string, assert func(t *testing.T, body map[string]any, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("o corpo da requisição não é um JSON válido: %v\nOriginal: %s", err, raw)
			}
		}
		if assert != nil {
			assert(t, body, r)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDingTalkSendsActionCardWhenLinkPresent(t *testing.T) {
	srv := capturePost(t, `{"errcode":0,"errmsg":"ok"}`, func(t *testing.T, body map[string]any, _ *http.Request) {
		if body["msgtype"] != "actionCard" {
			t.Fatalf("com link de retorno deveria enviar actionCard, obtido %v", body["msgtype"])
		}
		card, _ := body["actionCard"].(map[string]any)
		if card["singleURL"] != "https://artex.local/function/findings/detail?id=42" {
			t.Errorf("link de retorno perdido: %v", card["singleURL"])
		}
	})
	if _, err := (dingTalkChannel{}).Send(context.Background(), map[string]any{"webhook": srv.URL}, singleMsg()); err != nil {
		t.Fatalf("falha na entrega: %v", err)
	}
}

func TestDingTalkFallsBackToMarkdownForBatch(t *testing.T) {
	srv := capturePost(t, `{"errcode":0,"errmsg":"ok"}`, func(t *testing.T, body map[string]any, _ *http.Request) {
		if body["msgtype"] != "markdown" {
			t.Fatalf("a mensagem consolidada deveria enviar markdown, obtido %v", body["msgtype"])
		}
		md, _ := body["markdown"].(map[string]any)
		if !strings.Contains(md["text"].(string), "近 30 分钟") {
			t.Errorf("o corpo consolidado não tem a janela de tempo: %v", md["text"])
		}
	})
	if _, err := (dingTalkChannel{}).Send(context.Background(), map[string]any{"webhook": srv.URL}, batchMsg(3)); err != nil {
		t.Fatalf("falha na entrega: %v", err)
	}
}

// TestDingTalkBusinessErrorIsPermanent trava o julgamento de "HTTP 200 mas errcode diferente de 0".
// Não verificar o errcode registraria uma falha de entrega como sucesso — uma armadilha comum às plataformas de IM chinesas.
func TestDingTalkBusinessErrorIsPermanent(t *testing.T) {
	srv := capturePost(t, `{"errcode":310000,"errmsg":"keywords not in content"}`, nil)
	_, err := (dingTalkChannel{}).Send(context.Background(), map[string]any{"webhook": srv.URL}, singleMsg())
	if err == nil {
		t.Fatal("errcode diferente de 0 deveria dar erro")
	}
	if !IsPermanent(err) {
		t.Fatalf("palavra-chave que não confere é erro de configuração e deveria ser marcada como falha permanente, obtido %v", err)
	}
	if !strings.Contains(err.Error(), "310000") {
		t.Errorf("a mensagem de erro deveria incluir o código de erro da plataforma, obtido %v", err)
	}
}

func TestWeComTruncatesCJKWithinByteLimit(t *testing.T) {
	var contentLen int
	srv := capturePost(t, `{"errcode":0,"errmsg":"ok"}`, func(t *testing.T, body map[string]any, _ *http.Request) {
		md, _ := body["markdown"].(map[string]any)
		content, _ := md["content"].(string)
		contentLen = len(content)
		if !utf8.ValidString(content) {
			t.Fatal("depois do truncamento não é UTF-8 válido — o WeCom rejeitaria a mensagem inteira")
		}
	})
	// Monta um consolidado em chinês longo o bastante, que necessariamente excede 4096 bytes.
	m := batchMsg(200)
	if _, err := (weComChannel{}).Send(context.Background(), map[string]any{"webhook": srv.URL}, m); err != nil {
		t.Fatalf("falha na entrega: %v", err)
	}
	if contentLen > weComMarkdownLimit {
		t.Fatalf("corpo de %d bytes excede o limite do WeCom %d", contentLen, weComMarkdownLimit)
	}
	if contentLen == 0 {
		t.Fatal("corpo vazio")
	}
}

func TestWeComRateLimitIsRetryableButKeyErrorIsPermanent(t *testing.T) {
	limited := capturePost(t, `{"errcode":45009,"errmsg":"api freq out of limit"}`, nil)
	_, err := (weComChannel{}).Send(context.Background(), map[string]any{"webhook": limited.URL}, singleMsg())
	if err == nil || IsPermanent(err) {
		t.Fatalf("45009 é limitação por janela deslizante e deveria ser passível de nova tentativa, obtido %v", err)
	}

	badKey := capturePost(t, `{"errcode":93000,"errmsg":"invalid webhook url"}`, nil)
	_, err = (weComChannel{}).Send(context.Background(), map[string]any{"webhook": badKey.URL}, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("93000 é key inválida, tentar de novo não se cura sozinho, deveria ser falha permanente, obtido %v", err)
	}
}

func TestFeishuCardStructureAndSign(t *testing.T) {
	const secret = "SECtest123"
	srv := capturePost(t, `{"code":0,"msg":"success"}`, func(t *testing.T, body map[string]any, _ *http.Request) {
		if body["msg_type"] != "interactive" {
			t.Fatalf("deveria enviar cartão interativo, obtido %v", body["msg_type"])
		}
		card, _ := body["card"].(map[string]any)
		header, _ := card["header"].(map[string]any)
		if header["template"] != "orange" {
			t.Errorf("a severidade high deveria ter a cor orange, obtido %v", header["template"])
		}
		// Com secret configurado, é obrigatório levar os parâmetros de assinatura, senão o Feishu rejeita com 19021.
		if body["sign"] == nil || body["timestamp"] == nil {
			t.Fatalf("parâmetros de assinatura ausentes: %v", body)
		}
		// Entre os elementos do cartão deve haver um botão cuja url aponta para o detalhe da vulnerabilidade.
		elements, _ := card["elements"].([]any)
		foundButton := false
		for _, e := range elements {
			em, _ := e.(map[string]any)
			if em["tag"] != "action" {
				continue
			}
			actions, _ := em["actions"].([]any)
			for _, a := range actions {
				am, _ := a.(map[string]any)
				if am["url"] == "https://artex.local/function/findings/detail?id=42" {
					foundButton = true
				}
			}
		}
		if !foundButton {
			t.Fatal("o cartão não tem botão apontando para a página de detalhe")
		}
	})
	cfg := map[string]any{"webhook": srv.URL, "secret": secret}
	if _, err := (feishuChannel{}).Send(context.Background(), cfg, singleMsg()); err != nil {
		t.Fatalf("falha na entrega: %v", err)
	}
}

func TestFeishuWithoutSecretOmitsSign(t *testing.T) {
	srv := capturePost(t, `{"code":0,"msg":"success"}`, func(t *testing.T, body map[string]any, _ *http.Request) {
		if body["sign"] != nil || body["timestamp"] != nil {
			t.Fatalf("sem secret configurado não deveria levar parâmetros de assinatura: %v", body)
		}
	})
	if _, err := (feishuChannel{}).Send(context.Background(), map[string]any{"webhook": srv.URL}, singleMsg()); err != nil {
		t.Fatalf("falha na entrega: %v", err)
	}
}

func TestTelegramEscapesHTMLInUntrustedContent(t *testing.T) {
	var text string
	srv := capturePost(t, `{"ok":true}`, func(t *testing.T, body map[string]any, _ *http.Request) {
		text, _ = body["text"].(string)
		if body["parse_mode"] != "HTML" {
			t.Fatalf("deveria usar o modo de parse HTML, obtido %v", body["parse_mode"])
		}
	})
	m := Message{Items: []Item{{
		Severity: "high",
		// Título e resumo vêm do alvo testado/da saída do modelo, e são conteúdo não confiável.
		Name:    `<script>alert(1)</script>`,
		Summary: "a & b < c",
	}}}
	if _, err := (telegramChannel{}).Send(context.Background(),
		map[string]any{"bot_token": "tok", "chat_id": "1", "base_url": srv.URL}, m); err != nil {
		t.Fatalf("falha na entrega: %v", err)
	}
	if strings.Contains(text, "<script>") {
		t.Fatalf("HTML sem escape, há injeção: %q", text)
	}
	if !strings.Contains(text, "&lt;script&gt;") {
		t.Fatalf("esperava as entidades escapadas, obtido %q", text)
	}
	if !strings.Contains(text, "a &amp; b") {
		t.Fatalf("& sem escape, obtido %q", text)
	}
}

func TestTelegramErrorClassification(t *testing.T) {
	rateLimited := capturePost(t, `{"ok":false,"error_code":429,"description":"Too Many Requests"}`, nil)
	_, err := (telegramChannel{}).Send(context.Background(),
		map[string]any{"bot_token": "tok", "chat_id": "1", "base_url": rateLimited.URL}, singleMsg())
	if err == nil || IsPermanent(err) {
		t.Fatalf("429 deveria ser passível de nova tentativa, obtido %v", err)
	}

	forbidden := capturePost(t, `{"ok":false,"error_code":403,"description":"bot was blocked by the user"}`, nil)
	_, err = (telegramChannel{}).Send(context.Background(),
		map[string]any{"bot_token": "tok", "chat_id": "1", "base_url": forbidden.URL}, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("403 é problema de configuração e deveria ser falha permanente, obtido %v", err)
	}
}

func TestWebhookDefaultTemplateProducesValidJSON(t *testing.T) {
	// Este é o motivo de existir do template padrão: quando o título contém aspas e quebra de linha, qualquer forma ingênua como
	// `"title": "{{.Title}}"` produz JSON inválido. Já {{json .}} não.
	srv := capturePost(t, `{"ok":true}`, func(t *testing.T, body map[string]any, _ *http.Request) {
		if body["title"] != `[🟠 高危] 登录处 "SQL注入" 风险` {
			t.Errorf("título não foi restaurado corretamente: %v", body["title"])
		}
		items, _ := body["items"].([]any)
		if len(items) != 1 {
			t.Fatalf("o número de items deveria ser 1, obtido %d", len(items))
		}
		it, _ := items[0].(map[string]any)
		if it["summary"] != "参数 id\n未过滤 导致注入" {
			t.Errorf("resumo não foi restaurado corretamente: %v", it["summary"])
		}
		// Valores numéricos precisam ser números JSON e não strings (formas como json:"...,string" caem nessa armadilha).
		if _, ok := it["finding_id"].(float64); !ok {
			t.Errorf("finding_id deveria ser número, obtido %T", it["finding_id"])
		}
	})
	if _, err := (webhookChannel{}).Send(context.Background(), map[string]any{"url": srv.URL}, singleMsg()); err != nil {
		t.Fatalf("falha na entrega: %v", err)
	}
}

func TestWebhookCustomTemplateAndHeaders(t *testing.T) {
	srv := capturePost(t, `{"ok":true}`, func(t *testing.T, body map[string]any, r *http.Request) {
		if r.Header.Get("X-Token") != "s3cret" {
			t.Errorf("cabeçalho personalizado perdido: %v", r.Header)
		}
		if body["msg"] != "3 itens" {
			t.Errorf("a renderização do template personalizado está errada: %v", body["msg"])
		}
		if body["first"] != "漏洞1" {
			t.Errorf("a extração por range está errada: %v", body["first"])
		}
	})
	cfg := map[string]any{
		"url":           srv.URL,
		"headers":       map[string]any{"X-Token": "s3cret"},
		"body_template": `{"msg": {{json (printf "%d itens" .Count)}}, "first": {{json (index .Items 0).Name}}}`,
	}
	if _, err := (webhookChannel{}).Send(context.Background(), cfg, batchMsg(3)); err != nil {
		t.Fatalf("falha na entrega: %v", err)
	}
}

func TestWebhookRejectsNonJSONRenderResult(t *testing.T) {
	cfg := map[string]any{"url": "https://example.com/hook", "body_template": `not json at all`}
	_, err := (webhookChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("o resultado renderizado não é JSON e deveria ser falha permanente (template escrito errado, repetir não adianta), obtido %v", err)
	}
}

func TestWebhookValidateCatchesBadConfigEarly(t *testing.T) {
	bad := []map[string]any{
		{},
		{"url": "file:///etc/passwd"},
		{"url": "https://example.com", "method": "DELETE"},
		{"url": "https://example.com", "body_template": `{{.Items.`},
	}
	for i, cfg := range bad {
		if err := (webhookChannel{}).Validate(cfg); err == nil {
			t.Errorf("o conjunto de configuração nº %d deveria ser recusado: %v", i, cfg)
		}
	}
}

func TestEmailMessageIsWellFormed(t *testing.T) {
	msg, err := buildEmailMessage("artex@example.com", []string{"a@example.com", "b@example.com"}, singleMsg())
	if err != nil {
		t.Fatalf("falha ao montar o e-mail: %v", err)
	}
	if !strings.HasPrefix(msg, "From: artex@example.com\r\n") {
		t.Fatalf("cabeçalho From errado:\n%s", msg)
	}
	if !strings.Contains(msg, "To: a@example.com, b@example.com\r\n") {
		t.Fatalf("cabeçalho To errado:\n%s", msg)
	}
	// O assunto em chinês precisa ser codificado conforme a RFC 2047, senão o cliente o exibe como caracteres ilegíveis.
	if !strings.Contains(msg, "Subject: =?utf-8?") {
		t.Fatalf("o assunto não foi codificado conforme a RFC 2047:\n%s", msg)
	}
	if dec, err := new(mime.WordDecoder).DecodeHeader(mustExtractHeader(t, msg, "Subject")); err != nil {
		t.Fatalf("não foi possível decodificar o assunto: %v", err)
	} else if !strings.Contains(dec, "SQL注入") {
		t.Fatalf("conteúdo errado depois de decodificar o assunto: %q", dec)
	}

	// O corpo é base64, e o decodificado deve ser HTML válido.
	parts := strings.SplitN(msg, "\r\n\r\n", 2)
	if len(parts) != 2 {
		t.Fatal("o e-mail não tem a separação entre cabeçalho e corpo")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(strings.TrimSpace(parts[1]), "\r\n", ""))
	if err != nil {
		t.Fatalf("falha ao decodificar o corpo em base64: %v", err)
	}
	html := string(decoded)
	if !strings.HasPrefix(html, "<div") {
		t.Fatalf("o corpo não é HTML: %.80s", html)
	}
	// O título aparece como está na posição de texto: aspas duplas no conteúdo de texto HTML são caracteres válidos e não precisam de escape.
	// Aqui se assevera "preservado como está" para evitar que no futuro alguém acrescente por engano uma camada de escape de aspas, fazendo as aspas
	// chinesas aparecerem como &quot;.
	if !strings.Contains(html, `"SQL注入"`) {
		t.Fatalf("as aspas do título deveriam ser preservadas como estão na posição de texto: %.200s", html)
	}
}

// TestEmailEscapesStructuralInjection cobre a injeção que o corpo do e-mail realmente precisa evitar:
// título e resumo da vulnerabilidade vêm do alvo testado e da saída do modelo, e são conteúdo não confiável. Na posição de texto é preciso escapar
// & < > (senão dá para injetar tags), e na posição de atributo também é preciso escapar as aspas (senão dá para fechar o href).
func TestEmailEscapesStructuralInjection(t *testing.T) {
	m := Message{
		Items: []Item{{
			Severity:  "high",
			Name:      `<script>alert(1)</script>`,
			Summary:   "a & b > c",
			DetailURL: `https://artex.local/x?a="onmouseover=alert(1)`,
		}},
	}
	html := htmlBody(m, 0)
	if strings.Contains(html, "<script>") {
		t.Fatalf("título sem escape, é possível injetar tags: %s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("esperava as entidades escapadas: %s", html)
	}
	if !strings.Contains(html, "a &amp; b &gt; c") {
		t.Fatalf("& e > sem escape: %s", html)
	}
	// O link de retorno é o public_base_url configurável pelo administrador, com confiabilidade relativamente alta, mas na posição de atributo ainda é preciso
	// escapar as aspas — senão um endereço com aspas fecharia o href e injetaria um manipulador de evento.
	if strings.Contains(html, `onmouseover=alert(1)">`) {
		t.Fatalf("o atributo href não foi escapado corretamente: %s", html)
	}
	if !strings.Contains(html, "&quot;") {
		t.Fatalf("as aspas na posição de atributo deveriam ser escapadas: %s", html)
	}
}

func mustExtractHeader(t *testing.T, msg, name string) string {
	t.Helper()
	for _, line := range strings.Split(msg, "\r\n") {
		if strings.HasPrefix(line, name+": ") {
			return strings.TrimPrefix(line, name+": ")
		}
	}
	t.Fatalf("cabeçalho %s não encontrado", name)
	return ""
}

func TestChannelValidateReportsMissingFields(t *testing.T) {
	// Os erros de validação são exibidos diretamente a quem configura e precisam dizer claramente o que falta, e não um genérico "configuração inválida".
	cases := []struct {
		kind   string
		cfg    map[string]any
		substr string
	}{
		{KindDingTalk, map[string]any{}, "Webhook"},
		{KindFeishu, map[string]any{}, "Webhook"},
		{KindWeCom, map[string]any{}, "Webhook"},
		{KindTelegram, map[string]any{}, "Bot Token"},
		{KindTelegram, map[string]any{"bot_token": "t"}, "Chat ID"},
		{KindEmail, map[string]any{}, "SMTP"},
		{KindEmail, map[string]any{"host": "h"}, "porta"},
		{KindEmail, map[string]any{"host": "h", "port": 587, "from": "f"}, "destinatário"},
	}
	for _, tc := range cases {
		ch, ok := Get(tc.kind)
		if !ok {
			t.Fatalf("canal %s não registrado", tc.kind)
		}
		err := ch.Validate(tc.cfg)
		if err == nil {
			t.Errorf("a configuração de %s deveria falhar na validação: %v", tc.kind, tc.cfg)
			continue
		}
		if !strings.Contains(err.Error(), tc.substr) {
			t.Errorf("a mensagem de erro de %s deveria mencionar %q, obtido %q", tc.kind, tc.substr, err.Error())
		}
	}
}

// TestEmailSMTPErrorClassification trava a distinção semântica entre SMTP 4xx/5xx.
// Se 4xx também fosse julgado falha permanente, um servidor de e-mail com greylisting faria cada push cair em failed logo após a primeira
// tentativa — e o greylisting é justamente o cenário em que a nova tentativa automática mais deveria atuar.
func TestEmailSMTPErrorClassification(t *testing.T) {
	cases := []struct {
		reply     string
		permanent bool
	}{
		{"450 4.7.1 Greylisting in action, please come back later", false},
		{"451 4.3.0 Temporary system failure", false},
		{"452 4.2.2 Mailbox full", false},
		{"550 5.1.1 User unknown", true},
		{"553 5.1.3 Bad address syntax", true},
		{"554 5.7.1 Relay access denied", true},
		// Quando não se obtém o código de resposta, trata-se como "passível de nova tentativa": melhor tentar uma vez a mais do que condenar uma possível
		// falha transitória.
		{"unexpected EOF", false},
		{"", false},
	}
	for _, tc := range cases {
		err := smtpStageError("destinatário recusado", errors.New(tc.reply))
		if got := IsPermanent(err); got != tc.permanent {
			t.Errorf("resposta %q: esperava permanent=%v, obtido %v", tc.reply, tc.permanent, got)
		}
		// Independentemente da classificação, o texto original deve ser mantido para quem for investigar.
		if tc.reply != "" && !strings.Contains(err.Error(), tc.reply) {
			t.Errorf("o texto original da resposta %q foi descartado: %v", tc.reply, err)
		}
	}
}

func TestRegistryCoversAllKinds(t *testing.T) {
	// Os seis canais são indispensáveis — faltando um, ele some em silêncio do dropdown da UI.
	want := []string{KindDingTalk, KindEmail, KindFeishu, KindTelegram, KindWebhook, KindWeCom}
	got := Kinds()
	if len(got) != len(want) {
		t.Fatalf("o número de canais deveria ser %d, obtido %d: %v", len(want), len(got), got)
	}
	for _, k := range want {
		if !ValidKind(k) {
			t.Errorf("canal %s não registrado", k)
		}
		if ch, ok := Get(k); !ok || ch.Kind() != k {
			t.Errorf("o Kind() do canal %s não coincide com a chave de registro", k)
		}
	}
	if ValidKind("nope") {
		t.Error("um tipo não registrado não deveria passar na validação")
	}
}

func TestPermanentErrorUnwrap(t *testing.T) {
	base := &permanentSentinel{}
	err := Permanent(base)
	if !IsPermanent(err) {
		t.Fatal("deveria ser reconhecido como falha permanente")
	}
	if !strings.Contains(err.Error(), "sentinel") {
		t.Fatalf("a mensagem de erro deveria repassar a causa subjacente: %v", err)
	}
	if Permanent(nil) != nil {
		t.Fatal("Permanent(nil) deve retornar nil")
	}
	if IsPermanent(nil) {
		t.Fatal("nil não é falha permanente")
	}
}

type permanentSentinel struct{}

func (*permanentSentinel) Error() string { return "sentinel" }
