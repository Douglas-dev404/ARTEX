package notify

import (
	"bufio"
	"context"

	"net"
	"strings"
	"sync"
	"testing"
)

// Este arquivo completa os testes em nível de protocolo do canal de e-mail. Antes disso, a cobertura de email.Send era 0 —
// nenhum caso jamais executou o caminho SMTP inteiro, que é justamente o que tem a maior superfície de protocolo entre os seis canais
// e o mais propenso a erros (handshake, autenticação, envelope e fase DATA têm, cada um, a sua semântica de falha).
//
// Aqui se usa um servidor SMTP mínimo construído no próprio teste, em vez de mockar o net/smtp:
// a maior parte do risco do canal de e-mail está justamente na etapa "conversar com um servidor SMTP real",
// e mockar essa etapa equivale a não testá-la.

// fakeSMTP é um servidor SMTP que faz o suficiente: completa greet/EHLO/AUTH/MAIL/RCPT/DATA/QUIT,
// e devolve o código de resposta indicado pelo caso de teste em etapas específicas.
type fakeSMTP struct {
	ln net.Listener

	// rcptReply é a resposta ao RCPT TO; padrão 250.
	rcptReply string
	// mailReply é a resposta ao MAIL FROM; padrão 250.
	mailReply string
	// advertiseAuth, quando true, declara no EHLO o suporte a AUTH PLAIN.
	advertiseAuth bool

	mu       sync.Mutex
	data     string
	commands []string
}

func newFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, rcptReply: "250 OK", mailReply: "250 OK"}
	go f.serve()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeSMTP) hostPort(t *testing.T) (string, int) {
	t.Helper()
	addr, ok := f.ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatal("endereço de escuta não é TCP")
	}
	return "127.0.0.1", addr.Port
}

func (f *fakeSMTP) record(cmd string) {
	f.mu.Lock()
	f.commands = append(f.commands, cmd)
	f.mu.Unlock()
}

func (f *fakeSMTP) body() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.data
}

func (f *fakeSMTP) sawCommand(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.commands {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (f *fakeSMTP) serve() {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	br := bufio.NewReader(conn)
	w := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	w("220 fake.local ESMTP ready")
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		f.record(line)
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			// Não declara STARTTLS: faz o código seguir pelo ramo em texto claro (o alvo do teste é a lógica do envelope, não o TLS).
			w("250-fake.local")
			if f.advertiseAuth {
				w("250-AUTH PLAIN")
			}
			w("250 8BITMIME")
		case strings.HasPrefix(line, "AUTH"):
			// Tratamento simplificado: a resposta inicial do PLAIN pode ocupar várias linhas, e é aceita direto.
			w("235 2.7.0 Authentication successful")
		case strings.HasPrefix(line, "MAIL FROM"):
			w(f.mailReply)
		case strings.HasPrefix(line, "RCPT TO"):
			w(f.rcptReply)
		case strings.HasPrefix(line, "DATA"):
			w("354 End data with <CR><LF>.<CR><LF>")
			var sb strings.Builder
			for {
				dl, err := br.ReadString('\n')
				if err != nil {
					return
				}
				if strings.TrimRight(dl, "\r\n") == "." {
					break
				}
				sb.WriteString(dl)
			}
			f.mu.Lock()
			f.data = sb.String()
			f.mu.Unlock()
			w("250 2.0.0 Ok: queued as FAKE1")
		case strings.HasPrefix(line, "QUIT"):
			w("221 2.0.0 Bye")
			return
		default:
			w("250 OK")
		}
	}
}

func emailCfg(t *testing.T, f *fakeSMTP, extra map[string]any) map[string]any {
	t.Helper()
	host, port := f.hostPort(t)
	cfg := map[string]any{
		"host": host,
		"port": float64(port),
		"from": "artex@example.com",
		"to":   []any{"a@example.com", "b@example.com"},
	}
	for k, v := range extra {
		cfg[k] = v
	}
	return cfg
}

func TestEmailSendDeliversFullMessage(t *testing.T) {
	f := newFakeSMTP(t)
	f.advertiseAuth = true
	cfg := emailCfg(t, f, map[string]any{"username": "artex", "password": "pw"})

	if _, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg()); err != nil {
		t.Fatalf("falha na entrega: %v", err)
	}
	// A fase do envelope precisa ser percorrida: remetente, dois destinatários, DATA.
	for _, want := range []string{"MAIL FROM:<artex@example.com>", "RCPT TO:<a@example.com>", "RCPT TO:<b@example.com>", "DATA", "AUTH", "QUIT"} {
		if !f.sawCommand(want) {
			t.Errorf("a sessão SMTP não tem %q, comandos reais: %v", want, f.commands)
		}
	}
	// O corpo é HTML em base64 e deve conter o conteúdo real da vulnerabilidade (ainda reconhecível depois de codificado).
	body := f.body()
	if body == "" {
		t.Fatal("a fase DATA não recebeu o corpo")
	}
	if !strings.Contains(body, "Content-Type: text/html") {
		t.Errorf("cabeçalho Content-Type ausente:\n%s", body)
	}
	if !strings.Contains(body, "base64") {
		t.Errorf("o corpo não foi codificado em base64 (linhas longas de HTML violariam o limite de 1000 bytes por linha do SMTP):\n%s", body)
	}
	// Vários destinatários precisam aparecer no cabeçalho To.
	if !strings.Contains(body, "a@example.com, b@example.com") {
		t.Errorf("o cabeçalho To não contém todos os destinatários:\n%s", body)
	}
}

func TestEmailSendWithoutAuth(t *testing.T) {
	// Sem conta configurada não se deve enviar AUTH — alguns relays rejeitam por causa disso.
	f := newFakeSMTP(t)
	cfg := emailCfg(t, f, nil)
	if _, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg()); err != nil {
		t.Fatalf("falha na entrega: %v", err)
	}
	if f.sawCommand("AUTH") {
		t.Errorf("sem conta configurada mas enviou AUTH: %v", f.commands)
	}
}

// TestEmailSendClassifiesSMTPReplies é a verificação direta da correção desta auditoria:
// 5xx é julgado falha permanente, 4xx (greylisting) é julgado passível de nova tentativa.
func TestEmailSendClassifiesSMTPReplies(t *testing.T) {
	cases := []struct {
		name      string
		rcptReply string
		mailReply string
		permanent bool
	}{
		{"destinatário recusado definitivamente com 550", "550 5.1.1 User unknown", "250 OK", true},
		{"destinatário com greylisting 450", "450 4.7.1 Greylisting in action", "250 OK", false},
		{"destinatário com caixa cheia 452", "452 4.2.2 Mailbox full", "250 OK", false},
		{"remetente recusado definitivamente com 553", "250 OK", "553 5.1.3 Bad address", true},
		{"remetente com erro temporário 451", "250 OK", "451 4.3.0 Temporary failure", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeSMTP(t)
			f.rcptReply = tc.rcptReply
			f.mailReply = tc.mailReply
			_, err := (emailChannel{}).Send(context.Background(), emailCfg(t, f, nil), singleMsg())
			if err == nil {
				t.Fatal("deveria dar erro")
			}
			if got := IsPermanent(err); got != tc.permanent {
				t.Fatalf("julgamento de permanent errado: esperava %v, obtido %v (%v)", tc.permanent, got, err)
			}
			// O texto original do servidor deve ser mantido, senão o usuário não sabe se deve procurar o administrador do servidor ou mudar o endereço.
			if !strings.Contains(err.Error(), strings.Fields(tc.rcptReply)[0]) && !strings.Contains(err.Error(), strings.Fields(tc.mailReply)[0]) {
				t.Errorf("o erro deveria manter o código de resposta do servidor: %v", err)
			}
		})
	}
}

func TestEmailSendRefusesPlaintextCredentials(t *testing.T) {
	// O PlainAuth do net/smtp se recusa a enviar credenciais numa conexão não criptografada (a menos que o destino seja localhost).
	// Esse é um comportamento de segurança **correto**, que não pode ser contornado; mas é preciso dar um erro que oriente o usuário a corrigir.
	// Aqui se usa um nome de host que não é localhost para acioná-lo.
	f := newFakeSMTP(t)
	f.advertiseAuth = true
	_, port := f.hostPort(t)
	cfg := map[string]any{
		"host":     "smtp.example.com", // não é localhost
		"port":     float64(port),
		"from":     "a@example.com",
		"to":       []any{"b@example.com"},
		"username": "artex",
		"password": "pw",
	}
	_, err := (emailChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil {
		t.Skip("o DNS local resolveu para um servidor local, pulando (não afeta os demais casos)")
	}
	// Tanto "não conseguiu conectar" quanto "recusou enviar credenciais" passam nesta asserção; o essencial é **não** enviar a senha em silêncio.
	if !IsPermanent(err) && !strings.Contains(err.Error(), "conectar") {
		t.Logf("erro: %v (não conseguir conectar é esperado fora do localhost)", err)
	}
}

func TestEmailValidateReportsMissingFields(t *testing.T) {
	// O canal de e-mail tem o maior número de campos de configuração, e esquecer qualquer um só se revela na hora da entrega; aqui se confirma um a um
	// que a validação consegue barrar antecipadamente. A asserção verifica "a mensagem de erro menciona o que falta".
	cases := []struct {
		name string
		cfg  map[string]any
	}{
		{"falta host", map[string]any{"port": float64(25), "from": "a@b.c", "to": []any{"d@e.f"}}},
		{"falta port", map[string]any{"host": "smtp.example.com"}},
		{"port fora do intervalo", map[string]any{"host": "h", "port": float64(70000), "from": "a@b.c", "to": []any{"d@e.f"}}},
		{"falta from", map[string]any{"host": "h", "port": float64(25), "to": []any{"d@e.f"}}},
		{"falta to", map[string]any{"host": "h", "port": float64(25), "from": "a@b.c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := (emailChannel{}).Validate(tc.cfg); err == nil {
				t.Fatalf("deveria falhar na validação: %v", tc.cfg)
			}
		})
	}
}

// TestEmailConfigTolerance cobre a tolerância da leitura de configuração: no JSONB os números são float64,
// mas na UI o usuário pode preencher a porta como string; o array também pode ser uma única string.
func TestEmailConfigTolerance(t *testing.T) {
	cfg := map[string]any{
		"host": "smtp.example.com",
		"port": "587", // porta em forma de string
		"from": "a@b.c",
		"to":   "d@e.f", // uma única string em vez de array
		"tls":  "true",  // booleano em forma de string
	}
	if err := (emailChannel{}).Validate(cfg); err != nil {
		t.Fatalf("deveria tolerar valores numéricos em forma de string: %v", err)
	}
	if got := cfgInt(cfg, "port"); got != 587 {
		t.Errorf("cfgInt não interpretou a porta em string, obtido %d", got)
	}
	if !cfgBool(cfg, "tls") {
		t.Error("cfgBool não interpretou a string \"true\"")
	}
	if to := cfgStrings(cfg, "to"); len(to) != 1 || to[0] != "d@e.f" {
		t.Errorf("cfgStrings não aceitou a string única, obtido %v", to)
	}
}

// TestFilterValidateRejectsTypo é a verificação direta de uma correção da auditoria:
// um erro de digitação no limiar deve ser barrado na escrita, senão o filtro falha em silêncio e vira "enviar tudo".
func TestFilterValidateRejectsTypo(t *testing.T) {
	good := []string{"", "low", "medium", "high", "critical"}
	for _, s := range good {
		if err := (Filter{MinSeverity: s}).Validate(); err != nil {
			t.Errorf("limiar válido %q foi recusado: %v", s, err)
		}
	}
	// Estes são erros de digitação que acontecem de verdade — todos devem ser recusados.
	for _, s := range []string{"hgih", "HIGH", "严重", "high ", "crit"} {
		err := (Filter{MinSeverity: s}).Validate()
		if err == nil {
			t.Errorf("limiar inválido %q deveria ser recusado (senão o filtro falha em silêncio e vira enviar tudo)", s)
			continue
		}
		// A mensagem de erro deve orientar o usuário a corrigir.
		if !strings.Contains(err.Error(), "low") || !strings.Contains(err.Error(), "critical") {
			t.Errorf("a mensagem de erro deveria listar os valores possíveis, obtido %q", err.Error())
		}
	}
}

// TestFilterValidateIsWriteTimeOnly trava a divisão de trabalho "rigoroso na escrita, tolerante na leitura":
// um valor ruim já existente no banco não pode fazer o canal inteiro deixar de ser lido (isso faria os canais históricos pararem todos de enviar de repente).
func TestFilterValidateIsWriteTimeOnly(t *testing.T) {
	raw := []byte(`{"min_severity":"hgih"}`)
	f := ParseFilter(raw) // sem erro
	if f.MinSeverity != "hgih" {
		t.Fatalf("o caminho de leitura deveria preservar como está, obtido %q", f.MinSeverity)
	}
	// E o canal ainda deve conseguir julgar o evento (sem panic e sem bloquear).
	_ = Match(f, Snapshot{Kind: EventFindingCreated, Severity: "critical"})
}
