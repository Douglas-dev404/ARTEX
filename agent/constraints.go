package agent

import (
	"strings"

	"github.com/Autumn-27/artex/db"
)

// constraintBlock renders this task's operation constraints (task_constraints) as a
// high-priority block appended to the planner/worker system prompt. allow/deny are
// grouped; empty string when there are no constraints (or ts is nil). The framing
// deliberately puts these ABOVE the exploration/expansion heuristics so a declared
// boundary wins the tug-of-war against "chase another entry surface".
func constraintBlock(ts *db.ExplorationStore) string {
	if ts == nil {
		return ""
	}
	rows, err := ts.ListConstraints()
	if err != nil || len(rows) == 0 {
		return ""
	}
	var allow, deny []string
	for _, c := range rows {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		if c.Kind == "allow" {
			allow = append(allow, "- "+text)
		} else {
			deny = append(deny, "- "+text)
		}
	}
	if len(allow) == 0 && len(deny) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[Restrições de operação (prioridade máxima, acima de qualquer heurística de exploração/expansão abaixo; antes de gerar cada intenção e de executar cada ação, verifique primeiro se há violação; havendo violação, não prossiga)]:")
	if len(allow) > 0 {
		b.WriteString("\nOperações permitidas:\n")
		b.WriteString(strings.Join(allow, "\n"))
	}
	if len(deny) > 0 {
		b.WriteString("\nOperações proibidas:\n")
		b.WriteString(strings.Join(deny, "\n"))
	}
	b.WriteString("\n(Descobrir novos alvos/portas/hosts fora das restrições não equivale a ter autorização: a menos que caiam no escopo permitido acima, registre-os como fato out-of-scope e ignore-os; não derive intenções nem execute ações para eles.)")
	return b.String()
}
