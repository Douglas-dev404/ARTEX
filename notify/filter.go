package notify

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// Filter é o contrato da coluna JSONB notification_channels.filter: as condições de filtro de uma instância de canal.
// Todos os campos são opcionais, e o padrão é "não filtrar" — essa é justamente a semântica de reserva para configurações malformadas, veja ParseFilter.
type Filter struct {
	// MinSeverity é o limiar mínimo de severidade (low/medium/high/critical); vazio = sem limiar.
	MinSeverity string `json:"min_severity"`
	// TaskIDs / AssetIDs vazios significam sem restrição; quando não vazios, exige-se que o evento tenha interseção com eles.
	TaskIDs  []int64 `json:"task_ids"`
	AssetIDs []int64 `json:"asset_ids"`
	// VulnClassInclude vazio significa aceitar tudo; quando não vazio, exige que a vulnclass corresponda a qualquer uma das palavras-chave.
	// VulnClassExclude exclui ao corresponder a qualquer palavra-chave (a exclusão tem prioridade sobre a inclusão).
	// A correspondência é por substring sem distinção de maiúsculas e minúsculas — mais segura que regex: uma regex configurada errada não faz o canal falhar silenciosamente.
	VulnClassInclude []string `json:"vulnclass_include"`
	VulnClassExclude []string `json:"vulnclass_exclude"`
	// OnStatusChange decide se este canal recebe eventos de mudança de status de vulnerabilidade (só faz sentido no modo realtime).
	OnStatusChange bool `json:"on_status_change"`
}

// ParseFilter interpreta a configuração de filtro do canal.
//
// **Nunca retorna error.** Esta é uma escolha de projeto deliberada: quando a configuração do filtro está malformada, ela sempre degrada para o Filter de valor
// zero (= sem filtro = tudo corresponde), porque para um sistema de notificação de vulnerabilidades, **enviar um a mais é muito melhor que
// perder silenciosamente uma de alta severidade**. Fazer a falha de interpretação virar "não enviar" equivale a dar ao usuário um canal que parece
// configurado, mas na prática não envia nada — o pior modo de falha.
func ParseFilter(raw []byte) Filter {
	var f Filter
	if len(raw) == 0 {
		return f
	}
	// Em caso de falha de interpretação, f permanece com valor zero, ou seja, sem filtro.
	_ = json.Unmarshal(raw, &f)
	return f
}

// ValidMinSeverity informa se s é um limiar de severidade válido (string vazia significa sem limiar).
func ValidMinSeverity(s string) bool {
	if s == "" {
		return true
	}
	_, ok := severityRank[s]
	return ok
}

// Validate valida os campos do filtro cujo **valor é restrito**, para ser chamado ao salvar o canal.
//
// Por que é preciso barrar na escrita: o julgamento de Match para um limiar desconhecido é `rank >= 0`, sempre verdadeiro —
// ou seja, um erro de digitação em min_severity ("hgih") faz o filtro **falhar silenciosamente** e virar
// "enviar tudo". Isso vai na mesma direção da escolha deste pacote, "melhor enviar a mais do que deixar de enviar" (nada se perde),
// mas a consequência é que o usuário acha que está fazendo envio por níveis e na verdade despeja todas as vulnerabilidades no grupo,
// sem nenhum indício de que configurou errado. Essa "degradação silenciosa" deve ser barrada justamente na entrada.
//
// Atenção: Validate só é usado no caminho de **escrita**. O caminho de leitura continua usando a semântica tolerante do ParseFilter,
// de modo que valores ruins já existentes nos dados históricos não impeçam o canal inteiro de ser lido.
func (f Filter) Validate() error {
	if !ValidMinSeverity(f.MinSeverity) {
		return fmt.Errorf("nível mínimo %q inválido; opções: low / medium / high / critical, ou deixe vazio para sem limite", f.MinSeverity)
	}
	return nil
}

// Match decide se um evento deve ser entregue a um canal com estas condições de filtro.
//
// **Nunca retorna error**, pelo mesmo motivo do ParseFilter: qualquer exceção interna é tratada como "corresponde".
// Ordem do julgamento: tipo do evento → limiar de severidade → escopo de tarefa/ativo → palavras-chave do tipo de vulnerabilidade.
func Match(f Filter, s Snapshot) bool {
	// Eventos de mudança de status só são recebidos por canais que o habilitaram explicitamente. Desligado por padrão, porque a grande maioria dos usuários
	// entende "push" como "descobrir uma nova vulnerabilidade", e não como acompanhar cada transição de status em forma de registro corrido.
	if s.Kind == EventFindingStatusChanged && !f.OnStatusChange {
		return false
	}
	if !AtLeast(s.Severity, f.MinSeverity) {
		return false
	}
	if len(f.TaskIDs) > 0 && !slices.Contains(f.TaskIDs, s.TaskID) {
		return false
	}
	if len(f.AssetIDs) > 0 && !intersectsInt(f.AssetIDs, s.AssetIDs) {
		return false
	}
	// A exclusão tem prioridade: corresponder a qualquer palavra-chave de exclusão tira o evento, mesmo que também corresponda à lista de inclusão.
	if len(f.VulnClassExclude) > 0 && containsAnyFold(s.VulnClass, f.VulnClassExclude) {
		return false
	}
	if len(f.VulnClassInclude) > 0 && !containsAnyFold(s.VulnClass, f.VulnClassInclude) {
		return false
	}
	return true
}

func intersectsInt(a, b []int64) bool {
	// Uma varredura linear em coleções pequenas basta; ambos os lados têm ordem de grandeza de "algumas dezenas marcadas à mão",
	// e o custo de montar um map é maior que o ganho.
	for _, v := range b {
		if slices.Contains(a, v) {
			return true
		}
	}
	return false
}

// containsAnyFold informa se s contém qualquer palavra-chave de keywords (sem distinção de maiúsculas e minúsculas).
func containsAnyFold(s string, keywords []string) bool {
	lower := strings.ToLower(s)
	for _, kw := range keywords {
		kw = strings.ToLower(strings.TrimSpace(kw))
		if kw != "" && strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
