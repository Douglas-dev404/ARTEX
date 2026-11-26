package notify

import (
	"net/url"
	"testing"
	"time"
)

// Os valores de referência da assinatura foram calculados de forma independente pelo OpenSSL, e não gerados com a implementação deste pacote —
// senão só se provaria que "o código não mudou", e não que "o algoritmo está certo".
//
//	TS=1700000000000, SECRET=SECtest123
//	DingTalk: printf '%s\n%s' "$TS" "$SECRET" | openssl dgst -sha256 -hmac "$SECRET" -binary | openssl base64 -A
//	      -> w3RMHXzixTMdzr8OHJUmVLS4IoPJVdu+Ut1LE48MePE=
//	Feishu: printf '' | openssl dgst -sha256 -hmac "$(printf '%s\n%s' "$TS" "$SECRET")" -binary | openssl base64 -A
//	      -> Hd4xFWQU6R6ad4nzy4ETIznzlqebqH7xcTFVmONTudo=
const (
	signTestTSMillis = int64(1700000000000)
	signTestSecret   = "SECtest123"
	dingTalkExpected = "w3RMHXzixTMdzr8OHJUmVLS4IoPJVdu+Ut1LE48MePE="
	feishuExpected   = "Hd4xFWQU6R6ad4nzy4ETIznzlqebqH7xcTFVmONTudo="
)

func TestDingTalkSignMatchesReference(t *testing.T) {
	got, err := dingTalkSignedURL("https://oapi.dingtalk.com/robot/send?access_token=tok", signTestSecret, time.UnixMilli(signTestTSMillis))
	if err != nil {
		t.Fatalf("falha na assinatura: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("endereço produzido não interpretável: %v", err)
	}
	q := u.Query()
	if q.Get("sign") != dingTalkExpected {
		t.Errorf("assinatura não confere\nesperado %s\nobtido %s", dingTalkExpected, q.Get("sign"))
	}
	if q.Get("timestamp") != "1700000000000" {
		t.Errorf("o timestamp deveria estar em milissegundos e ser incluído como está, obtido %q", q.Get("timestamp"))
	}
	// Os parâmetros de query originais (access_token) não podem ser sobrescritos pela assinatura.
	if q.Get("access_token") != "tok" {
		t.Errorf("parâmetro de query original perdido, obtido %q", q.Get("access_token"))
	}
}

func TestFeishuSignMatchesReference(t *testing.T) {
	got := feishuSign("1700000000000", signTestSecret)
	if got != feishuExpected {
		t.Errorf("assinatura não confere\nesperado %s\nobtido %s", feishuExpected, got)
	}
}

// TestSignAlgorithmsDiffer trava a diferença de algoritmo entre os dois canais. Um é exatamente o inverso do outro na ordem dos parâmetros
// (DingTalk key=secret, Feishu key=string a assinar), e copiar de um para o outro falha na validação com certeza;
// este caso garante que uma refatoração futura não vai fundir os dois numa mesma função.
func TestSignAlgorithmsDiffer(t *testing.T) {
	ts := "1700000000000"
	dingURL, err := dingTalkSignedURL("https://example.com/hook", signTestSecret, time.UnixMilli(signTestTSMillis))
	if err != nil {
		t.Fatal(err)
	}
	dq, _ := url.Parse(dingURL)
	if dq.Query().Get("sign") == feishuSign(ts, signTestSecret) {
		t.Fatal("as assinaturas do DingTalk e do Feishu são iguais, o que indica que o algoritmo de um dos canais foi implementado errado")
	}
}

func TestDingTalkNoSecretLeavesURLUntouched(t *testing.T) {
	// Robô sem assinatura habilitada: não se pode acrescentar do nada os parâmetros timestamp/sign.
	const hook = "https://oapi.dingtalk.com/robot/send?access_token=tok"
	got, err := dingTalkSignedURL(hook, "", time.UnixMilli(signTestTSMillis))
	if err != nil {
		t.Fatal(err)
	}
	if got != hook {
		t.Fatalf("sem secret configurado o endereço não deveria ser alterado, obtido %q", got)
	}
}

func TestValidateHTTPURL(t *testing.T) {
	ok := []string{"https://example.com/hook", "http://10.0.0.1:8080/x?y=1"}
	for _, s := range ok {
		if err := validateHTTPURL(s); err != nil {
			t.Errorf("%q deveria ser aceito: %v", s, err)
		}
	}
	// file:// e semelhantes não devem ser liberados — o tratamento que o http.Client dá a eles vai além do escopo esperado.
	bad := []string{"", "file:///etc/passwd", "ftp://example.com", "https://", "gopher://x"}
	for _, s := range bad {
		if err := validateHTTPURL(s); err == nil {
			t.Errorf("%q deveria ser recusado", s)
		}
	}
}
