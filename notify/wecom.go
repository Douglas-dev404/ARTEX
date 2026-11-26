package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// weComMarkdownLimit é o limite rígido do markdown content do robô de grupo do WeCom (em bytes, não em caracteres).
// É a restrição mais apertada entre os seis canais e a principal razão de TruncateBytes existir.
const weComMarkdownLimit = 4096

// weComChannel implementa o robô de grupo do WeCom (WeChat Work).
//
// Características da plataforma:
//   - Autentica unicamente pela key na URL e não suporta assinatura — portanto o próprio endereço do webhook é toda a credencial.
//   - O markdown content tem limite de 4096 **bytes**, e se exceder a mensagem inteira é rejeitada (não truncada). Em chinês, 3 bytes/caractere,
//     o que significa que só se podem escrever pouco mais de mil caracteres no corpo, e o truncamento precisa ser feito no cliente.
//   - Limite de 20 mensagens/minuto, também contido por limitação de taxa no cliente.
type weComChannel struct{}

func (weComChannel) Kind() string { return KindWeCom }

func (weComChannel) DefaultRatePerMin() int { return 20 }

// O WeCom tem uma única credencial, o Webhook (a key na URL), e não suporta assinatura —
// o endereço inteiro é toda a credencial, sem outros campos a mascarar.
func (weComChannel) SecretKeys() []string { return []string{"webhook"} }

// O WeCom tem um único campo, o Webhook, que é ao mesmo tempo destino e credencial, portanto não existe "credencial remanescente após alterar o endereço".
func (weComChannel) DestinationKeys() []string { return []string{"webhook"} }

func (weComChannel) Validate(cfg map[string]any) error {
	hook := cfgString(cfg, "webhook")
	if hook == "" {
		return errors.New("Endereço do Webhook ausente")
	}
	if err := validateHTTPURL(hook); err != nil {
		return fmt.Errorf("Webhook 地址无效: %w", err)
	}
	return nil
}

func (c weComChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	// Um lote consolidado pode ficar muito longo (50 itens × uma linha cada + prefixo), e 4096 bytes são facilmente excedidos.
	// O truncamento é feito aqui em vez de esperar o erro da plataforma: ser rejeitado significa perder o lote inteiro, enquanto truncar ao menos entrega os primeiros itens.
	content, kept := markdownBody(m, weComMarkdownLimit)
	payload := map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]any{"content": content},
	}
	raw, err := doJSON(ctx, "POST", cfgString(cfg, "webhook"), nil, payload)
	if err != nil {
		return 0, err
	}
	var res struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return 0, fmt.Errorf("falha ao interpretar a resposta do WeCom: %w (%s)", err, snippet(raw))
	}
	if res.ErrCode != 0 {
		// 45009 é chamada de interface acima do limite — a janela de limitação da plataforma é deslizante, e tentar de novo após o recuo funciona,
		// por isso é classificado explicitamente como passível de nova tentativa. Chegar até aqui indica que o rate_per_min do cliente está configurado de forma agressiva demais;
		// a nova tentativa é só uma rede de segurança, e a correção real é reduzir o limite de taxa deste canal.
		if res.ErrCode == 45009 {
			return 0, fmt.Errorf("limitação de taxa do WeCom %d: %s", res.ErrCode, res.ErrMsg)
		}
		// 93000 é webhook key inválida — falha permanente, tentar de novo não resolve.
		return 0, Permanent(fmt.Errorf("WeCom retornou erro %d: %s", res.ErrCode, res.ErrMsg))
	}
	return kept, nil
}
