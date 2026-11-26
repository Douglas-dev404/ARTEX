package notify

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// emailDialTimeout / emailSessionTimeout limitam, respectivamente, o estabelecimento da conexão e a sessão SMTP inteira.
// O net/smtp não tem nenhum mecanismo de timeout próprio; sem estes dois limites, um par travado deixaria
// a goroutine de entrega pendurada para sempre — e o dispatcher processa em série numa única goroutine,
// o que equivale a paralisar todo o sistema de notificações.
const (
	emailDialTimeout    = 10 * time.Second
	emailSessionTimeout = 45 * time.Second
)

// emailChannel implementa a entrega de e-mail via SMTP.
type emailChannel struct{}

func (emailChannel) Kind() string { return KindEmail }

// E-mail não tem limitação de taxa da plataforma, mas não deve ser usado para inundar; define-se um valor padrão folgado.
func (emailChannel) DefaultRatePerMin() int { return 60 }

// Mascara apenas a senha. Host SMTP, conta e destinatários não são segredo; mascará-los só tornaria a edição mais trabalhosa.
func (emailChannel) SecretKeys() []string { return []string{"password"} }

// host/port determinam a qual servidor a senha é entregue; tls determina se a transmissão é criptografada. A mudança de qualquer um dos três
// exige se manifestar novamente sobre a senha — o que também obriga que "desligar o TLS" seja feito levando a credencial explicitamente, e não como um ajuste casual.
func (emailChannel) DestinationKeys() []string { return []string{"host", "port", "tls"} }

func (emailChannel) Validate(cfg map[string]any) error {
	if cfgString(cfg, "host") == "" {
		return errors.New("endereço do servidor SMTP ausente")
	}
	port := cfgInt(cfg, "port")
	if port <= 0 || port > 65535 {
		return errors.New("porta SMTP inválida (deve ser 1-65535)")
	}
	if cfgString(cfg, "from") == "" {
		return errors.New("endereço do remetente ausente")
	}
	if len(cfgStrings(cfg, "to")) == 0 {
		return errors.New("é necessário ao menos um endereço de destinatário")
	}
	return nil
}

func (c emailChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	host := cfgString(cfg, "host")
	port := cfgInt(cfg, "port")
	from := cfgString(cfg, "from")
	to := cfgStrings(cfg, "to")
	username := cfgString(cfg, "username")
	password := cfgString(cfg, "password")
	implicitTLS := cfgBool(cfg, "tls")

	msg, err := buildEmailMessage(from, to, m)
	if err != nil {
		return 0, Permanent(err)
	}

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	client, err := emailDial(ctx, addr, host, implicitTLS)
	if err != nil {
		return 0, err
	}
	defer client.Close()

	// STARTTLS: se o outro lado suportar, faz o upgrade. Em sessão em texto claro não se podem enviar credenciais (veja a nota sobre auth abaixo).
	if !implicitTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
				return 0, fmt.Errorf("falha no STARTTLS: %w", err)
			}
		}
	}
	if username != "" {
		if err := client.Auth(smtp.PlainAuth("", username, password, host)); err != nil {
			// smtp.PlainAuth se recusa a enviar credenciais numa conexão não criptografada (a menos que o destino seja localhost).
			// Esse é um comportamento de segurança **correto**, que não deve ser contornado, mas é preciso traduzir o motivo com clareza —
			// caso contrário o usuário só veria "unencrypted connection" sem saber o que fazer.
			if strings.Contains(err.Error(), "unencrypted connection") {
				return 0, Permanent(fmt.Errorf("credenciais não enviadas: a conexão não está criptografada. Habilite o TLS, ou use a porta 465 (TLS implícito), ou marque \"Habilitar TLS\" (%w)", err))
			}
			return 0, Permanent(fmt.Errorf("falha na autenticação SMTP: %w", err))
		}
	}
	if err := client.Mail(from); err != nil {
		return 0, smtpStageError(fmt.Sprintf("remetente %s recusado", from), err)
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return 0, smtpStageError(fmt.Sprintf("destinatário %s recusado", rcpt), err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return 0, fmt.Errorf("falha no SMTP DATA: %w", err)
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return 0, fmt.Errorf("falha ao gravar o corpo do e-mail: %w", err)
	}
	if err := w.Close(); err != nil {
		return 0, fmt.Errorf("falha ao enviar o e-mail: %w", err)
	}
	// A falha do Quit não afeta o fato de que "o e-mail já foi aceito pelo servidor", portanto seu erro é ignorado.
	_ = client.Quit()
	// E-mail não tem truncamento de tamanho (o corpo HTML é enviado por inteiro), então o lote inteiro conta como entregue.
	return len(m.Items), nil
}

// emailDial estabelece a conexão SMTP.
//
// implicitTLS=true usa o modo "conectou, já é TLS", como na porta 465; false usa a conexão em texto claro em 25/587 e depois
// STARTTLS. Os dois não podem ser misturados: enviar um greeting em texto claro para a porta 465 faz a conexão ser derrubada na hora.
//
// O prazo da sessão é definido **já na criação da conexão** (e não depois), porque o Client do net/smtp esconde a conexão
// subjacente num campo não exportado, inacessível de fora; uma vez entregue a conexão, só o deadline definido previamente
// serve de rede de segurança. Isso também cobre o bloqueio na fase de handshake.
// O Control usa blockInternalDial, a mesma guarda compartilhada com os canais baseados em HTTP. Sem ela, o SMTP
// seria a brecha de toda a proteção contra SSRF: preencher host com 169.254.169.254 ou 127.0.0.1 conectaria direto,
// e quando o handshake do smtp.NewClient falha, ele embrulha no erro a linha devolvida pelo outro lado, que é ecoada via last_error
// pela interface de histórico de entregas, formando uma primitiva de leitura semi-cega; a diferença de tempo entre "conexão recusada" e "timeout" também
// poderia ser usada para sondar portas. A fase de conexão é o ponto em que a proteção de fato vale, e também cobre DNS rebinding.
func emailDial(ctx context.Context, addr, host string, implicitTLS bool) (*smtp.Client, error) {
	d := &net.Dialer{Timeout: emailDialTimeout, Control: blockInternalDial}
	var conn net.Conn
	var err error
	if implicitTLS {
		conn, err = tls.DialWithDialer(d, "tcp", addr, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("falha ao conectar ao servidor SMTP: %w", err)
	}
	_ = conn.SetDeadline(time.Now().Add(emailSessionTimeout))
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("falha no handshake SMTP: %w", err)
	}
	return client, nil
}

// smtpStageError classifica, pelo código de resposta SMTP, a falha de uma etapa em "pode tentar de novo" ou "falha permanente".
//
// Por que é preciso distinguir: o 4xx e o 5xx do SMTP têm semânticas totalmente diferentes —
//   - 4xx (450 greylisting, 451 erro local, 452 armazenamento insuficiente) são recusas **temporárias**,
//     e o correto é tentar de novo mais tarde; o greylisting, em especial, é encontrado em quase toda primeira entrega.
//   - 5xx (550 usuário inexistente, 553 endereço inválido) são recusas permanentes, e repetir não faz sentido.
//
// Se tudo fosse falha permanente, um servidor de e-mail com greylisting habilitado faria **cada** push cair em failed
// logo na primeira tentativa — e esse tipo de falha é exatamente o cenário em que a nova tentativa automática mais deveria atuar.
// O código de resposta é obtido dos três primeiros dígitos do texto do erro; se não for possível obtê-lo, trata-se como passível de nova tentativa (melhor tentar uma vez a mais
// do que condenar uma possível falha transitória só por não conseguir interpretar).
func smtpStageError(what string, err error) error {
	code := smtpReplyCode(err.Error())
	if code >= 500 && code < 600 {
		return Permanent(fmt.Errorf("%s: %w", what, err))
	}
	return fmt.Errorf("%s: %w", what, err)
}

// smtpReplyCode obtém do texto do erro SMTP o código de resposta de três dígitos inicial; retorna 0 se não conseguir.
// O net/smtp não exporta o campo do código de erro, então só dá para obtê-lo do texto; o formato é "450 4.7.1 ...".
func smtpReplyCode(text string) int {
	if len(text) < 3 {
		return 0
	}
	n, err := strconv.Atoi(text[:3])
	if err != nil {
		return 0
	}
	return n
}

// buildEmailMessage monta o e-mail completo conforme a RFC 5322.
//
// O corpo é codificado em base64 por dois motivos: primeiro, o SMTP determina que uma linha não passe de 1000 bytes, e o corpo
// HTML (especialmente em e-mails consolidados) facilmente tem linhas longas demais; segundo, o base64 naturalmente nunca gera uma linha começando com "."
// o que dispensa o trabalho de escapar pontos no SMTP.
func buildEmailMessage(from string, to []string, m Message) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	// O assunto em chinês precisa ser codificado conforme a RFC 2047, caso contrário o cliente o exibirá como caracteres ilegíveis.
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", htmlTitle(m)))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	// E-mail não tem limite rígido de tamanho, portanto o corpo não é truncado.
	b.WriteString("\r\n")
	encoded := base64.StdEncoding.EncodeToString([]byte(htmlBody(m, 0)))
	// base64 quebra linha a cada 76 caracteres, conforme a RFC 2045.
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded + "\r\n")
	return b.String(), nil
}
