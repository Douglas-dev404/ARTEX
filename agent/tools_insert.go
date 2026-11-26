package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// assetInterceptCandidates extrai de um item de ativo a inserir as strings candidatas de domínio/IP/URL, usadas na correspondência de interceptação de ativos.
// O host da URL é desmembrado e classificado, para que ativos de serviço/endpoint "com apenas URL" também possam ser atingidos por regras de domínio/IP.
func assetInterceptCandidates(item assetInputItem) (domains, ips, urls []string) {
	add := func(dst *[]string, s string) {
		if s = strings.TrimSpace(s); s != "" {
			*dst = append(*dst, s)
		}
	}
	add(&domains, item.Domain)
	for _, d := range item.BoundDomains {
		add(&domains, d)
	}
	add(&ips, item.IP)
	add(&ips, item.ServiceIP)
	add(&urls, item.URL)
	if item.URL != "" {
		if u, err := url.Parse(item.URL); err == nil {
			if h := u.Hostname(); h != "" {
				if net.ParseIP(h) != nil {
					add(&ips, h)
				} else {
					add(&domains, h)
				}
			}
		}
	}
	return domains, ips, urls
}

// assetInputLabel retorna um identificador curto de um ativo a inserir, usado na mensagem de explicação da interceptação.
func assetInputLabel(item assetInputItem) string {
	typ := strings.TrimSpace(item.Type)
	var target string
	switch {
	case strings.TrimSpace(item.Domain) != "":
		target = strings.TrimSpace(item.Domain)
	case strings.TrimSpace(item.URL) != "":
		target = strings.TrimSpace(item.URL)
	case strings.TrimSpace(item.IP) != "":
		target = strings.TrimSpace(item.IP)
	case strings.TrimSpace(item.ServiceIP) != "":
		target = strings.TrimSpace(item.ServiceIP)
	default:
		target = "(desconhecido)"
	}
	if typ != "" {
		return fmt.Sprintf("[%s] %s", typ, target)
	}
	return target
}

// =====================================================================
// Unified asset insertion tools
// =====================================================================

// SetAssetStore wires the asset store and company store onto this ToolSet
// so the insert_assets, add_company_scope, and list_assets tools are active.
func (t *ToolSet) SetAssetStore(as *db.AssetStore, cs *db.CompanyStore) {
	t.as = as
	t.cs = cs
}

// assetInputItem is one element of the insert_assets "assets" array.
type assetInputItem struct {
	Type string `json:"type"` // root_domain|ip|subdomain|app|service|endpoint

	// ---- root_domain / subdomain ----
	Domain      string   `json:"domain"`
	ICP         string   `json:"icp"`
	RecordType  string   `json:"record_type"`
	RecordValue []string `json:"record_value"`

	// ---- ip ----
	IP           string           `json:"ip"`
	BoundDomains []string         `json:"bound_domains"`
	OpenPorts    []db.PortService `json:"open_ports"`

	// ---- app ----
	AppName     string `json:"app_name"`
	BundleID    string `json:"bundle_id"`
	Category    string `json:"category"`
	Description string `json:"description"`
	AppICP      string `json:"app_icp"`
	CompanyID   *int64 `json:"company_id"` // explicit company link (app only; others auto-attribute via scope)

	// ---- service (http) ----
	URL           string           `json:"url"`
	Technologies  []string         `json:"technologies"`
	StatusCode    *int             `json:"status_code"`
	ContentLength *int64           `json:"content_length"`
	PageTitle     string           `json:"page_title"`
	FaviconMMH3   string           `json:"favicon_mmh3"`
	Auth          []map[string]any `json:"auth"`
	ServiceName   string           `json:"service_name"`
	ServiceIP     string           `json:"service_ip"` // optional enrichment IP

	// ---- service (other) ----
	Port  int    `json:"port"`
	Proto string `json:"proto"`

	// ---- endpoint ----
	Method string           `json:"method"`
	Params []map[string]any `json:"params"`
}

// insertAssets is the unified insert_assets agent tool.
func (t *ToolSet) insertAssets() actool.CoreTool {
	return writeTool(
		"insert_assets",
		"Registra em lote ativos recém-descobertos; uma chamada pode misturar vários tipos (type veja o enum).\n"+
			"Campos obrigatórios por tipo: root_domain→domain; ip→ip (deve ser IPv4/IPv6, não nome de host); subdomain→domain; app→app_name; service(HTTP)→url; service(não HTTP)→service_name+port (ip/domain pelo menos um); endpoint→url+method. O significado dos demais campos está nas respectivas descrições.\n"+
			"auth/technologies/params são mesclados por acréscimo (append), sem sobrescrever o valor original.\n"+
			"Retorno: {results:[{index,id,type}], errors:[{index,error}]}",
		obj(map[string]any{
			// task_id não é exposto ao modelo: a qual task o worker pertence é atribuído de forma autoritativa pelo programa via SetTaskID (ver handler).
			"assets": map[string]any{
				"type":        "array",
				"description": "Array de ativos, cada elemento corresponde a um registro de ativo",
				"items": obj(map[string]any{
					"type": map[string]any{
						"type":        "string",
						"enum":        []string{"root_domain", "ip", "subdomain", "app", "service", "endpoint"},
						"description": "Tipo do ativo",
					},
					// root_domain / subdomain
					"domain":      str("Domínio raiz ou subdomínio (obrigatório para root_domain/subdomain)"),
					"icp":         str("Número de registro ICP (opcional)"),
					"record_type": str("Tipo de resolução DNS: A/AAAA/CNAME/MX etc. (opcional para subdomain)"),
					"record_value": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Lista de valores de resolução DNS (opcional para subdomain, como [\"1.2.3.4\",\"2.3.4.5\"])",
					},
					// ip
					"ip": str("Endereço IP, deve ser um endereço IPv4/IPv6, não pode ser nome de host (para nome de host use o campo domain de type=subdomain); obrigatório para o tipo ip; pode ser preenchido nos tipos service/endpoint, para associar o IP"),
					"bound_domains": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Lista de domínios vinculados a este IP (opcional para o tipo ip)",
					},
					"open_ports": map[string]any{
						"type":        "array",
						"description": "Lista de portas abertas (opcional para o tipo ip)",
						"items": obj(map[string]any{
							"port":    intp("Número da porta"),
							"service": str("Nome do serviço, como http/ssh/mysql etc. (opcional)"),
						}, "port"),
					},
					// app
					"app_name":    str("Nome do aplicativo (obrigatório para o tipo app)"),
					"bundle_id":   str("Bundle ID (opcional para o tipo app)"),
					"category":    str("Categoria do aplicativo (opcional)"),
					"description": str("Descrição do aplicativo (opcional)"),
					"app_icp":     str("Registro ICP do aplicativo (opcional)"),
					"company_id":  intp("id da empresa proprietária (opcional para o tipo app; o app não pode ser atribuído automaticamente por scope, é preciso indicar explicitamente. O id é retornado por add_company_scope)"),
					// service (http)
					"url":         str("URL completa, incluindo protocolo e porta (obrigatório para serviço HTTP; service_type é definido automaticamente como http)"),
					"status_code": intp("Código de status da resposta HTTP, como 200/301/403/404 (opcional)"),
					"content_length": map[string]any{
						"type":        "integer",
						"description": "Tamanho em bytes do corpo da resposta HTTP (opcional)",
					},
					"page_title":   str("Conteúdo do <title> da página (opcional)"),
					"favicon_mmh3": str("Hash MMH3 do favicon (opcional)"),
					"technologies": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Lista de fingerprints/stack tecnológica, como [\"Nginx\",\"Vue\",\"Bootstrap\"] (opcional)",
					},
					"auth": map[string]any{
						"type":        "array",
						"description": "Lista de informações de autenticação descobertas, cada uma com campos como type/username/password (opcional, acrescenta sem sobrescrever)",
						"items":       map[string]any{"type": "object"},
					},
					// service (other, não HTTP)
					"service_name": str("Nome do serviço, como ssh/mysql/redis (obrigatório quando service é não HTTP)"),
					"port":         intp("Número da porta (obrigatório quando service é não HTTP)"),
					// endpoint
					"method": str("Método HTTP: GET/POST/PUT/PATCH/DELETE etc. (obrigatório para endpoint)"),
					"params": map[string]any{
						"type":        "array",
						"description": "Lista de parâmetros da requisição, cada um com location(query/body/header/path)/name/value/type (opcional, acrescenta sem sobrescrever)",
						"items":       map[string]any{"type": "object"},
					},
				}, "type"),
			},
		}, "assets"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("insert_assets não está habilitado: AssetStore não inicializado"), nil
			}
			var a struct {
				Assets []assetInputItem `json:"assets"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf("invalid input: " + err.Error()), nil
			}
			// task_id é atribuído de forma autoritativa pelo programa (worker: SetTaskID), não aceita valor passado pelo modelo — evita que o modelo omita/passe errado
			// e deixe o ativo sem tarefa ou na tarefa errada. Para chamadores sem contexto de tarefa (auto/pentest/chat), t.taskID=0.
			taskID := t.taskID

			type result struct {
				Index int    `json:"index"`
				ID    int64  `json:"id"`
				Type  string `json:"type"`
			}
			type errEntry struct {
				Index int    `json:"index"`
				Error string `json:"error"`
			}

			var results []result
			var errs []errEntry

			// As regras do portão de ativos são carregadas de uma vez; se a leitura falhar, pula o julgamento (não bloqueia a inserção).
			// Regras de interceptação = global ∪ block de nível de tarefa; regras de permissão = allow de nível de tarefa.
			blockRules, _ := t.as.ListAssetInterceptRules()
			var allowRules []db.AssetInterceptRule
			if t.taskID > 0 {
				if tb, ta, err := t.as.TaskInterceptRulesSplit(t.taskID); err == nil {
					blockRules = append(blockRules, tb...)
					allowRules = ta
				}
			}

			for i, item := range a.Assets {
				// Portão de ativos: primeiro intercepta e depois permite; ativos recusados têm a inserção proibida (pula o Upsert e os efeitos colaterais seguintes).
				domains, ips, urls := assetInterceptCandidates(item)
				if d := db.EvaluateAssetGate(blockRules, allowRules, domains, ips, urls); !d.Allowed {
					errs = append(errs, errEntry{
						Index: i,
						Error: fmt.Sprintf("ativo %s %s, inserção proibida", assetInputLabel(item), d.Reason),
					})
					continue
				}

				typ := strings.TrimSpace(item.Type)
				var id int64
				var err error

				switch typ {
				case "root_domain":
					id, err = t.as.UpsertRootDomain(db.UpsertRootDomainReq{
						Domain: item.Domain,
						ICP:    item.ICP,
						TaskID: taskID,
					})

				case "ip":
					id, err = t.as.UpsertIP(db.UpsertIPReq{
						IP:           item.IP,
						BoundDomains: item.BoundDomains,
						OpenPorts:    item.OpenPorts,
						TaskID:       taskID,
					})

				case "subdomain":
					id, err = t.as.UpsertSubdomain(db.UpsertSubdomainReq{
						Domain:      item.Domain,
						RecordType:  item.RecordType,
						RecordValue: item.RecordValue,
						ICP:         item.ICP,
						TaskID:      taskID,
					})

				case "app":
					id, err = t.as.UpsertApp(db.UpsertAppReq{
						Name:        item.AppName,
						BundleID:    item.BundleID,
						Category:    item.Category,
						Description: item.Description,
						ICP:         item.AppICP,
						CompanyID:   item.CompanyID,
						TaskID:      taskID,
					})

				case "service":
					// distinguish HTTP vs other by presence of url
					if item.URL != "" {
						// agent may send "ip" or "service_ip" for the enrichment IP; accept both
						svcIP := item.ServiceIP
						if svcIP == "" {
							svcIP = item.IP
						}
						id, err = t.as.UpsertHTTPService(db.UpsertHTTPServiceReq{
							URL:           item.URL,
							Technologies:  item.Technologies,
							StatusCode:    item.StatusCode,
							ContentLength: item.ContentLength,
							PageTitle:     item.PageTitle,
							FaviconMMH3:   item.FaviconMMH3,
							Auth:          item.Auth,
							IP:            svcIP,
							TaskID:        taskID,
						})
					} else {
						id, err = t.as.UpsertOtherService(db.UpsertOtherServiceReq{
							Domain:      item.Domain,
							IP:          item.IP,
							Port:        item.Port,
							ServiceName: item.ServiceName,
							Auth:        item.Auth,
							TaskID:      taskID,
						})
					}

				case "endpoint":
					id, err = t.as.UpsertEndpoint(db.UpsertEndpointReq{
						URL:    item.URL,
						Method: item.Method,
						Params: item.Params,
						IP:     item.ServiceIP,
						TaskID: taskID,
					})

				default:
					errs = append(errs, errEntry{Index: i, Error: "unknown type: " + typ})
					continue
				}

				if err != nil {
					errs = append(errs, errEntry{Index: i, Error: err.Error()})
					continue
				}
				results = append(results, result{Index: i, ID: id, Type: typ})
				t.writes.Assets++
				t.anchorOwner(id)
				if taskID > 0 {
					var sourceNodeID *int64
					if t.ownerNode > 0 {
						nodeID := t.ownerNode
						sourceNodeID = &nodeID
					}
					summary := "Registrado pelo Agent via insert_assets"
					if t.ownerNode > 0 {
						summary = fmt.Sprintf("Registrado pela intenção #%d do Worker via insert_assets", t.ownerNode)
					}
					_ = t.as.SetTaskAssetSource(taskID, id, "agent", summary, sourceNodeID)
				}
				// Entrada automática no escopo de teste (source='auto'): só para este item inserido explicitamente no topo pelo worker, adicionando
				// um escopo conservador conforme seu tipo; ativos derivados por side-effect não passam por aqui, então o escopo não se amplia às cegas. Com taskID=0 não faz nada.
				// Independe da chave de cobertura: task_scope é a fronteira de escopo da tarefa (base de filtro de list/consultas),
				// a chave de cobertura só decide se ele é usado como denominador no cálculo das métricas, não se o próprio escopo é acumulado.
				{
					svcIP := item.ServiceIP
					if svcIP == "" {
						svcIP = item.IP
					}
					_ = t.as.AddAutoScope(taskID, typ, item.Domain, item.URL, svcIP)
				}
			}

			return jsonResult(map[string]any{
				"results": results,
				"errors":  errs,
			})
		},
	)
}

// addCompanyScope writes to company_scope table and triggers asset attribution.
func (t *ToolSet) addCompanyScope() actool.CoreTool {
	return writeTool(
		"add_company_scope",
		"Inclui domínio/IP/CIDR/registro ICP/palavra-chave de empresa no [escopo de ativos] de uma empresa — domínios, redes e ICP reivindicam automaticamente os ativos atingidos, e a palavra-chave só é fornecida ao Agent como dica de escopo.\n"+
			"O nome da empresa é único: se company não existe, é criada; se já existe, é reutilizada (apenas mescla o escopo).\n"+
			"scope uma entrada por linha, o sistema identifica automaticamente: domínio raiz / URL / IP único / faixa CIDR / registro ICP / palavra-chave de empresa.\n"+
			"É obrigatório dar reason explicando o fundamento da atribuição (whois/certificado/ASN etc.).\n"+
			"Salvaguardas: rejeita TLD puro e faixas de rede amplas demais (prefixo IPv4 deve ser /16-/32, prefixo IPv6 /32-/128); linhas inválidas são ignoradas e retornadas em errors.",
		obj(map[string]any{
			"company": str("Nome da empresa (criada se não existir, reutilizada se existir; nome único)"),
			"scope":   str("Escopo de ativos, uma entrada por linha: domínio / URL / IP / CIDR / registro ICP / palavra-chave de empresa"),
			"reason":  str("Fundamento da atribuição (evidência/origem), preenchimento obrigatório"),
			"logo":    str("URL do ícone da empresa (opcional; só vale ao criar nova empresa)"),
		}, "company", "scope"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.cs == nil {
				return actool.Errorf("add_company_scope não está habilitado: CompanyStore não inicializado"), nil
			}
			var a struct {
				Company string `json:"company"`
				Scope   string `json:"scope"`
				Reason  string `json:"reason"`
				Logo    string `json:"logo"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if strings.TrimSpace(a.Company) == "" {
				return actool.Errorf("company não pode ser vazio"), nil
			}
			companyID, _, err := t.cs.UpsertCompany(a.Company, a.Logo)
			if err != nil {
				return actool.Errorf("falha ao criar/obter a empresa: " + err.Error()), nil
			}
			lines := splitLines(a.Scope)
			added, skipped, invalid, errMsgs := t.cs.AddScope(companyID, lines, a.Reason)
			out := map[string]any{
				"company_id": companyID,
				"added":      added,
				"skipped":    skipped,
				"invalid":    invalid,
			}
			if len(errMsgs) > 0 {
				out["errors"] = errMsgs
			}
			return jsonResult(out)
		},
	)
}

// addTaskScope lets the plan agent add test scope to THE CURRENT TASK — the coverage
// denominator and the task's authorization edge. Worker discoveries are auto-scoped
// (precise host) by insertAssets; this tool is for DELIBERATELY WIDENING: pull a whole
// root domain or whole company into scope, or add a specific subdomain / ip.
func (t *ToolSet) addTaskScope() actool.CoreTool {
	return writeTool(
		"add_task_scope",
		"Inclui o escopo de teste na [tarefa atual] — esta é a fronteira de autorização da tarefa e também o denominador da cobertura de teste dos ativos.\n"+
			"kind suporta: company (todos os ativos da empresa inteira) / root_domain (o domínio raiz inteiro, com todos os subdomínios) / subdomain (um único subdomínio exato) / ip / cidr / icp / keyword.\n"+
			"Nota: os hosts que o worker vai encontrando um a um são adicionados ao escopo pelo sistema [automaticamente] (subdomínio exato); esta ferramenta serve para [ampliar ativamente] — incluir o domínio raiz inteiro/a empresa inteira, ou complementar um subdomínio/IP específico.\n"+
			"value: para company passe o nome ou id da empresa (a empresa deve já existir); para root_domain/subdomain passe o domínio; para ip/cidr passe o IP ou faixa de rede; para icp/keyword passe o número de registro ou a palavra-chave da empresa.\n"+
			"É obrigatório dar reason explicando o fundamento (auditável). Para várias entradas, use o array entries.",
		obj(map[string]any{
			"entries": map[string]any{"type": "array", "description": "Lote: [{kind, value}]. kind∈company/root_domain/subdomain/ip/cidr/icp/keyword.", "items": map[string]any{"type": "object"}},
			"kind":    str("[Único] company / root_domain / subdomain / ip / cidr / icp / keyword"),
			"value":   str("[Único] nome ou id da empresa / domínio / IP / CIDR / ICP / palavra-chave"),
			"reason":  str("Fundamento da inclusão (para auditoria), preenchimento obrigatório"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("add_task_scope não está habilitado: AssetStore não inicializado"), nil
			}
			if t.taskID <= 0 {
				return actool.Errorf("add_task_scope requer contexto da tarefa (atualmente sem task)"), nil
			}
			type scopeEntry struct {
				Kind  string `json:"kind"`
				Value string `json:"value"`
			}
			var a struct {
				Entries    []scopeEntry `json:"entries"`
				scopeEntry              // modo único
				Reason     string       `json:"reason"`
			}
			_ = json.Unmarshal(in, &a)
			items := a.Entries
			if len(items) == 0 {
				items = []scopeEntry{a.scopeEntry}
			}
			var added []map[string]any
			errs := map[string]string{}
			for i, e := range items {
				ts, err := t.as.AddAgentScope(t.taskID, strings.TrimSpace(e.Kind), e.Value, a.Reason, "agent")
				if err != nil {
					errs[strconv.Itoa(i)] = err.Error()
					continue
				}
				added = append(added, map[string]any{"kind": ts.Kind, "domain": ts.Domain, "net": ts.Net, "value": ts.Value, "company_id": ts.CompanyID})
			}
			out := map[string]any{"added": added}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		},
	)
}

// listUntestedAssets lets the plan agent pull the current + directly inherited
// scope's not-yet-tested assets on demand (filter by type, paginated).
func (t *ToolSet) listUntestedAssets() actool.CoreTool {
	return readTool(
		"list_untested_assets",
		"Consulta os ativos dentro do escopo da [tarefa atual e das tarefas diretamente associadas] que ainda não foram cobertos por âncoras de fatos (escopo associado é somente leitura; serve para você julgar se vale testar mais, sem decidir por você).\n"+
			"Filtro opcional por tipo de ativo: root_domain/subdomain/service/app/endpoint/ip.\n"+
			"Paginação: page começa em 1, page_size padrão 10. Retorna {assets:[{id,type,label}], total, page, page_size}. Disponível apenas no contexto de tarefa.",
		obj(map[string]any{
			"type":      str("Filtro de tipo de ativo (opcional): root_domain/subdomain/service/app/endpoint/ip"),
			"page":      intp("Número da página, começando em 1 (padrão 1)"),
			"page_size": intp("Quantidade por página (padrão 10)"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("list_untested_assets não está habilitado: AssetStore não inicializado"), nil
			}
			if t.taskID <= 0 || t.ts == nil {
				return actool.Errorf("list_untested_assets requer contexto da tarefa"), nil
			}
			var a struct {
				Type     string `json:"type"`
				Page     int    `json:"page"`
				PageSize int    `json:"page_size"`
			}
			_ = json.Unmarshal(in, &a)
			if a.Page <= 0 {
				a.Page = 1
			}
			if a.PageSize <= 0 {
				a.PageSize = 10
			}
			offset := (a.Page - 1) * a.PageSize
			assets, total, err := t.as.ListUntestedAssetsWithSources(t.taskID, strings.TrimSpace(a.Type), a.PageSize, offset)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{
				"assets": assets, "total": total, "page": a.Page, "page_size": a.PageSize,
			})
		},
	)
}

// listAssets lets an agent query the asset table.
func (t *ToolSet) listAssets() actool.CoreTool {
	return readTool(
		"list_assets",
		"Consulta o banco de ativos: busca por expressão DSL, ou obtém direto por id/ids; suporta paginação. Retorna apenas ativos dentro do escopo de teste da [tarefa atual e das tarefas diretamente associadas].\n"+
			"DSL: field=value aproximado (ILIKE) | field==value exato | field!=value exclui | campos numéricos suportam > >= < <= | palavra solta = texto completo aproximado; combinação AND/OR (AND tem maior precedência), com parênteses para agrupar. O tipo de ativo usa o parâmetro type independente, não se escreve no DSL.\n"+
			"Quando id/ids não são passados, dsl deve ser não vazio (não se permite consulta total sem condições).\n"+
			"Campos disponíveis: domain (domínio raiz/sub/de serviço), root_domain, ip, url, page_title, icp, service_name, app_name, method (como GET/POST), service_type (http|other), record_type (como A/CNAME), technology (array, = aproximado == exato), port/status_code/company_id (inteiros).\n"+
			"Exemplo: status_code>=400 AND technology=shiro ; (port==80 OR port==443) AND technology=nginx",
		obj(map[string]any{
			"dsl":    str(`Expressão de consulta DSL (sintaxe/campos: veja a descrição da ferramenta). Obrigatória não vazia quando id/ids não são passados.`),
			"type":   str("Filtro de tipo de ativo: root_domain|ip|subdomain|app|service|endpoint (campo independente, pode ser combinado com dsl; type sozinho não basta para consultar, ainda é preciso dsl)"),
			"id":     intp("Obter direto por um único id de ativo (opcional, mutuamente exclusivo com dsl/type)"),
			"ids":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Obter direto por vários ids de ativo (opcional, mutuamente exclusivo com dsl/type)"},
			"limit":  intp("Limite de retorno, padrão 10 (opcional)"),
			"offset": intp("Deslocamento de paginação, padrão 0 (opcional)"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("list_assets não está habilitado: AssetStore não inicializado"), nil
			}
			var a struct {
				DSL    string  `json:"dsl"`
				Type   string  `json:"type"`
				ID     int64   `json:"id"`
				IDs    []int64 `json:"ids"`
				Limit  int     `json:"limit"`
				Offset int     `json:"offset"`
			}
			_ = json.Unmarshal(in, &a)
			if a.Limit <= 0 {
				a.Limit = 10
			}

			var assets []*db.Asset
			var err error
			switch {
			case a.ID > 0:
				assets, err = t.as.GetByIDsInScope(t.taskID, []int64{a.ID})
			case len(a.IDs) > 0:
				assets, err = t.as.GetByIDsInScope(t.taskID, a.IDs)
			case a.DSL != "":
				assets, err = t.as.QueryDSLInScope(a.DSL, a.Type, t.taskID, a.Limit, a.Offset)
			default:
				return actool.Errorf("Quando id/ids não são passados, dsl não pode ser vazio: não se permite consultar todos os ativos sem condições, forneça condições de consulta"), nil
			}
			if err != nil {
				return actool.Errorf("Erro de DSL: " + err.Error()), nil
			}
			return jsonResult(map[string]any{
				"count":  len(assets),
				"assets": assets,
			})
		},
	)
}

// listCompanies lets an agent enumerate companies (empresas) with their scope + asset count.
func (t *ToolSet) listCompanies() actool.CoreTool {
	return readTool(
		"list_companies",
		"Lista as [empresas] do banco de ativos, com seu escopo de ativos (scope) e a quantidade de ativos atribuídos. Usada para ver quais empresas existem e "+
			"obter company_id (usado em insert_assets para associar app e em list_assets para filtrar por company_id). "+
			"search opcional filtra por nome da empresa de forma aproximada (sem distinção de maiúsculas/minúsculas); vazio retorna todas.",
		obj(map[string]any{
			"search": str("Filtra aproximadamente por nome da empresa (opcional, sem distinção de maiúsculas/minúsculas); vazio retorna todas"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.cs == nil {
				return actool.Errorf("list_companies não está habilitado: CompanyStore não inicializado"), nil
			}
			var a struct {
				Search string `json:"search"`
			}
			_ = json.Unmarshal(in, &a)
			cos, err := t.cs.ListCompanies()
			if err != nil {
				return actool.Errorf("falha ao consultar empresas: " + err.Error()), nil
			}
			q := strings.ToLower(strings.TrimSpace(a.Search))
			type companyOut struct {
				ID         int64    `json:"id"`
				Name       string   `json:"name"`
				AssetCount int      `json:"asset_count"`
				Scope      []string `json:"scope"`
			}
			out := make([]companyOut, 0, len(cos))
			for _, c := range cos {
				if q != "" && !strings.Contains(strings.ToLower(c.Name), q) {
					continue
				}
				scope := make([]string, 0, len(c.Scope))
				for _, r := range c.Scope {
					scope = append(scope, r.Raw)
				}
				out = append(out, companyOut{ID: c.ID, Name: c.Name, AssetCount: c.AssetCount, Scope: scope})
			}
			return jsonResult(map[string]any{"count": len(out), "companies": out})
		},
	)
}

// splitLines splits a multi-line string into non-empty trimmed lines.
func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// WorkerTools returns the tool set for a work agent.
func (t *ToolSet) WorkerTools() []actool.CoreTool {
	return []actool.CoreTool{
		// list_findings mantido: antes de reportar uma vulnerabilidade, consulte as vulnerabilidades já confirmadas desta tarefa, evitando reportar a mesma vulnerabilidade duas vezes.
		t.listFindings(),
		t.addFinding(), t.recordFact(),
		// asset management (handlers guard nil store internally).
		// add_company_scope não é dado ao worker: definir o escopo de ativos da empresa é responsabilidade de planejamento/controle principal/Auto, o worker só executa exploração.
		t.insertAssets(), t.listAssets(),
		// Revisão entre works: o worker também pode reaproveitar as observações de outros works, evitando trabalho repetido.
		// search_all_worker_traces: sem precisar saber o intent_id antes, busca globalmente os passos que acertam por palavra-chave;
		// get_worker_trace: depois de travar um work, lista passos/busca no local/obtém o conteúdo completo.
		t.searchAllWorkerTraces(), t.getWorkerTrace(),
		// node_detail: depois de obter o intent_id/id de nó, o worker pode consultar os detalhes completos desse nó (junto com a revisão acima).
		t.nodeDetail(),
		// As ferramentas a seguir continuam [não dadas] ao worker, ficando só para planner/main (ler contexto e fazer retrospectiva entre works é responsabilidade de planejamento,
		// o worker só executa e grava uma única intenção): list_facts / list_companies / list_worker_traces.
	}
}

// MainAgentTools returns the human-interface tool set.
func (t *ToolSet) MainAgentTools() []actool.CoreTool {
	return []actool.CoreTool{
		t.graphOverview(), t.listFindings(), t.listFacts(), t.nodeDetail(),
		t.expandDigest(), // cold-digest §6.1
		t.getWorkerOutput(), t.getWorkerTrace(), t.searchAllWorkerTraces(), t.addHint(), t.addIntent(),
		// steer_work: a pessoa pode, em tempo real, injetar numa intenção (work) em execução uma instrução de correção de rumo (sem interromper nem perder progresso).
		t.steerWorkTool(),
		// set_goals: a pessoa pode, em execução, acrescentar a esta tarefa um novo objetivo final (o planejador reavalia a partir dele se foi atingido).
		t.setGoals(),
		// set_constraints: a pessoa pode, em execução, complementar/alterar as restrições de operação (allow/deny) desta tarefa, delimitando a fronteira de exploração do planner/worker.
		t.setConstraints(),
		// asset management (handlers guard nil store internally)
		t.insertAssets(), t.addCompanyScope(), t.listAssets(),
		t.addFinding(), t.recordFact(),
		t.addTaskScope(),
		// list_untested_assets: consulta sob demanda os ativos não testados dentro do escopo desta tarefa (tipo + paginação), decidindo por conta própria o que testar adicionalmente.
		t.listUntestedAssets(),
	}
}

// AllDomainTools returns the union of all domain tools across all agent types,
// deduped by name (mainagent order wins). Used by the server to build a registry
// for injecting domain tools into agents (Auto, custom) that don't own a per-task
// ToolSet. The caller provides real stores; tools are callable at taskID=0 scope.
func (t *ToolSet) AllDomainTools() []actool.CoreTool {
	seen := map[string]bool{}
	var out []actool.CoreTool
	all := append(append(t.MainAgentTools(), t.PlannerTools()...), t.WorkerTools()...)
	for _, tool := range all {
		if !seen[tool.Name()] {
			seen[tool.Name()] = true
			out = append(out, tool)
		}
	}
	return out
}
