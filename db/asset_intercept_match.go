package db

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Camada de correspondência/execução das regras de interceptação de ativos. asset_intercept.go cuida apenas do armazenamento das regras; aqui
// comparamos domínio/IP/URL do "ativo alvo" com as regras habilitadas. Chamada pelas ferramentas do agent (add_intent,
// insert_assets) antes de emitir a intenção / inserir ativos; em caso de acerto, a operação é recusada.

// AssetInterceptKindLabel retorna o rótulo legível de kind, usado nas mensagens de explicação ao agent.
func AssetInterceptKindLabel(kind string) string {
	switch kind {
	case "exact_domain":
		return "Domínio (exato)"
	case "exact_ip":
		return "IP (exato)"
	case "exact_url":
		return "URL (exato)"
	case "fuzzy_domain":
		return "Domínio (parcial)"
	case "fuzzy_ip":
		return "IP (parcial)"
	case "fuzzy_url":
		return "URL (parcial)"
	case "cidr":
		return "Faixa CIDR"
	}
	return kind
}

// Reason retorna um motivo legível do acerto, no formato: Regra de interceptação de ativos atingida [Domínio (parcial): .gov.cn] (observação).
func (r AssetInterceptRule) Reason() string {
	s := fmt.Sprintf("Regra de interceptação de ativos atingida [%s: %s]", AssetInterceptKindLabel(r.Kind), r.Pattern)
	if note := strings.TrimSpace(r.Note); note != "" {
		s += " (" + note + ")"
	}
	return s
}

// matchOne verifica se uma única regra habilitada atinge os candidatos de domínio/IP/URL informados e retorna o valor específico que atingiu.
func matchOne(r AssetInterceptRule, domains, ips, urls []string) (string, bool) {
	p := strings.TrimSpace(r.Pattern)
	if p == "" {
		return "", false
	}
	switch r.Kind {
	case "exact_domain":
		for _, d := range domains {
			if strings.EqualFold(strings.TrimSpace(d), p) {
				return d, true
			}
		}
	case "exact_ip":
		for _, ip := range ips {
			if strings.TrimSpace(ip) == p {
				return ip, true
			}
		}
	case "exact_url":
		for _, u := range urls {
			if strings.TrimSpace(u) == p {
				return u, true
			}
		}
	case "fuzzy_domain":
		lp := strings.ToLower(p)
		for _, d := range domains {
			if d != "" && strings.Contains(strings.ToLower(d), lp) {
				return d, true
			}
		}
	case "fuzzy_ip":
		for _, ip := range ips {
			if ip != "" && strings.Contains(ip, p) {
				return ip, true
			}
		}
	case "fuzzy_url":
		lp := strings.ToLower(p)
		for _, u := range urls {
			if u != "" && strings.Contains(strings.ToLower(u), lp) {
				return u, true
			}
		}
	case "cidr":
		_, ipnet, err := net.ParseCIDR(p)
		if err != nil {
			return "", false
		}
		for _, ip := range ips {
			if pip := net.ParseIP(strings.TrimSpace(ip)); pip != nil && ipnet.Contains(pip) {
				return ip, true
			}
		}
	}
	return "", false
}

// MatchAssetInterceptRules retorna a primeira regra habilitada que atinge os candidatos de domínio/IP/URL informados, e o valor específico atingido.
// Usada por insert_assets com a entrada original (assetInputItem, ainda não gravado no banco).
func MatchAssetInterceptRules(rules []AssetInterceptRule, domains, ips, urls []string) (AssetInterceptRule, string, bool) {
	for _, r := range rules {
		if !r.Enabled {
			continue
		}
		if v, ok := matchOne(r, domains, ips, urls); ok {
			return r, v, true
		}
	}
	return AssetInterceptRule{}, "", false
}

// interceptCandidates extrai de um ativo já gravado os candidatos de domínio/IP/URL para a correspondência de interceptação.
// O host da URL é separado e classificado, para que ativos de serviço que têm "apenas URL" também sejam atingidos pelas regras de domínio/IP.
func (a *Asset) interceptCandidates() (domains, ips, urls []string) {
	add := func(dst *[]string, s string) {
		if s = strings.TrimSpace(s); s != "" {
			*dst = append(*dst, s)
		}
	}
	add(&domains, a.Domain)
	add(&domains, a.RootDomain)
	for _, d := range a.BoundDomains {
		add(&domains, d)
	}
	add(&ips, a.IP)
	add(&urls, a.URL)
	if a.URL != "" {
		if u, err := url.Parse(a.URL); err == nil {
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

// InterceptLabel retorna um identificador curto do ativo, usado nas mensagens de explicação ao agent.
func (a *Asset) InterceptLabel() string {
	var target string
	switch {
	case a.Domain != "":
		target = a.Domain
	case a.URL != "":
		target = a.URL
	case a.IP != "":
		target = a.IP
	default:
		target = fmt.Sprintf("#%d", a.ID)
	}
	return fmt.Sprintf("Ativo #%d[%s] %s", a.ID, a.Type, target)
}

// hasEnabledRule verifica se existe alguma regra habilitada no conjunto de regras.
func hasEnabledRule(rules []AssetInterceptRule) bool {
	for _, r := range rules {
		if r.Enabled {
			return true
		}
	}
	return false
}

// AssetGateDecision é o resultado da decisão do portão "bloquear primeiro, permitir depois" para um conjunto de candidatos.
type AssetGateDecision struct {
	Allowed bool
	Reason  string // motivo da recusa (sem o identificador do ativo); vazio quando Allowed=true
}

// EvaluateAssetGate executa a decisão do portão no nível de tarefa:
//  1. Se atingir qualquer blockRules habilitada -> recusa (motivo de interceptação).
//  2. Senão, se allowRules tiver itens habilitados e nenhum for atingido -> recusa (fora do escopo permitido).
//  3. Caso contrário, libera.
//
// Quando allowRules está vazia / sem itens habilitados, o portão de permissão não vale (a lista de permissão não é ativada e tudo é liberado),
// evitando que "nenhuma regra de permissão configurada" bloqueie todos os ativos.
func EvaluateAssetGate(blockRules, allowRules []AssetInterceptRule, domains, ips, urls []string) AssetGateDecision {
	if rule, _, ok := MatchAssetInterceptRules(blockRules, domains, ips, urls); ok {
		return AssetGateDecision{Allowed: false, Reason: rule.Reason()}
	}
	if hasEnabledRule(allowRules) {
		if _, _, ok := MatchAssetInterceptRules(allowRules, domains, ips, urls); !ok {
			return AssetGateDecision{Allowed: false, Reason: "fora do escopo permitido da tarefa (lista de permissão), teste não autorizado"}
		}
	}
	return AssetGateDecision{Allowed: true}
}

// AssetInterceptHit descreve um ativo recusado pelo portão (atingido por interceptação ou fora do escopo permitido).
type AssetInterceptHit struct {
	Asset  *Asset
	Reason string // motivo legível
}

// Describe retorna uma explicação legível: informações do ativo + motivo.
func (h AssetInterceptHit) Describe() string {
	return fmt.Sprintf("%s → %s", h.Asset.InterceptLabel(), h.Reason)
}

// ListAssetInterceptRules é um repasse do método homônimo de *DB, para que chamadores que só possuem AssetStore
// (como as ferramentas do agent) também possam ler as regras.
func (s *AssetStore) ListAssetInterceptRules() ([]AssetInterceptRule, error) {
	return s.db.ListAssetInterceptRules()
}

// CheckAssetsIntercept carrega os ativos por id e executa a decisão do portão "bloquear primeiro, permitir depois" em cada um, retornando todos os
// ativos recusados. Regras de interceptação = globais ∪ block do nível de tarefa; regras de permissão = allow do nível de tarefa (somente desta tarefa).
// Sem ids, retorna rápido. Usa GetByIDs global (sem filtro de escopo da tarefa) para que a interceptação não seja enfraquecida pelo escopo.
func (s *AssetStore) CheckAssetsIntercept(taskID int64, ids []int64) ([]AssetInterceptHit, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	blockRules, err := s.db.ListAssetInterceptRules()
	if err != nil {
		return nil, err
	}
	var allowRules []AssetInterceptRule
	if taskID > 0 {
		tb, ta, err := s.TaskInterceptRulesSplit(taskID)
		if err != nil {
			return nil, err
		}
		blockRules = append(blockRules, tb...)
		allowRules = ta
	}
	// Sem regras de interceptação e sem regras de permissão habilitadas -> não há o que decidir, tudo é liberado.
	if len(blockRules) == 0 && !hasEnabledRule(allowRules) {
		return nil, nil
	}
	assets, err := s.GetByIDs(ids)
	if err != nil {
		return nil, err
	}
	var hits []AssetInterceptHit
	for _, a := range assets {
		domains, ips, urls := a.interceptCandidates()
		if d := EvaluateAssetGate(blockRules, allowRules, domains, ips, urls); !d.Allowed {
			hits = append(hits, AssetInterceptHit{Asset: a, Reason: d.Reason})
		}
	}
	return hits, nil
}
