package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"
)

// webhookChannel é o adaptador de Webhook genérico: o usuário define a URL, o método, os cabeçalhos e o template JSON.
// Sua existência evita que este recurso precise de uma implementação para cada um de Slack / Mattermost / Discord / sistemas próprios —
// todas essas plataformas podem ser cobertas por um template configurável.
type webhookChannel struct{}

func (webhookChannel) Kind() string { return KindWebhook }

// O Webhook genérico não tem limite oficial; retornar 0 significa sem limitação de taxa por padrão, cabendo ao usuário definir conforme a capacidade do outro lado.
func (webhookChannel) DefaultRatePerMin() int { return 0 }

// Mascara url e headers: o próprio endereço de destino costuma conter token e os cabeçalhos personalizados geralmente carregam credenciais de autenticação,
// e ambos apareceriam no retorno da interface, então os dois precisam ser protegidos.
// O custo é que, ao editar, para mudar um dos cabeçalhos é preciso preencher de novo o conjunto inteiro (o valor mascarado é interpretado como "manter o valor original") —
// essa troca é deliberada: melhor preencher uma vez a mais do que ecoar a credencial no navegador.
func (webhookChannel) SecretKeys() []string { return []string{"url", "headers"} }

// O destino é a url. Ao alterar a url é preciso se manifestar novamente sobre headers — caso contrário o cabeçalho Authorization original
// seria enviado como está ao novo endereço, e este é justamente o caminho principal para contornar a máscara.
func (webhookChannel) DestinationKeys() []string { return []string{"url"} }

// webhookDefaultTemplate é o corpo de requisição de reserva quando nenhum template é informado: uma estrutura JSON direta,
// que cobre a grande maioria dos receptores próprios do tipo "recebe um JSON e grava no banco".
const webhookDefaultTemplate = `{
  "title": {{json .Title}},
  "batch": {{.Batch}},
  "count": {{.Count}},
  "items": [
{{- range $i, $it := .Items}}
{{- if $i}},{{end}}
    {
      "finding_id": {{$it.FindingID}},
      "name": {{json $it.Name}},
      "vulnclass": {{json $it.VulnClass}},
      "severity": {{json $it.Severity}},
      "summary": {{json $it.Summary}},
      "assets": {{json $it.Assets}},
      "detail_url": {{json $it.DetailURL}}
    }
{{- end}}
  ]
}`

// webhookTemplateData é o contexto exposto aos templates do usuário.
type webhookTemplateData struct {
	Title   string
	Batch   bool
	Count   int
	Items   []webhookItem
	HomeURL string
	// SentAt é o horário desta entrega (RFC3339), para o receptor registrar.
	SentAt string
}

type webhookItem struct {
	FindingID     int64
	Name          string
	VulnClass     string
	Severity      string
	SeverityLabel string
	Summary       string
	Assets        []string
	DetailURL     string
	FromStatus    string
	ToStatus      string
	// StatusLabel é a descrição legível da mudança de status, como "Pendente → Corrigido"; vazio quando não é mudança de status.
	StatusLabel string
}

func (webhookChannel) Validate(cfg map[string]any) error {
	raw := cfgString(cfg, "url")
	if raw == "" {
		return errors.New("URL de destino ausente")
	}
	if err := validateHTTPURL(raw); err != nil {
		return fmt.Errorf("URL de destino inválida: %w", err)
	}
	if m := strings.ToUpper(cfgString(cfg, "method")); m != "" && m != http.MethodGet && m != http.MethodPost && m != http.MethodPut && m != http.MethodPatch {
		return fmt.Errorf("método não suportado %s (disponíveis: GET/POST/PUT/PATCH)", m)
	}
	if tpl := cfgString(cfg, "body_template"); tpl != "" {
		if _, err := parseWebhookTemplate(tpl); err != nil {
			return fmt.Errorf("erro de sintaxe no template do corpo da requisição: %w", err)
		}
	}
	return nil
}

func (c webhookChannel) Send(ctx context.Context, cfg map[string]any, m Message) (int, error) {
	if err := c.Validate(cfg); err != nil {
		return 0, Permanent(err)
	}
	method := strings.ToUpper(cfgString(cfg, "method"))
	if method == "" {
		method = http.MethodPost
	}

	// GET não leva corpo: colocar o conteúdo na query vai além da capacidade do template e também não condiz com a semântica do GET,
	// então o GET só serve para receptores do tipo "disparar um gancho ao ser acionado".
	var payload any
	if method != http.MethodGet {
		body, err := renderWebhookBody(cfgString(cfg, "body_template"), m)
		if err != nil {
			return 0, Permanent(err)
		}
		// O que o template renderiza é um JSON em forma de string; aqui é convertido em json.RawMessage e enviado como está,
		// evitando um segundo escape que embrulharia a estrutura cuidadosamente construída pelo usuário dentro de uma string JSON.
		if !json.Valid([]byte(body)) {
			return 0, Permanent(errors.New("o resultado da renderização do template do corpo da requisição não é um JSON válido"))
		}
		payload = json.RawMessage(body)
	}

	headers := cfgMap(cfg, "headers")
	if ct := cfgString(cfg, "content_type"); ct != "" {
		// Permite sobrescrever, mas aplica depois dos headers, garantindo que a configuração explícita tenha prioridade.
		if headers == nil {
			headers = map[string]string{}
		}
		headers["Content-Type"] = ct
	}
	if _, err := doJSON(ctx, method, cfgString(cfg, "url"), headers, payload); err != nil {
		return 0, err
	}
	// O Webhook genérico não trunca o corpo (o receptor é um serviço do próprio usuário, e o tamanho é decidido por body_template),
	// portanto o lote inteiro conta como entregue.
	return len(m.Items), nil
}

// renderWebhookBody renderiza o corpo da requisição com o template do usuário (ou o template padrão).
func renderWebhookBody(tpl string, m Message) (string, error) {
	if strings.TrimSpace(tpl) == "" {
		tpl = webhookDefaultTemplate
	}
	t, err := parseWebhookTemplate(tpl)
	if err != nil {
		return "", fmt.Errorf("erro de sintaxe no template do corpo da requisição: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, newWebhookTemplateData(m)); err != nil {
		return "", fmt.Errorf("falha ao renderizar o template do corpo da requisição: %w", err)
	}
	return buf.String(), nil
}

// parseWebhookTemplate interpreta o template.
//
// missingkey=zero faz chaves ausentes de map renderizarem como valor zero em vez de erro — mas o contexto deste arquivo é uma struct,
// então o efeito principal é evitar que o range falhe quando .Items está vazio. O que realmente precisa ser evitado é .Items ser nil.
func parseWebhookTemplate(tpl string) (*template.Template, error) {
	return template.New("body").Funcs(webhookTemplateFuncs).Option("missingkey=zero").Parse(tpl)
}

// webhookTemplateFuncs são as funções auxiliares expostas ao template.
var webhookTemplateFuncs = template.FuncMap{
	// json serializa qualquer valor como JSON.
	//
	// Esta função não é um enfeite, mas uma necessidade: sem ela, o usuário só poderia escrever {{.Title}} para interpolar diretamente,
	// e basta o título da vulnerabilidade conter aspas ou quebra de linha para o corpo inteiro deixar de ser um JSON válido — o receptor
	// o rejeitaria, e a mensagem de erro apontaria para "falha ao interpretar o JSON", sem nenhuma pista de que há uma aspa no título.
	"json": func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(raw), nil
	},
	// jsons serve para embutir um fragmento JSON dentro do valor de uma string de outro JSON (aplicando uma camada de escape de string).
	"jsons": func(v any) (string, error) {
		raw, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		quoted, err := json.Marshal(string(raw))
		if err != nil {
			return "", err
		}
		// Remove as aspas externas: quem chama decide se quer ou não adicionar aspas.
		return string(quoted[1 : len(quoted)-1]), nil
	},
}

func newWebhookTemplateData(m Message) webhookTemplateData {
	d := webhookTemplateData{
		Title:   markdownTitle(m),
		Batch:   m.Batch,
		Count:   len(m.Items),
		HomeURL: m.HomeURL,
		SentAt:  time.Now().Format(time.RFC3339),
		Items:   make([]webhookItem, 0, len(m.Items)),
	}
	for _, it := range m.Items {
		wi := webhookItem{
			FindingID:     it.FindingID,
			Name:          it.Name,
			VulnClass:     it.VulnClass,
			Severity:      it.Severity,
			SeverityLabel: SeverityLabel(it.Severity),
			Summary:       it.Summary,
			Assets:        append([]string{}, it.Assets...),
			DetailURL:     it.DetailURL,
			FromStatus:    it.FromStatus,
			ToStatus:      it.ToStatus,
		}
		if it.IsStatusChange() {
			wi.StatusLabel = StatusLabel(it.FromStatus) + " → " + StatusLabel(it.ToStatus)
		}
		d.Items = append(d.Items, wi)
	}
	return d
}
