package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// telegramTextLimit é o limite do campo text do sendMessage do Telegram (em caracteres).
const telegramTextLimit = 4096

// telegramChannel implementa a Bot API do Telegram.
//
// Características da plataforma:
//   - Toda a autenticação está no path da URL (/bot<token>/sendMessage), sem necessidade de assinatura.
//   - Usa o modo de parse HTML em vez de MarkdownV2: o MarkdownV2 exige escapar `_*[]()~`>#+-=|{}.!`,
//     18 caracteres no total, e esquecer um faz a mensagem inteira ser rejeitada; o HTML só precisa escapar os três & < >.
//   - O erro de negócio também fica escondido no HTTP 200, e é identificado pelo campo ok.
type telegramChannel struct{}

func (telegramChannel) Kind() string { return KindTelegram }

// Telegram: cerca de 1 mensagem/segundo em chat individual e 20 mensagens/minuto em grupos. Adota-se um valor conservador.
func (telegramChannel) DefaultRatePerMin() int { return 20 }

// O Bot Token é a credencial completa; chat_id é só o destinatário e não é segredo (com ele, sem o Token, não é possível enviar mensagens).
func (telegramChannel) SecretKeys() []string { return []string{"bot_token"} }

// base_url determina para qual endpoint de API o Token é enviado (como um proxy reverso próprio); alterá-lo exige se manifestar novamente sobre o Token.
func (telegramChannel) DestinationKeys() []string { return []string{"base_url"} }

func (telegramChannel) Validate(cfg map[string]any) error {
	if cfgString(cfg, "bot_token") == "" {
		return errors.New("Bot Token ausente")
	}
	if cfgString(cfg, "chat_id") == "" {
		return errors.New("Chat ID ausente")
	}
	if base := cfgString(cfg, "base_url"); base != "" {
		if err := validateHTTPURL(base); err != nil {
			return fmt.Errorf("endereço da API inválido: %w", err)
		}
	}
	return nil
}

func (c telegramChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	endpoint, err := telegramEndpoint(cfg)
	if err != nil {
		return 0, Permanent(err)
	}
	text, kept := telegramHTML(m)
	payload := map[string]any{
		"chat_id":                  cfgString(cfg, "chat_id"),
		"text":                     text,
		"parse_mode":               "HTML",
		"disable_web_page_preview": false,
	}
	raw, err := doJSON(ctx, "POST", endpoint, nil, payload)
	if err != nil {
		return 0, err
	}
	var res struct {
		OK          bool   `json:"ok"`
		ErrorCode   int    `json:"error_code"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("falha ao interpretar a resposta do Telegram: %w (%s)", err, snippet(raw))
	}
	if res.OK {
		return kept, nil
	}
	// 429 é limitação de taxa, e tentar de novo após recuo funciona; os demais (400 parâmetro errado, 401 token errado, 403 bloqueado,
	// 404 chat inexistente) são problemas de configuração, e tentar de novo não resolve.
	if res.ErrorCode == 429 {
		return 0, fmt.Errorf("limitação de taxa do Telegram: %s", res.Description)
	}
	return 0, Permanent(fmt.Errorf("Telegram retornou erro %d: %s", res.ErrorCode, res.Description))
}

// telegramEndpoint monta o endereço do sendMessage. Quando base_url está vazio usa a API oficial,
// e quando preenchido é usado para um proxy reverso próprio da Bot API (necessidade comum em redes na China).
func telegramEndpoint(cfg map[string]any) (string, error) {
	base := cfgString(cfg, "base_url")
	if base == "" {
		base = "https://api.telegram.org"
	}
	base = strings.TrimSuffix(base, "/")
	token := cfgString(cfg, "bot_token")
	raw := base + "/bot" + token + "/sendMessage"
	u, err := url.Parse(raw)
	if err != nil {
		// Não repassamos err: o endereço contém o Bot Token, e nesta altura nem o addr deveria ser ecoado.
		return "", fmt.Errorf("falha ao montar o endereço da API (endereço da API: %s)", redactRequestTarget(base))
	}
	return u.String(), nil
}

// telegramHTML renderiza o corpo HTML e retorna o corpo e o número de itens efetivamente escritos (veja Channel.Send).
func telegramHTML(m Message) (string, int) {
	var b strings.Builder
	b.WriteString("<b>" + telegramEscape(markdownTitle(m)) + "</b>\n")
	if m.Batch {
		// O limite do Telegram é em **caracteres**, então o empacotamento também mede em caracteres (runeSize).
		footer := ""
		if m.HomeURL != "" {
			footer = fmt.Sprintf("\n\n<a href=\"%s\">Ver todos na plataforma</a>", telegramEscapeAttr(m.HomeURL))
		}
		kept := packItemCount(m.Items, telegramTextLimit, telegramReservedRunes, footer, runeSize, func(it Item, idx int) string {
			return telegramBatchLine(it, idx+1)
		})
		items := m.Items[:kept]
		b.Reset()
		b.WriteString("<b>" + telegramEscape(telegramBatchTitle(m, items, len(m.Items))) + "</b>")
		for i, it := range items {
			b.WriteString("\n" + telegramEscape(telegramBatchLine(it, i+1)))
		}
		b.WriteString(footer)
		return TruncateHTML(b.String(), telegramTextLimit), kept
	}
	if len(m.Items) == 0 {
		return b.String(), 0
	}
	it := m.Items[0]
	if it.IsStatusChange() {
		b.WriteString(fmt.Sprintf("\n<b>Mudança de status</b>: %s → %s",
			telegramEscape(StatusLabel(it.FromStatus)), telegramEscape(StatusLabel(it.ToStatus))))
	}
	if it.VulnClass != "" && it.VulnClass != it.Title() {
		b.WriteString("\n<b>Tipo</b>: " + telegramEscape(it.VulnClass))
	}
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		b.WriteString("\n<b>Ativos</b>: " + telegramEscape(a))
	}
	if s := OneLine(it.Summary, maxSummaryRunes); s != "" {
		b.WriteString("\n<b>Resumo</b>: " + telegramEscape(s))
	}
	if it.DetailURL != "" {
		b.WriteString(fmt.Sprintf("\n\n<a href=\"%s\">Ver detalhes</a>", telegramEscapeAttr(it.DetailURL)))
	}
	return TruncateHTML(b.String(), telegramTextLimit), 1
}

// telegramReservedRunes é o espaço reservado para o título da mensagem e para o possível aviso de truncamento (medido em caracteres).
const telegramReservedRunes = 160

// telegramBatchLine renderiza uma linha do consolidado (sem escape; o escape é feito de uma vez pelo chamador).
func telegramBatchLine(it Item, idx int) string {
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		return fmt.Sprintf("%d. %s · %s — %s", idx, SeverityLabel(it.Severity), it.Title(), a)
	}
	return fmt.Sprintf("%d. %s · %s", idx, SeverityLabel(it.Severity), it.Title())
}

// telegramBatchTitle renderiza a linha de título da mensagem consolidada. A contagem usada é a de itens **efetivamente contidos nesta mensagem**,
// e não o total do lote — caso contrário o leitor acharia que o número no cabeçalho é tudo.
func telegramBatchTitle(m Message, items []Item, total int) string {
	title := fmt.Sprintf("Resumo de vulnerabilidades · total de %d", total)
	if extra := total - len(items); extra > 0 {
		title += fmt.Sprintf(" (mostrando os primeiros %d; os outros %d seguem na próxima)", len(items), extra)
	}
	if m.WindowMinutes > 0 {
		title = fmt.Sprintf("Últimos %d minutos · %s", m.WindowMinutes, title)
	}
	return title
}

// telegramEscape escapa o conteúdo de texto HTML.
// O Telegram só reconhece estas três entidades; entidades já existentes como &amp; são escapadas uma segunda vez — este é justamente o
// comportamento desejado: queremos exibir os caracteres originais, e não deixar o usuário injetar HTML.
func telegramEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// telegramEscapeAttr escapa valores de atributo HTML. Além do escape de texto, é preciso tratar aspas —
// aspas na URL fecham o atributo href antes da hora e transformam o restante em ponto de injeção.
func telegramEscapeAttr(s string) string {
	s = telegramEscape(s)
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}
