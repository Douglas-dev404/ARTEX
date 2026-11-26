package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// GetSetting, para "chave não existe" e "erro de leitura", difere só por um error: em ambos os casos value é
// string vazia. Se um handler relacionado à senha tratar o error como "senha ainda não definida", durante
// uma instabilidade do banco ele deixa a porta de inicialização aberta — o authInit liberaria uma requisição não autenticada para sobrescrever a senha de administrador existente,
// e o authStatus mandaria o frontend direto para /setup fazer exatamente isso.
//
// Estes dois testes fecham o pool de conexões do handler para provocar a falha de leitura e afirmam que ambos os pontos fazem fail closed.
func TestAuthStatusFailsClosedWhenDataSourceUnavailable(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()
	// Fecha o pool para que o GetSetting seguinte retorne error em vez de sql.ErrNoRows.
	if err := m.pg.Close(); err != nil {
		t.Fatal(err)
	}

	s := &Server{m: m}
	w := httptest.NewRecorder()
	s.authStatus(w, httptest.NewRequest("GET", "/api/auth/status", nil))

	if w.Code != 503 {
		t.Fatalf("status=%d want 503 (falha de leitura tratada como não inicializado mandaria o usuário a /setup sobrescrever a senha); body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Initialized *bool `json:"initialized"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err == nil && payload.Initialized != nil {
		t.Fatalf("em falha de leitura não deve responder initialized, obtido %v", *payload.Initialized)
	}
}

func TestAuthInitFailsClosedWhenDataSourceUnavailable(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer m.Close()
	if err := m.pg.Close(); err != nil {
		t.Fatal(err)
	}

	s := &Server{m: m}
	w := httptest.NewRecorder()
	body := strings.NewReader(`{"password":"correct horse battery"}`)
	s.authInit(w, httptest.NewRequest("POST", "/api/auth/init", body))

	if w.Code != 503 {
		t.Fatalf("status=%d want 503 (liberar em falha de leitura deixaria requisição não autenticada sobrescrever a senha existente); body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "token") {
		t.Fatalf("em falha de leitura não deve emitir token: %s", w.Body.String())
	}
}

func TestValidatePassword(t *testing.T) {
	for _, tc := range []struct {
		name, pw string
		wantErr  bool
	}{
		{"vazia", "", true},
		{"sete posições", "1234567", true},
		{"oito posições", "12345678", false},
		{"oito ideogramas contam por caractere e não por byte", "密码密码密码密码", false},
		{"três ideogramas somam 9 bytes mas só 3 caracteres", "密码强", true},
		{"72 bytes", strings.Repeat("a", 72), false},
		{"73 bytes excede o limite do bcrypt", strings.Repeat("a", 73), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validatePassword(tc.pw); (got != "") != tc.wantErr {
				t.Fatalf("validatePassword(%q)=%q, wantErr=%v", tc.pw, got, tc.wantErr)
			}
		})
	}
}
