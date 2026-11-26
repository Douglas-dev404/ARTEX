package agent

import (
	"encoding/json"
	"fmt"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

const findingIDGuidance = "\n\n**Convenção de numeração de vulnerabilidades**: finding_id é o ID do registro independente da vulnerabilidade; finding_node_id é o ID do nó de exploração. Os id de list_findings / list_task_findings / node_detail / get_task_node_detail continuam sendo o ID do nó de exploração; leia o número independente no finding_id do mesmo retorno. get_finding_traffic / bind_finding_traffic usam o finding_id independente. O parâmetro finding_id do antigo update_finding_report ainda recebe finding_node_id. Não use o número da primeira linha de report_finding nas ferramentas de evidência, nem tente adivinhar outros números ao encontrar erro de numeração."

// The server supplies the persisted setting. A missing setting/host is off.
// Consulted at assembly and again on writes so an already-running session
// cannot keep binding after the user switches the feature off.
var FindingTrafficBindingEnabled func() bool

func findingTrafficBindingEnabled() bool {
	return FindingTrafficBindingEnabled != nil && FindingTrafficBindingEnabled()
}

// Applied after ToolResolve: user descriptions and prompts remain intact, while
// all actual reporters (including Planner and custom chat agents) see the same
// API contract. Disabled/unbound tools are never reintroduced here.
func findingWorkflowTools(agentKey string, tools []actool.CoreTool) ([]actool.CoreTool, string) {
	if !findingTrafficBindingEnabled() {
		out := make([]actool.CoreTool, 0, len(tools))
		for _, tool := range tools {
			if tool.Name() == "bind_finding_traffic" {
				continue
			}
			if agentKey == "reporter" && (tool.Name() == "traffic_search" || tool.Name() == "traffic_get" || tool.Name() == "traffic_blob") {
				continue
			}
			switch tool.Name() {
			case "report_finding", "add_hint", "add_task_hint":
				// Work on a copy: toggling back on must restore the original schema.
				raw, _ := json.Marshal(tool.InputSchema())
				var schema map[string]any
				if json.Unmarshal(raw, &schema) == nil {
					stripTrafficParameters(schema)
					tool = DecorateTool(tool, tool.Description(), schema)
				}
			}
			out = append(out, tool)
		}
		return out, ""
	}
	out := append([]actool.CoreTool(nil), tools...)
	has := map[string]bool{}
	for i, tool := range out {
		has[tool.Name()] = true
		note := ""
		switch tool.Name() {
		case "report_finding":
			note = "\nPor padrão, o Agent de relatório confere e vincula o tráfego antes de redigir o relatório. Quem reporta mantém em evidence os comandos de verificação, as saídas-chave, os IDs de tráfego reais já existentes e sua finalidade, para que o Agent de relatório os verifique contra o registro de execução; não é necessário consultar pacotes extras só para vincular. Compatível com vínculo imediato explícito: traffic_refs ou evidence_hint_id podem enviar referências já verificadas, sendo que o segundo lê as referências estruturadas do hint designado desta tarefa; se qualquer uma for inválida, todo este reporte falha. TCP/sem pacotes não precisam desses parâmetros opcionais. O retorno traz finding_id e finding_node_id, que representam respectivamente o registro independente e o nó de exploração."
		case "add_hint", "add_task_hint":
			note = "\nAo repassar uma vulnerabilidade confirmada, mantenha nos traffic_refs do hint correspondente o ID, a finalidade, a descrição e a ordem do tráfego já verificado (um único vai no nível superior; em lote, nos elementos correspondentes de hints), e explique em text qual vulnerabilidade específica ele comprova. Quem chama não pode repassar apenas o texto e descartar as referências de tráfego existentes. Candidatos não verificados não podem ser passados como evidência."
		case "get_finding_traffic", "bind_finding_traffic", "list_findings", "list_task_findings", "node_detail", "get_task_node_detail", "update_finding_report":
			note = findingIDGuidance
		}
		if note != "" {
			out[i] = DecorateTool(tool, tool.Description()+note, tool.InputSchema())
		}
	}
	guidance := ""
	if has["report_finding"] || has["add_task_hint"] || has["add_hint"] {
		guidance = "\n\n**Repasse de evidência de tráfego (opcional)**: o vínculo automático é feito, por padrão, pelo Agent de relatório depois que a vulnerabilidade entra no banco e antes de redigir o relatório. Quem reporta deve manter em evidence os comandos de verificação, as saídas-chave, os IDs de tráfego reais já existentes e sua finalidade, e incluir o intent_id na tarefa, para facilitar o rastreamento pelo Agent de relatório; não é preciso consultar pacotes extras só para vincular. Quando Auto / Planner reportam em nome de outro, não descarte as referências que o executor já tem. add_hint / add_task_hint podem repassar com traffic_refs; o vínculo imediato explícito continua compatível com traffic_refs / evidence_hint_id de report_finding. Com TCP ou sem pacotes, registre normalmente, sem adivinhar IDs e sem repetir sondagens apenas para completar pacotes."
		if has["add_task_hint"] && !has["add_hint"] {
			guidance += "\nQuando a conversa da plataforma não tem contexto de tarefa, não chame report_finding diretamente; repasse via add_task_hint a uma tarefa correspondente existente, para que o Agent da tarefa o registre, e confira o resultado com list_task_findings."
		}
		if has["prove_goal"] || has["goal_met"] {
			guidance += "\nAntes de julgar o objetivo concluído, conclua primeiro o reporte/repasse das evidências já existentes desta rodada. Não encerre a tarefa nem cancele Workers só porque a vulnerabilidade em texto já foi registrada enquanto o repasse de evidências ainda não terminou; sem pacotes, não é preciso esperar nem forçar a captura."
		}
	}
	if has["update_finding_report"] && has["bind_finding_traffic"] && has["get_finding_traffic"] {
		guidance += "\n\n**Vinculação automática de tráfego antes do relatório (ativada)**: você é responsável por conferir e vincular o tráfego da vulnerabilidade acionada desta vez e, depois, redigir o relatório. Primeiro obtenha finding_id e finding_node_id explícitos a partir do JSON retornado por report_finding ou de get_task_node_detail / list_task_findings. Leia os detalhes da vulnerabilidade, o registro de execução da intenção correspondente e a lista de evidências existentes, dando preferência aos IDs reais repassados por quem reportou. Se esta verificação foi por HTTP e a ferramenta de tráfego está disponível, use traffic_search para filtrar candidatos e depois traffic_get para conferir, um a um, que a requisição/resposta realmente sustenta a vulnerabilidade; domínio e horário servem apenas para filtrar e não comprovam a pertença. Vincule as evidências confirmadas, na ordem de reprodução, com bind_finding_traffic(finding_id, traffic_refs), escolhendo baseline / proof / verification / supporting e explicando a finalidade. Opere apenas sobre a vulnerabilidade desta vez, sem criar vulnerabilidades duplicadas nem sondar o alvo novamente. Após o vínculo bem-sucedido, chame get_finding_traffic de novo para obter a version mais recente, leia o corpo necessário e passe a version efetivamente lida como evidence_version para update_finding_report (cujo parâmetro finding_id continua usando finding_node_id). Vínculos já existentes não precisam ser adicionados de novo. Com TCP, tráfego não coletado, ferramenta indisponível ou sem correspondência exata, pule o vínculo automático e redija o relatório normalmente com base nas evidências de texto/comando, explicando o motivo, sem adivinhar para completar o tráfego. Se o vínculo falhar, não declare sucesso; preserve as evidências existentes e explique no relatório o motivo de não ter vinculado."
	}
	if guidance != "" || has["get_finding_traffic"] || has["update_finding_report"] {
		guidance += findingIDGuidance
	}
	return out, guidance
}

func stripTrafficParameters(schema map[string]any) {
	props, _ := schema["properties"].(map[string]any)
	delete(props, "traffic_refs")
	delete(props, "evidence_hint_id")
	if required, ok := schema["required"].([]any); ok {
		kept := required[:0]
		for _, key := range required {
			if key != "traffic_refs" && key != "evidence_hint_id" {
				kept = append(kept, key)
			}
		}
		schema["required"] = kept
	}
	if hints, ok := props["hints"].(map[string]any); ok {
		if items, ok := hints["items"].(map[string]any); ok {
			stripTrafficParameters(items)
		}
	}
}

// HintTrafficSchema is shared by the task-local and cross-task hint tools.
func HintTrafficSchema() map[string]any {
	return map[string]any{"type": "array", "description": "Opcional: referências de tráfego já verificadas e correspondentes à vulnerabilidade específica deste hint, mantendo a ordem; após o repasse, report_finding pode receber evidence_hint_id para carregar essas referências.", "items": obj(map[string]any{"traffic_id": str("ID de tráfego real"), "role": str("baseline / proof / verification / supporting"), "note": str("Que conclusão este tráfego sustenta")}, "traffic_id")}
}

func (t *ToolSet) findingRefsFromHint(hintID int64, explicit []db.TrafficRef) ([]db.TrafficRef, error) {
	if hintID <= 0 {
		return db.NormalizeTrafficRefs(explicit)
	}
	n, err := t.ts.GetNode(hintID) // local store only: inherited hints cannot supply evidence
	if err != nil {
		return nil, err
	}
	if n == nil || n.Kind != db.KindHint {
		return nil, fmt.Errorf("evidence_hint_id=%d deve ser um nó de hint desta tarefa (hints herdados não podem ser usados diretamente para vínculo)", hintID)
	}
	var payload struct {
		Refs []db.TrafficRef `json:"traffic_refs"`
	}
	if err := json.Unmarshal(n.Payload, &payload); err != nil {
		return nil, err
	}
	return db.NormalizeTrafficRefs(append(append([]db.TrafficRef{}, explicit...), payload.Refs...))
}
