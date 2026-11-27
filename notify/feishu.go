package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// feishuChannel implementa o robô personalizado do Feishu (incluindo Lark), usando cartões interativos.
//
// Características da plataforma:
//   - O algoritmo de assinatura é **diferente** do DingTalk e é muito fácil de errar; veja o comentário de feishuSign.
//   - Assim como no DingTalk, o erro de negócio vem no body de um HTTP 200 (code != 0).
//   - O header do cartão suporta modelos de cor; mapeia-se a severidade para a cor, para que se veja de relance a gravidade na lista de mensagens.
type feishuChannel struct{}

func (feishuChannel) Kind() string { return KindFeishu }

// O robô personalizado do Feishu permite cerca de 5 vezes/segundo, o que equivale a 100 vezes/minuto.
func (feishuChannel) DefaultRatePerMin() int { return 100 }

// O trecho final do endereço do Webhook é o identificador único do robô e é uma credencial.
func (feishuChannel) SecretKeys() []string { return []string{"webhook", "secret"} }

// Pelo mesmo motivo: alterar o endereço do Webhook exige se manifestar novamente sobre a chave de assinatura para o novo endereço.
func (feishuChannel) DestinationKeys() []string { return []string{"webhook"} }

func (feishuChannel) Validate(cfg map[string]any) error {
	hook := cfgString(cfg, "webhook")
	if hook == "" {
		return errors.New("Endereço do Webhook ausente")
	}
	if err := validateHTTPURL(hook); err != nil {
		return fmt.Errorf("endereço de Webhook inválido: %w", err)
	}
	return nil
}

func (c feishuChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	card, kept := feishuCard(m)
	payload := map[string]any{
		"msg_type": "interactive",
		"card":     card,
	}
	// Os parâmetros de assinatura ficam no mesmo nível da mensagem e só aparecem quando o secret está configurado.
	if secret := cfgString(cfg, "secret"); secret != "" {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		payload["timestamp"] = ts
		payload["sign"] = feishuSign(ts, secret)
	}
	raw, err := doJSON(ctx, "POST", cfgString(cfg, "webhook"), nil, payload)
	if err != nil {
		return 0, err
	}
	var res struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		// Algumas versões do hook do Feishu usam este conjunto de nomes de campos; também são tratados por compatibilidade.
		StatusCode    int    `json:"StatusCode"`
		StatusMessage string `json:"StatusMessage"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("falha ao interpretar a resposta do Feishu: %w (%s)", err, snippet(raw))
	}
	if res.Code != 0 {
		return 0, Permanent(fmt.Errorf("Feishu retornou erro %d: %s", res.Code, res.Msg))
	}
	if res.StatusCode != 0 {
		return 0, Permanent(fmt.Errorf("Feishu retornou erro %d: %s", res.StatusCode, res.StatusMessage))
	}
	return kept, nil
}

// feishuSign calcula a assinatura conforme a regra oficial do Feishu.
//
// Este é um ponto em que é especialmente fácil cair: o exemplo oficial é
//
//	hmac.new(string_to_sign.encode(), digestmod=sha256)
//
// ou seja, **key = timestamp + "\n" + secret, com message vazio**, e não o intuitivo
// "key=secret, message=stringToSign" — esse é justamente o algoritmo do DingTalk. Os dois algoritmos são exatamente o inverso um do outro;
// implementar seguindo o de outro canal faz a validação de assinatura falhar com certeza (erro 19021).
func feishuSign(timestamp, secret string) string {
	stringToSign := timestamp + "\n" + secret
	mac := hmac.New(sha256.New, []byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// feishuSeverityTemplate mapeia a severidade da vulnerabilidade para o modelo de cor do header do cartão.
// Severidade desconhecida usa grey — não usa blue, para não confundir com low.
func feishuSeverityTemplate(severity string) string {
	switch severity {
	case "critical":
		return "red"
	case "high":
		return "orange"
	case "medium":
		return "yellow"
	case "low":
		return "blue"
	default:
		return "grey"
	}
}

// feishuMaxCardBytes é um limite conservador para o conteúdo do cartão. O Feishu limita o tamanho do cartão e, se excedido, a mensagem inteira é rejeitada;
// adota-se um valor claramente abaixo do limite oficial, já contando também a sobrecarga do empacotamento JSON.
const feishuMaxCardBytes = 24000

// feishuCard monta o cartão interativo e retorna o cartão e o **número de itens efetivamente escritos**.
// kept tem o mesmo uso que em markdownBody: só os itens que de fato entraram no cartão devem ser marcados como entregues.
func feishuCard(m Message) (map[string]any, int) {
	elements := []any{}
	kept := 0
	if m.Batch {
		// Primeiro empacota por item inteiro e só depois monta o cabeçalho: o cabeçalho deve dizer "os outros N itens continuam na próxima mensagem",
		// e N precisa vir da contagem realmente acomodada.
		kept = packItemCount(m.Items, feishuMaxCardBytes, markdownReservedBytes, "", byteSize, func(it Item, idx int) string {
			return feishuBatchLine(it, idx+1)
		})
		items := m.Items[:kept]
		elements = append(elements, feishuMarkdownDiv(markdownBatchIntro(m, items, len(m.Items))))
		for i, it := range items {
			elements = append(elements, feishuMarkdownDiv(feishuBatchLine(it, i+1)))
		}
		if m.HomeURL != "" {
			elements = append(elements, feishuButton("Ver todos na plataforma", m.HomeURL))
		}
	} else if len(m.Items) > 0 {
		kept = 1
		it := m.Items[0]
		elements = append(elements, feishuMarkdownDiv(feishuItemLines(it)))
		if it.DetailURL != "" {
			elements = append(elements, feishuButton("Ver detalhes", it.DetailURL))
		}
	}

	card := map[string]any{
		"config":   map[string]any{"wide_screen_mode": true},
		"header":   map[string]any{"title": map[string]any{"tag": "plain_text", "content": markdownTitle(m)}},
		"elements": elements,
	}
	if len(m.Items) > 0 {
		card["header"].(map[string]any)["template"] = feishuSeverityTemplate(m.Items[0].Severity)
	}
	return card, kept
}

func feishuMarkdownDiv(content string) map[string]any {
	return map[string]any{"tag": "div", "text": map[string]any{"tag": "lark_md", "content": content}}
}

func feishuButton(label, url string) map[string]any {
	return map[string]any{
		"tag": "action",
		"actions": []any{map[string]any{
			"tag":  "button",
			"text": map[string]any{"tag": "lark_md", "content": label},
			"url":  url,
			"type": "primary",
		}},
	}
}

// feishuItemLines renderiza o corpo lark_md de uma única vulnerabilidade.
//
// lark_md é um formato de texto da mesma família do markdown e também interpreta links e ênfase, portanto os campos vindos de fora
// sempre passam por markdownText (uma linha só + escape) — caso contrário um único título de vulnerabilidade poderia virar
// um link externo clicável no Feishu.
func feishuItemLines(it Item) string {
	out := fmt.Sprintf("**%s · %s**", SeverityLabel(it.Severity), markdownText(it.Title(), 0))
	if it.IsStatusChange() {
		out += fmt.Sprintf("\n**Mudança de status**: %s → %s",
			markdownText(StatusLabel(it.FromStatus), 0), markdownText(StatusLabel(it.ToStatus), 0))
	}
	if it.VulnClass != "" && it.VulnClass != it.Title() {
		out += fmt.Sprintf("\n**Tipo**: %s", markdownText(it.VulnClass, 0))
	}
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		out += fmt.Sprintf("\n**Ativos**: %s", markdownText(a, 0))
	}
	if it.Summary != "" {
		if s := markdownText(it.Summary, maxSummaryRunes); s != "" {
			out += fmt.Sprintf("\n**Resumo**: %s", s)
		}
	}
	return out
}

// feishuBatchLine renderiza uma linha do cartão consolidado.
func feishuBatchLine(it Item, index int) string {
	line := fmt.Sprintf("**%d. %s · %s**", index, SeverityLabel(it.Severity), markdownText(it.Title(), 0))
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		line += " — " + markdownText(a, 0)
	}
	return line
}
