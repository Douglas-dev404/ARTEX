package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"
)

// dingTalkChannel implementa o robô personalizado do DingTalk.
//
// Características da plataforma (que determinam as escolhas de implementação aqui):
//   - Limite de 20 mensagens/minuto por robô; o excedente é descartado silenciosamente (o HTTP ainda pode ser 200),
//     então a limitação de taxa precisa ser feita no cliente, veja DefaultRatePerMin.
//   - A configuração de segurança é uma entre três: assinatura / palavra-chave personalizada / lista de IPs permitidos. A assinatura é a única
//     opção que não depende do conteúdo da mensagem, por isso só ela é suportada (também se aceita o webhook puro, sem nenhuma das três).
//   - Sucesso e falha retornam ambos HTTP 200, distinguidos pelo errcode no body — sem verificar o errcode,
//     uma falha de entrega seria registrada como sucesso.
type dingTalkChannel struct{}

func (dingTalkChannel) Kind() string { return KindDingTalk }

func (dingTalkChannel) DefaultRatePerMin() int { return 20 }

// O endereço do Webhook do DingTalk contém o access_token, que já é uma credencial, por isso é mascarado por inteiro.
func (dingTalkChannel) SecretKeys() []string { return []string{"webhook", "secret"} }

// O destino é o próprio endereço do Webhook do DingTalk; ao alterar o endereço é preciso se manifestar novamente sobre a chave de assinatura para o novo endereço.
func (dingTalkChannel) DestinationKeys() []string { return []string{"webhook"} }

func (dingTalkChannel) Validate(cfg map[string]any) error {
	hook := cfgString(cfg, "webhook")
	if hook == "" {
		return errors.New("Endereço do Webhook ausente")
	}
	if err := validateHTTPURL(hook); err != nil {
		return fmt.Errorf("Webhook 地址无效: %w", err)
	}
	return nil
}

// Send entrega uma mensagem. Com link de retorno e item único usa ActionCard (com botão); caso contrário, usa markdown.
func (c dingTalkChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	hook := cfgString(cfg, "webhook")
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	endpoint, err := dingTalkSignedURL(hook, cfgString(cfg, "secret"), time.Now())
	if err != nil {
		return 0, Permanent(err)
	}

	title := markdownTitle(m)
	// O corpo markdown do DingTalk não tem limite de bytes explícito, mas ainda assim aplicamos um limite de proteção para evitar que o campo de evidências cresça de forma anormal.
	text, kept := markdownBody(m, 20000)

	var payload any
	if !m.Batch && len(m.Items) == 1 && m.Items[0].DetailURL != "" {
		payload = map[string]any{
			"msgtype": "actionCard",
			"actionCard": map[string]any{
				"title":          title,
				"text":           text,
				"btnOrientation": "0",
				"singleTitle":    "Ver detalhes",
				"singleURL":      m.Items[0].DetailURL,
			},
		}
	} else {
		payload = map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]any{"title": title, "text": text},
		}
	}

	raw, err := doJSON(ctx, "POST", endpoint, nil, payload)
	if err != nil {
		return 0, err
	}
	// O DingTalk esconde os erros de negócio dentro da resposta 200.
	var res struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("falha ao interpretar a resposta do DingTalk: %w (%s)", err, snippet(raw))
	}
	if res.ErrCode != 0 {
		// 301000 é falha na validação da assinatura e 310000 é palavra-chave que não confere — ambos são erros de configuração,
		// e tentar de novo não resolve.
		return 0, Permanent(fmt.Errorf("DingTalk retornou erro %d: %s", res.ErrCode, res.ErrMsg))
	}
	return kept, nil
}

// dingTalkSignedURL acrescenta os parâmetros timestamp e sign ao webhook conforme a regra oficial de assinatura.
//
// Regra: string a assinar = timestamp + "\n" + secret, e a **chave do HMAC-SHA256 também é o secret**,
// com o resultado em base64 e depois codificado em URL. O timestamp é em milissegundos. Quando o secret é vazio, retorna a entrada
// sem alterações, para suportar robôs sem assinatura habilitada.
func dingTalkSignedURL(hook, secret string, now time.Time) (string, error) {
	if secret == "" {
		return hook, nil
	}
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "\n" + secret))
	sign := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	u, err := url.Parse(hook)
	if err != nil {
		// Não repassamos err: o texto de erro de url.Parse contém o endereço completo (incluindo o access_token).
		return "", fmt.Errorf("falha ao interpretar o endereço do Webhook: %s", redactRequestTarget(hook))
	}
	q := u.Query()
	q.Set("timestamp", ts)
	q.Set("sign", sign)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// validateHTTPURL valida que o endereço é utilizável e que o protocolo é suportado, e faz a checagem de rede interna para destinos com IP literal.
//
// Dois pontos de atenção:
//
//  1. **A mensagem de erro precisa ser ofuscada**. O url.Parse retorna um *url.Error cujo Error() traz o
//     **endereço original completo**, e nos endereços destes canais estão embutidas as credenciais (access_token do DingTalk,
//     key do WeCom, bot token do Telegram, hook id do Feishu). Antes, aqui havia um `return err` direto,
//     e o erro "endereço com formato inválido" levava a credencial embora, rumo à resposta 400 da interface de teste,
//     ao last_error gravado no banco a cada entrega, ao log do servidor e à interface de histórico de entregas.
//
//  2. **IP literal é julgado como rede interna na hora**, enquanto nomes de domínio ficam para a fase de conexão (blockInternalDial é o
//     ponto onde a proteção de fato vale, e também cobre DNS rebinding). Fazer uma vez aqui serve para dar o aviso já ao salvar a configuração,
//     em vez de esperar a primeira entrega falhar.
//
// Restringir o protocolo é defensivo: file:///gopher:// e similares fazem o http.Client ter
// comportamentos inesperados (embora já barrados pela checagem de scheme, não há motivo para abrir essa superfície).
func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("não foi possível interpretar o endereço (%s)", redactRequestTarget(raw))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("apenas http/https são suportados, recebido %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("nome do host ausente")
	}
	if ip := net.ParseIP(u.Hostname()); ip != nil && isBlockedDialIP(ip) && !allowLocalTargets() {
		return fmt.Errorf("entrega recusada para endereço local/link-local %s (se for realmente necessário entregar a um serviço local, defina %s=1)", ip, AllowLocalTargetsEnv)
	}
	return nil
}
