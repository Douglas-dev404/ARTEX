package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// Este arquivo cobre a classificação de erros da camada HTTP do doJSON.
//
// Por que testar à parte: os adaptadores de cada canal só cuidam dos códigos de erro de negócio da própria plataforma (errcode do DingTalk,
// code do Feishu, campo ok do Telegram), enquanto a classificação da **camada HTTP** é feita de forma unificada pelo doJSON,
// e as duas são linhas de defesa independentes. Sem esta, um gateway intermediário que devolvesse 503 seria tratado como falha permanente,
// desistindo direto da nova tentativa; e um 403 seria tratado como passível de nova tentativa, gastando três rodadas de recuo à toa.

func replyServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestDoJSONClassifiesHTTPStatus(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		permanent bool
	}{
		{"200 sucesso não conta como erro", 200, false},
		{"429 limitação de taxa, passível de nova tentativa", 429, false},
		{"408 timeout da requisição, passível de nova tentativa", 408, false},
		{"500 erro do servidor, passível de nova tentativa", 500, false},
		{"502 erro de gateway, passível de nova tentativa", 502, false},
		{"503 serviço indisponível, passível de nova tentativa", 503, false},
		{"400 parâmetro errado, falha permanente", 400, true},
		{"401 falha de autenticação, falha permanente", 401, true},
		{"403 acesso proibido, falha permanente", 403, true},
		{"404 endereço inexistente, falha permanente", 404, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := replyServer(t, tc.status, `{"detail":"upstream says no"}`)
			_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
			if tc.status < 300 {
				if err != nil {
					t.Fatalf("2xx não deveria dar erro: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("não 2xx deveria dar erro")
			}
			if got := IsPermanent(err); got != tc.permanent {
				t.Fatalf("julgamento de permanent errado para HTTP %d: esperava %v, obtido %v (%v)",
					tc.status, tc.permanent, got, err)
			}
			// O código de status deve aparecer no erro, senão o usuário não tem como saber se configurou errado ou se o outro lado caiu.
			// Assevera-se o número e não o StatusText em inglês do Go: o texto deste pacote é em português
			// (como no resto do projeto), e o número é a parte independente de idioma e que dá para asseverar de forma estável.
			if !strings.Contains(err.Error(), strconv.Itoa(tc.status)) {
				t.Errorf("a mensagem de erro deveria incluir o código de status HTTP %d, obtido %v", tc.status, err)
			}
		})
	}
}

// TestDoJSONIncludesResponseSnippet cobre o snippet: a explicação de erro devolvida pelo outro lado precisa ser trazida de volta,
// senão o usuário só sabe que "falhou", sem saber por que o outro lado recusou.
func TestDoJSONIncludesResponseSnippet(t *testing.T) {
	srv := replyServer(t, 400, `{"error":"invalid webhook token"}`)
	_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("deveria dar erro")
	}
	if !strings.Contains(err.Error(), "invalid webhook token") {
		t.Errorf("a mensagem de erro deveria trazer a explicação do outro lado, obtido %v", err)
	}
}

// TestDoJSONSnippetIsSingleLineAndBounded restringe a forma do snippet:
// a resposta do outro lado entra como está na coluna last_error e na tabela do front-end, e várias linhas/tamanho excessivo estragariam o layout e a carga.
func TestDoJSONSnippetIsSingleLineAndBounded(t *testing.T) {
	// Resposta com quebra de linha, tabulação e conteúdo longo demais de 5000 caracteres.
	long := strings.Repeat("x", 5000)
	srv := replyServer(t, 500, "line1\nline2\r\n\tline3 "+long)
	_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("deveria dar erro")
	}
	msg := err.Error()
	if strings.ContainsAny(msg, "\r\n\t") {
		t.Errorf("a mensagem de erro deveria ser comprimida em uma linha, obtido %q", msg)
	}
	// Limite do snippet de 200 caracteres + prefixo fixo; o total deve ser muito menor que a resposta original.
	if len(msg) > 400 {
		t.Errorf("mensagem de erro longa demais (%d bytes), deveria ser truncada pelo snippet: %q", len(msg), msg)
	}
}

// TestDoJSONRejectsOversizedResponse confirma que a leitura tem limite: quando o outro lado devolve conteúdo enorme de forma anormal,
// não se pode ler a resposta inteira para a memória (cada registro do histórico de entregas guarda uma cópia de last_error).
func TestDoJSONRejectsOversizedResponse(t *testing.T) {
	huge := strings.Repeat("A", 1<<20) // 1 MiB
	srv := replyServer(t, 400, huge)
	_, err := doJSON(context.Background(), "GET", srv.URL, nil, nil)
	if err == nil {
		t.Fatal("deveria dar erro")
	}
	if len(err.Error()) > 400 {
		t.Errorf("uma resposta grande demais deveria ser lida com limite e truncada, tamanho da mensagem de erro %d", len(err.Error()))
	}
}
