package agent

import (
	"context"

	"github.com/Autumn-27/artex/db"
)

// FindingRecorder is injected by the host; agents never synthesize or copy
// evidence bodies themselves. Its implementation owns the atomic write.
type FindingRecorder interface {
	Record(context.Context, db.RecordFindingInput, []db.TrafficRef) (*db.RecordedFinding, error)
}

// Tool-use guidance is appended without replacing the user's editable prompt.
// It does not require capture or claim that unavailable traffic tools exist.
const findingTrafficGuidance = "\n\n**Evidência de tráfego da vulnerabilidade (opcional)**: ao reportar uma vulnerabilidade com report_finding, se houver requisições/respostas HTTP que você já examinou e confirmou que sustentam a conclusão, use traffic_refs para vincular os IDs reais na ordem de reprodução; domínio e horário servem apenas como filtro de candidatos, sem presumir associação. Para vulnerabilidades não HTTP (como TCP), tráfego não capturado ou sem correspondência exata, omita ou passe []; mantenha em evidence a saída de comandos, logs e outras evidências verificáveis, e de preferência explique por que não houve vínculo. Não adivinhe IDs nem repita sondagens apenas para completar pacotes."

func (t *ToolSet) SetFindingRecorder(r FindingRecorder)   { t.findingRecorder = r }
func (w *Worker) SetFindingRecorder(r FindingRecorder)    { w.findingRecorder = r }
func (p *Planner) SetFindingRecorder(r FindingRecorder)   { p.findingRecorder = r }
func (m *MainAgent) SetFindingRecorder(r FindingRecorder) { m.findingRecorder = r }
