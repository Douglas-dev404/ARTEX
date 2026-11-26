package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// jsonResult marshals v to a JSON tool result.
func jsonResult(v any) (actool.Result, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	return actool.Text(string(b)), nil
}

// Este arquivo implementa o P2 "conjunto de ferramentas de orquestração entre tarefas" (docs/benchmark-orchestration §2 P2). São host tools — precisam
// acessar o Manager (Store de qualquer tarefa), o Engine (pausa) e o fluxo de criação de tarefa, por isso vivem na camada server.
// As ferramentas de leitura redirecionam as "ferramentas per-task existentes" para rodar no store da tarefa alvo (cria um ToolSet temporário
// e dá Call na ferramenta correspondente), reutilizando exatamente a mesma lógica; as de controle (spawn/pause) chamam direto Manager/Engine.
// Como as ferramentas de tráfego, recebem seed na tabela tools e são vinculadas por agent (só ficam visíveis se vinculadas ao agent de orquestração).

// hostTools is the runtime host-tool provider fed to ToolAugment: traffic tools
// (gated by capture) + cross-task orchestration tools + user-defined custom tools.
// The second return is the names of custom tools flagged `deferred` (schema
// withheld, routed via SearchExtraTools/ExecuteExtraTool). Per-agent binding still
// decides who actually sees any of them.
//
//nolint:unused // used as the hostTools provider in wireAgentAugment
func (s *Server) hostTools() ([]actool.CoreTool, map[string][]string) {
	tools := append(s.m.HostTools(), s.orchestrationTools()...)
	tools = append(tools, s.findingRetestTools()...)
	tools = append(tools, s.platformTools()...) // ferramentas de operação da plataforma (criar/editar skill/ferramenta/MCP, para o Auto usar)
	custom, err := s.customTools()
	if err != nil {
		log.Printf("[custom-tool] falha ao carregar: %v", err)
		return tools, nil
	}
	tools = append(tools, custom...)
	// deferred custom tools → name -> its bound agent keys. ToolAugment turns a
	// name into a deferred entry only for agents it's actually bound to (so we don't
	// advertise a tool the per-agent binding will drop from the callable set).
	deferred := map[string][]string{}
	rows, _ := s.m.pg.ListCustomTools()
	for _, t := range rows {
		if t.Deferred && t.Enabled {
			deferred[t.Key] = t.Agents
		}
	}
	return tools, deferred
}

// orchestrationTools returns the cross-task tool set. Bound per-agent via the
// tools table (default: no binding — opt-in for orchestration agents).
func (s *Server) orchestrationTools() []actool.CoreTool {
	return []actool.CoreTool{
		s.toolListTasks(),
		s.toolListLLMProfiles(),
		s.toolSpawnTask(),
		s.toolPauseTask(),
		s.toolGetTaskGraph(),
		s.toolListTaskFindings(),
		s.toolAddHint(),
		s.toolGetWorkerTrace(),
		s.toolListWorkerTraces(),
		s.toolSearchWorkerTraces(),
		s.toolGetTaskNodeDetail(),
		s.toolUpdateFindingReport(),
		s.toolGetFindingTraffic(),
		s.toolBindFindingTraffic(),
	}
}

// --- schema helpers ---

func strParam(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// parseProfileID reads an LLM profile id from a tool arg that may arrive as a JSON
// number (5) or a numeric string ("5"); returns 0 when absent/unparseable.
func parseProfileID(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		v, _ := strconv.ParseInt(strings.TrimSpace(str), 10, 64)
		return v
	}
	return 0
}

func objSchema(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		m["required"] = req
	}
	return m
}

func roTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: name, Description: desc, Schema: schema,
		ReadOnly:   func(json.RawMessage) bool { return true },
		Concurrent: func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

func wrTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: name, Description: desc, Schema: schema,
		Permissions: func(context.Context, json.RawMessage, permission.Context) permission.Decision {
			return permission.Allowed()
		},
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

// delegateToTask resolves the `task_id` in the input, builds a ToolSet bound to
// that task's store, strips task_id, and calls the chosen per-task tool — so the
// cross-task read reuses the exact in-task logic against another task.
func (s *Server) delegateToTask(ctx context.Context, in json.RawMessage, pick func(*agent.ToolSet) actool.CoreTool) (actool.Result, error) {
	var head struct {
		TaskID string `json:"task_id"`
	}
	_ = json.Unmarshal(in, &head)
	if strings.TrimSpace(head.TaskID) == "" {
		return actool.Errorf("task_id é obrigatório"), nil
	}
	t, ok := s.m.Task(head.TaskID)
	if !ok {
		return actool.Errorf("task não existe: " + head.TaskID), nil
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(in, &m)
	delete(m, "task_id")
	inner, _ := json.Marshal(m)
	tsx := agent.NewToolSet(t.Store, "orchestrator")
	if s.m.Assets() != nil {
		tsx.SetAssetStore(s.m.Assets(), s.m.Assets().Companies())
	}
	tsx.SetNotify(t.Notify)         // despertar genérico (escritas sem callback dedicado passam por ele; ferramentas de leitura são no-op)
	tsx.SetNotifyHint(t.NotifyHint) // add_hint → registra o gatilho "uma pessoa adicionou N dicas estratégicas: …" e acorda o planner
	return pick(tsx).Call(ctx, inner, nil)
}

// --- tools ---

func (s *Server) toolListTasks() actool.CoreTool {
	return roTool("list_tasks",
		"Lista todas as tarefas (id/descrição/meta/estado/tempo de execução/tarefa pai/configuração de LLM); o agent de orquestração usa para ter a visão global, ver quais tarefas estão travadas há muito tempo e qual LLM cada uma usa. Tempo de execução: em execução = criação→agora, estado terminal = criação→última atividade (segundos). llm_profile: nome da configuração usada pelo planner/worker da tarefa, (configuração ativa) = segue a configuração ativa global.",
		objSchema(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			lastAct, _ := s.m.PG().LastActivityAll()
			// id -> name to resolve each task's pinned LLM profile.
			profName := map[int64]string{}
			if profs, err := s.m.pg.ListProfiles(); err == nil {
				for _, p := range profs {
					profName[p.ID] = p.Name
				}
			}
			out := make([]map[string]any, 0)
			for _, t := range s.m.List() {
				status := s.deriveTaskStatus(t)
				end := lastAct[t.ExpID]
				if live := s.engine.LastActivity(t.ID); live > end {
					end = live
				}
				dur := int64(0)
				if status == "running" {
					dur = time.Now().Unix() - t.CreatedAt
				} else if end > t.CreatedAt {
					dur = end - t.CreatedAt
				}
				row := map[string]any{"id": t.ID, "description": t.Description, "goal": t.Goal, "status": status, "run_seconds": dur}
				if t.ParentRef != "" {
					row["parent_ref"] = t.ParentRef
				}
				llmState := t.llmStateSnapshot()
				if llmState.ProfileID == nil {
					row["llm_profile"] = "(configuração ativa)"
				} else if n, ok := profName[*llmState.ProfileID]; ok {
					row["llm_profile"] = n
				} else {
					row["llm_profile"] = fmt.Sprintf("#%d(excluída)", *llmState.ProfileID)
				}
				out = append(out, row)
			}
			return jsonResult(out)
		})
}

// toolListLLMProfiles lists the available LLM profiles (name/model/active) so an
// orchestration agent can pick one for spawn_task's llm_profile. Never leaks keys.
func (s *Server) toolListLLMProfiles() actool.CoreTool {
	return roTool("list_llm_profiles",
		"Lista as configurações de LLM (profile) disponíveis: id, nome, modelo, formato, se é a configuração ativa atual. Use o id no parâmetro llm_profile_id de spawn_task para designar um LLM exclusivo para a subtarefa (por exemplo, modelo barato para reconhecimento, modelo forte para exploração). Não inclui a API Key.",
		objSchema(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			profs, err := s.m.pg.ListProfiles()
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			out := make([]map[string]any, 0, len(profs))
			for _, p := range profs {
				out = append(out, map[string]any{
					"id": p.ID, "name": p.Name, "model": p.Model, "format": p.Format, "is_active": p.IsDefault,
				})
			}
			return jsonResult(map[string]any{"profiles": out})
		})
}

func (s *Server) toolSpawnTask() actool.CoreTool {
	return wrTool("spawn_task",
		"Cria uma nova subtarefa e inicia o motor de exploração, retornando task_id. Usada para despachar uma coisa (como um desafio/uma meta) como tarefa independente. parent_ref é opcional: informe o id da tarefa pai associada à orquestração atual para fazer a associação pai-filho.",
		objSchema(map[string]any{
			"description":            strParam("Descrição da tarefa (título curto)"),
			"goal":                   strParam("Meta da tarefa (o que se quer alcançar)"),
			"parent_ref":             strParam("Opcional: id da tarefa pai (para associação pai-filho)"),
			"source_task_ids":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": fmt.Sprintf("Opcional: lista de ids das tarefas de origem herdadas somente para leitura (no máximo %d). A subtarefa pode referenciar, somente para leitura, os ativos/conclusões já apurados por essas tarefas como ponto de partida; diferente do ponteiro puramente pai-filho de parent_ref, aqui é herança de conteúdo.", db.MaxTaskSourceCount)},
			"llm_profile_id":         map[string]any{"type": "integer", "description": "Opcional: id da configuração de LLM usada pelo planner/worker desta subtarefa (ver list_llm_profiles); se vazio, herda da tarefa pai e depois recorre à configuração ativa global"},
			"timeout_seconds":        map[string]any{"type": "integer", "description": "Opcional: timeout em nível de tarefa (segundos). Ao vencer, dispara o encerramento gracioso e entra no estado terminal timeout; vazio ou 0 = sem limite de tempo"},
			"plan_heartbeat_seconds": map[string]any{"type": "integer", "description": "Opcional: intervalo de disparo do heartbeat do planner (segundos). Se desde o fim da última rodada de planejamento/início da tarefa passou esse valor sem nenhum gatilho → dispara uma rodada de planejamento (fallback de deadlock + acordar para supervisionar workers em voo). Vazio ou 0 = padrão 600 (10min);"},
			"seed_first_intent":      map[string]any{"type": "boolean", "description": "Opcional: para tarefas simples pode ser ligado; na criação já envia uma intenção-semente (conteúdo = descrição + meta) para o worker começar o teste direto sem esperar o primeiro planner; padrão false (segue o padrão: planejar primeiro e depois executar)."},
		}, "description", "goal"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Description          string          `json:"description"`
				Goal                 string          `json:"goal"`
				ParentRef            string          `json:"parent_ref"`
				SourceTaskIDs        []string        `json:"source_task_ids"`
				LLMProfileID         json.RawMessage `json:"llm_profile_id"`
				TimeoutSeconds       int             `json:"timeout_seconds"`
				PlanHeartbeatSeconds int             `json:"plan_heartbeat_seconds"`
				SeedFirstIntent      bool            `json:"seed_first_intent"`
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Description) == "" {
				a.Description = "Tarefa sem nome"
			}
			if strings.TrimSpace(a.Goal) == "" {
				return actool.Errorf("goal é obrigatório"), nil
			}
			if a.TimeoutSeconds < 0 {
				a.TimeoutSeconds = 0
			}
			// Herança somente leitura de tarefas de origem: limite de quantidade + cada id válido/sem duplicata/existente, regras de validação iguais às da criação de tarefa por HTTP.
			if len(a.SourceTaskIDs) > db.MaxTaskSourceCount {
				return actool.Errorf(fmt.Sprintf("no máximo %d tarefas associadas podem ser selecionadas", db.MaxTaskSourceCount)), nil
			}
			sourceIDs := make([]int64, 0, len(a.SourceTaskIDs))
			seenSources := map[int64]bool{}
			for _, raw := range a.SourceTaskIDs {
				id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
				if err != nil || id <= 0 || seenSources[id] {
					return actool.Errorf("id de tarefa associada inválido ou duplicado"), nil
				}
				if _, ok := s.m.Task(strconv.FormatInt(id, 10)); !ok {
					return actool.Errorf(fmt.Sprintf("a tarefa associada #%d não existe", id)), nil
				}
				seenSources[id] = true
				sourceIDs = append(sourceIDs, id)
			}
			// LLM profile resolution: explicit id > inherit parent's pin > active(nil).
			var pin *int64
			if id := parseProfileID(a.LLMProfileID); id > 0 {
				if _, ok := s.loadProfileConfig(id); !ok {
					return actool.Errorf(fmt.Sprintf("a configuração de LLM #%d não existe ou não tem API Key definida", id)), nil
				}
				pin = &id
			} else if a.ParentRef != "" {
				if pt, ok := s.m.Task(a.ParentRef); ok {
					pin = pt.LLMProfileID
				}
			}
			var llmIDs []int64
			if pin != nil {
				llmIDs = []int64{*pin}
			}
			t, err := s.m.CreateTaskWithOptions(a.Description, a.Goal, db.TaskCreateOptions{
				SourceTaskIDs:        sourceIDs,
				LLMProfileIDs:        llmIDs,
				TimeoutSeconds:       a.TimeoutSeconds,
				PlanHeartbeatSeconds: a.PlanHeartbeatSeconds,
			})
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if a.ParentRef != "" {
				t.ParentRef = a.ParentRef
				if id, e := strconv.ParseInt(t.ID, 10, 64); e == nil {
					_ = s.m.PG().SetParentRef(id, a.ParentRef)
				}
			}
			// Fluxo compartilhado pós-criação, reutiliza o mesmo launchTask da criação de tarefa por HTTP (server.go createTask):
			// seed + decomposição visível em segundo plano das metas (rodada 0/passos do LLM/cada goal) + engine.Run.
			// seed_first_intent é false por padrão (padrão: planejar primeiro e depois executar); tarefas simples podem ligá-lo para enviar direto um work de teste.
			s.launchTask(t, a.Description+" "+a.Goal, a.SeedFirstIntent)
			return actool.Text(fmt.Sprintf("task created: %s", t.ID)), nil
		})
}

func (s *Server) toolPauseTask() actool.CoreTool {
	return wrTool("pause_task", "Pausa a tarefa indicada (para seus loops de planner/worker).",
		objSchema(map[string]any{"task_id": strParam("id da tarefa a pausar")}, "task_id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				TaskID string `json:"task_id"`
			}
			_ = json.Unmarshal(in, &a)
			t, ok := s.m.Task(a.TaskID)
			if !ok {
				return actool.Errorf("task não existe: " + a.TaskID), nil
			}
			if _, err := s.applyTaskControlWithCause(t, "pause", agent.AbortPausedByOrchestrator); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("task paused: " + a.TaskID), nil
		})
}

func (s *Server) toolGetTaskGraph() actool.CoreTool {
	return roTool("get_task_graph", "Lê a visão geral do grafo de exploração da tarefa indicada (igual a graph_overview: contagem de ativos/frontier/achados/cobertura etc.); use task_id para indicar a tarefa.",
		objSchema(map[string]any{"task_id": strParam("id da tarefa")}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).GraphOverviewTool)
		})
}

func (s *Server) toolListTaskFindings() actool.CoreTool {
	return roTool("list_task_findings", "Lê as vulnerabilidades confirmadas da tarefa indicada (inclui flag/PoC; cada item traz id/task_id/intent_id/vulnclass/severity/resumo/estado); use task_id para indicar a tarefa.",
		objSchema(map[string]any{"task_id": strParam("id da tarefa")}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).ListFindingsTool)
		})
}

func (s *Server) toolAddHint() actool.CoreTool {
	return wrTool("add_task_hint", "Injeta uma dica estratégica na tarefa indicada (o planner dessa tarefa a lerá ao gerar intenções na próxima rodada).\n"+
		"★Prefira em lote: coloque várias dicas no array hints e envie de uma vez (retorna o array ids, do mesmo tamanho e ordem de hints, itens com falha têm id=0); para uma só, omita hints e passe text no nível superior.",
		objSchema(map[string]any{
			"task_id":      strParam("id da tarefa"),
			"hints":        map[string]any{"type": "array", "description": "[Prefira este] array de dicas, cada elemento com os mesmos campos do nível superior (text/asset_ids/traffic_refs).", "items": objSchema(map[string]any{"text": strParam("Conteúdo da dica"), "asset_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}, "traffic_refs": agent.HintTrafficSchema()})},
			"text":         strParam("[Única] conteúdo da dica"),
			"traffic_refs": agent.HintTrafficSchema(),
			"asset_ids":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "ids de ativos ancorados (opcional, 0/1/vários; ids de ativos dentro desta tarefa)"},
		}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).AddHintTool)
		})
}

func (s *Server) toolGetWorkerTrace() actool.CoreTool {
	return roTool("get_task_worker_trace",
		"Vê o processo de execução de um work (intenção) na tarefa indicada: get_task_worker_trace(task_id, intent_id) mostra o resumo dos passos; depois, com step_ids=[...], obtém o conteúdo completo desses passos (no máximo 5 por vez; se passar mais, só os 5 primeiros são retornados).",
		objSchema(map[string]any{
			"task_id":   strParam("id da tarefa"),
			"intent_id": map[string]any{"type": "integer", "description": "id da intenção (o work dentro da tarefa)"},
			"step_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "Opcional: ids dos passos dos quais obter o conteúdo completo (no máximo 5 por vez; se passar mais, só os 5 primeiros são retornados e os demais são listados em omitted_step_ids)"},
		}, "task_id", "intent_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).GetWorkerTraceTool)
		})
}

func (s *Server) toolListWorkerTraces() actool.CoreTool {
	return roTool("list_task_worker_traces", "Lista quais works (intenções) rodaram na tarefa indicada + o nº de passos de cada um, para descobrir quais works valem a pena examinar (depois use get_task_worker_trace).",
		objSchema(map[string]any{"task_id": strParam("id da tarefa")}, "task_id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).ListWorkerTracesTool)
		})
}

func (s *Server) toolSearchWorkerTraces() actool.CoreTool {
	return roTool("search_task_worker_traces", "Busca por palavra-chave o processo de execução de todos os works da tarefa indicada (retorna o resumo dos passos encontrados + intent_id).",
		objSchema(map[string]any{"task_id": strParam("id da tarefa"), "q": strParam("palavra-chave da busca")}, "task_id", "q"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).SearchWorkerTracesTool)
		})
}

func (s *Server) toolGetTaskNodeDetail() actool.CoreTool {
	return roTool("get_task_node_detail",
		"Lê o conteúdo completo de um nó do grafo de exploração da tarefa indicada (achado/fato/intenção/meta: resumo + detalhes/evidências/PoC). id é o id do nó de exploração (como o retornado por report_finding, ou o id em list_task_findings). Antes de escrever o relatório de uma vulnerabilidade, use isto para obter a evidência completa dela.",
		objSchema(map[string]any{
			"task_id": strParam("id da tarefa"),
			"id":      map[string]any{"type": "integer", "description": "id do nó do grafo de exploração (não é id de ativo)"},
		}, "task_id", "id"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.delegateToTask(ctx, in, (*agent.ToolSet).NodeDetailTool)
		})
}

// toolUpdateFindingReport writes/overwrites a finding's detailed Markdown report.
// finding_id is the id report_finding returned ("finding recorded: <id>", the
// finding node id). The write (SetFindingReportByNodeID) is keyed by node_id and
// task-agnostic, so this host tool needs no task_id / exploration store.
func (s *Server) toolUpdateFindingReport() actool.CoreTool {
	return wrTool("update_finding_report",
		"Grava/atualiza o [relatório detalhado] de uma vulnerabilidade já registrada (texto completo em Markdown, sobrescreve todo o conteúdo antigo). Em finding_id passe o id retornado por report_finding (o número em \"finding recorded: <id>\"). Sugere-se que o relatório inclua: visão geral da vulnerabilidade, impacto e dano, passos de reprodução, evidência/PoC, recomendação de correção.",
		objSchema(map[string]any{
			"finding_id":       map[string]any{"type": "integer", "description": "id da vulnerabilidade alvo (id retornado por report_finding)"},
			"report":           strParam("Texto completo do relatório detalhado, em formato Markdown"),
			"evidence_version": map[string]any{"type": "integer", "description": "version da evidência retornada por get_finding_traffic; evita que o relatório sobrescreva novas alterações de evidência"},
		}, "finding_id", "report"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				EvidenceVersion *int64          `json:"evidence_version"`
				FindingID       json.RawMessage `json:"finding_id"`
				Report          string          `json:"report"`
			}
			_ = json.Unmarshal(in, &a)
			nodeID := parseProfileID(a.FindingID) // reutiliza o parse de "número ou string numérica"
			if nodeID <= 0 {
				return actool.Errorf("finding_id inválido"), nil
			}
			n, err := s.m.pg.SetFindingReportVersionByNodeID(ctx, nodeID, a.Report, a.EvidenceVersion)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if n == 0 {
				return actool.Errorf(fmt.Sprintf("não foi encontrado registro de vulnerabilidade para finding_id=%d (registre primeiro com report_finding)", nodeID)), nil
			}
			return actool.Text(fmt.Sprintf("finding %d report updated (%d chars)", nodeID, len(a.Report))), nil
		})
}

// deriveTaskStatus mirrors listTasks' status derivation for the list_tasks tool.
func (s *Server) deriveTaskStatus(t *Task) string {
	lifecycle := t.lifecycleSnapshot()
	switch {
	case isTerminalStatus(lifecycle.Status):
		return lifecycle.Status
	case lifecycle.Paused || s.engine.IsPaused(t.ID):
		return "paused"
	case s.engine.ReadyFor(t) && s.engine.Started(t.ID):
		return "running"
	}
	return "created"
}

// orchestrationToolSeeds seeds the cross-task tools into the tools table so they
// are bindable per-agent (default: bound to nobody — opt-in for orchestration
// agents). First-insert only, like the traffic seeds.
func (s *Server) seedOrchestrationTools() {
	// task-op + platform tools default-bind to the built-in Auto agent (ele nasceu para
	// operar a plataforma). SeedTool só vale na primeira inserção; as linhas já semeadas em bancos antigos são vinculadas por seedAutoDefaultBindings.
	autoAgents, _ := json.Marshal([]string{"auto"})
	for _, t := range s.orchestrationTools() {
		schema, _ := json.Marshal(t.InputSchema())
		bindings := autoAgents
		if t.Name() == "bind_finding_traffic" {
			bindings = json.RawMessage(`["reporter"]`)
		}
		_ = s.m.PG().SeedTool(t.Name(), t.Description(), schema, bindings)
	}
	for _, t := range s.platformTools() {
		schema, _ := json.Marshal(t.InputSchema())
		_ = s.m.PG().SeedTool(t.Name(), t.Description(), schema, autoAgents)
	}
	s.refreshBuiltinToolSchemas()
	s.seedAutoDefaultBindings()
	s.seedPlannerDefaultBindings()
	s.seedPlannerListAssetsBinding()
	s.seedCompanyScopeRebind()
	s.seedWorkerReadToolsUnbind() // list_facts/list_companies/list_worker_traces são desvinculados do worker por padrão (uma única vez)
	s.seedWorkerReadbackRebind()  // corrige a remoção indevida de uma migração antiga: revincula ao worker search_all_worker_traces/get_worker_trace/node_detail (uma única vez)
	s.seedAutoReportFindingBinding()
	s.unbindGoalMetDefault()
	s.reseedGoalsPrompt()             // o prompt de goals ganha o passo "extrair restrições operacionais" → bancos antigos recebem uma nova versão padrão (uma única vez)
	s.reseedMainAgentPrompt()         // o prompt de mainagent ganha "após atingir a meta, perguntar via add_intent se deve criar uma meta" (uma única vez)
	s.reseedPlannerPrompt()           // prompt do planner: reescreve a justificativa legítima de "0 intenções" + adiciona a conferência de aceitação quantificada (uma única vez)
	s.reseedWorkerPrompt()            // prompt do worker: adiciona o limiar de evidência para conclusões negativas (uma única vez)
	s.seedReporterAgent()             // pré-instala o agent de "redação de relatório" + vínculos de ferramentas + trigger de finding (uma única vez)
	s.upgradeReporterTriggerMessage() // migração complementar para bancos antigos: faz o reporter devolver evidence_version (uma única vez)
	s.seedFindingTrafficTools()       // acrescenta parâmetros opcionais de evidência e ferramentas de evidência somente leitura, preservando a configuração do usuário
	s.seedFindingWorkflowTools()
	// Nota: o vínculo padrão de ferramentas do pentest não precisa de migração — BuiltinToolSeeds, na inicialização limpa, já faz o seed de
	// list_assets/insert_assets/report_finding/list_findings/list_companies junto com
	// pentest (o projeto ainda não tem bancos antigos, não há migração).
}

// refreshBuiltinToolSchemas propagates code schema/description changes on the
// orchestration + platform tools into already-seeded rows ONCE per version flag —
// SeedTool is first-insert-only, so a new param (e.g. llm_profile de spawn_task) never
// reaches an old DB otherwise. Preserves each tool's agent binding + enabled flag.
// Bump the flag whenever these tools' schemas/descriptions change in code.
func (s *Server) refreshBuiltinToolSchemas() {
	const flag = "tool_schema_refresh_v7_list_facts_paging"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	tools := append(s.orchestrationTools(), s.platformTools()...)
	for _, t := range tools {
		schema, _ := json.Marshal(t.InputSchema())
		if err := s.m.pg.RefreshToolDefaults(t.Name(), t.Description(), schema); err != nil {
			log.Printf("[tools] refresh %s schema failed: %v", t.Name(), err)
		}
	}
	// Ao mesmo tempo, atualiza algumas ferramentas de agent integradas para o padrão do código:
	// - goal_met: a descrição semeada em bancos antigos traz o enganoso "encerrar esta rodada de planejamento", o que faria o planner tratá-lo como
	// meio de "encerrar uma rodada vazia", julgando a tarefa inteira concluída logo ao começar.
	// - insert_assets: novo parâmetro related (marca se o ativo é relevante para a tarefa atual, decide se entra na cobertura),
	// SeedTool só na primeira inserção, o schema já semeado em bancos antigos de outro modo não receberia este novo parâmetro.
	// - list_facts: passa a ser paginado, novos parâmetros limit/before/q; o schema vazio já semeado em bancos antigos de outro modo
	// apareceria na página de gestão de ferramentas como "sem parâmetros", e o modelo também não teria a descrição desses parâmetros.
	refreshBuiltin := map[string]bool{"goal_met": true, "insert_assets": true, "list_facts": true}
	for _, sd := range agent.BuiltinToolSeeds() {
		if !refreshBuiltin[sd.Key] {
			continue
		}
		schema, _ := json.Marshal(sd.Schema)
		if err := s.m.pg.RefreshToolDefaults(sd.Key, sd.Desc, schema); err != nil {
			log.Printf("[tools] refresh %s desc failed: %v", sd.Key, err)
		}
	}
	_ = s.m.pg.SetSetting(flag, "true")
	log.Printf("[tools] schema das ferramentas orchestration/platform atualizado para o padrão do código (uma única vez)")
}

// unbindGoalMetDefault removes goal_met's default "planner" binding ONCE (guarded by
// a settings flag), so existing DBs match the new default of NO agent. goal_met bypasses
// per-goal prove_goal to declare the whole task done — powerful/risky and redundant with
// the prove_goal→auto-complete path — so it ships unbound; users can re-bind it per agent
// in the UI. A user's own binding to another agent is untouched (we only strip planner).
func (s *Server) unbindGoalMetDefault() {
	const flag = "goal_met_unbind_default_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.RemoveAgentFromTool("planner", "goal_met"); err != nil {
		log.Printf("[tools] falha ao desvincular goal_met do planner: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// reseedGoalsPrompt atualiza o prompt do decompositor de metas goals para o [padrão atual do código] — porque o texto padrão ganhou
// o passo "extrair restrições operacionais (set_constraints) antes de decompor as metas", e SeedPromptIfEmpty só vale na primeira inserção, a version 1
// já existente em bancos antigos não recebe esse passo. Aqui, via gestão de versões, [acrescenta-se uma nova versão] e se troca para ela (ResetPromptToDefault),
// a versão antiga continua no histórico, se o usuário a tiver personalizado pode recuperá-la nos registros de versão. Guardado por flag em settings → só roda uma vez;
// se o padrão mudar de novo no futuro, é só fazer bump neste flag. Bancos novos não precisam de tratamento (SeedPromptIfEmpty já semeou o padrão mais recente).
func (s *Server) reseedGoalsPrompt() {
	const flag = "goals_prompt_constraint_step_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // tenta uma única vez, com sucesso ou não
	a, err := s.m.pg.GetAgentByKey("goals")
	if err != nil || a == nil {
		return // quando o banco novo ainda não criou a linha do agent, seedPrompts semeia direto o padrão mais recente, esta migração é desnecessária
	}
	tmpl := agent.BuiltinPromptSeeds()["goals"]
	if tmpl == "" {
		return
	}
	// Banco novo: seedPrompts já semeou o padrão mais recente → a versão atual já é igual ao padrão do código, não é preciso acrescentar versão duplicada.
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] falha ao reaplicar o novo padrão do prompt de goals: %v", err)
		return
	}
	log.Printf("[prompts] nova versão padrão acrescentada ao prompt de goals (adicionado o passo de extrair restrições operacionais, uma única vez)")
}

// reseedMainAgentPrompt atualiza o prompt do mainagent para o [padrão atual do código] — o texto padrão ganhou a orientação "depois que todas as
// metas forem atingidas, ao injetar intenção direta via add_intent, perguntar à pessoa se deve registrá-la como meta formal", e SeedPromptIfEmpty só vale na primeira
// inserção, versões já existentes em bancos antigos não a recebem. Via gestão de versões, [acrescenta-se uma nova versão] e se troca para ela (ResetPromptToDefault), a versão antiga
// continua no histórico, se o usuário a tiver personalizado pode recuperá-la nos registros de versão. Guardado por flag em settings → só roda uma vez. Bancos novos não precisam de tratamento
// (SeedPromptIfEmpty já semeou o padrão mais recente). Totalmente isomórfico a reseedGoalsPrompt.
func (s *Server) reseedMainAgentPrompt() {
	const flag = "mainagent_prompt_goalless_intent_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // tenta uma única vez, com sucesso ou não
	a, err := s.m.pg.GetAgentByKey("mainagent")
	if err != nil || a == nil {
		return // quando o banco novo ainda não criou a linha do agent, seedPrompts semeia direto o padrão mais recente, esta migração é desnecessária
	}
	tmpl := agent.BuiltinPromptSeeds()["mainagent"]
	if tmpl == "" {
		return
	}
	// Banco novo: seedPrompts já semeou o padrão mais recente → a versão atual já é igual ao padrão do código, não é preciso acrescentar versão duplicada.
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] falha ao reaplicar o novo padrão do prompt de mainagent: %v", err)
		return
	}
	log.Printf("[prompts] nova versão padrão acrescentada ao prompt de mainagent (adicionada a pergunta de criar meta após atingir a meta, uma única vez)")
}

// reseedPlannerPrompt atualiza o prompt do planner para o [padrão atual do código] — o texto padrão passou por uma refatoração enxuta, e rebaixou a "contenção" a
// apenas deduplicação, acrescentou "profundidade acima de cobertura", "piso rígido: meta não atingida e nenhuma intenção em execução obriga a produzir", e pôs um limite superior na revisão de conclusões negativas.
// Sempre que o padrão tiver mudança substancial, faz-se bump no flag abaixo (atualmente v2) para que bancos antigos existentes sejam atualizados mais uma vez. SeedPromptIfEmpty só vale na primeira inserção, versões já existentes em bancos antigos não a recebem, por isso usa-se a gestão de versões para
// [acrescentar uma nova versão] e trocar para ela (ResetPromptToDefault), a versão antiga continua no histórico, se o usuário a tiver personalizado pode recuperá-la nos registros de versão.
// Guardado por flag em settings → só roda uma vez. Bancos novos não precisam de tratamento (SeedPromptIfEmpty já semeou o padrão mais recente). Totalmente isomórfico a
// reseedGoalsPrompt.
func (s *Server) reseedPlannerPrompt() {
	const flag = "planner_prompt_compact_realistic_v2"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // tenta uma única vez, com sucesso ou não
	a, err := s.m.pg.GetAgentByKey("planner")
	if err != nil || a == nil {
		return // quando o banco novo ainda não criou a linha do agent, seedPrompts semeia direto o padrão mais recente, esta migração é desnecessária
	}
	tmpl := agent.BuiltinPromptSeeds()["planner"]
	if tmpl == "" {
		return
	}
	// Banco novo: seedPrompts já semeou o padrão mais recente → a versão atual já é igual ao padrão do código, não é preciso acrescentar versão duplicada.
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] falha ao reaplicar o novo padrão do prompt do planner: %v", err)
		return
	}
	log.Printf("[prompts] nova versão padrão acrescentada ao prompt do planner (refatoração enxuta + contenção rebaixada a deduplicação + profundidade primeiro + limite superior da revisão negativa, uma única vez)")
}

// reseedWorkerPrompt atualiza o prompt do worker para o [padrão atual do código] — no texto padrão, a seção record_fact removeu a frase inteira "conclusões
// negativas: escrever observação + forma de leitura tentativa", e desacoplou confidence (observed/inferred) de "se esgotou os meios desta intenção" (isso induz o planner a erro),
// e apertou a divisão do array facts em itens para a rara exceção de "totalmente independentes entre si, impossíveis de agrupar". Bump do flag para v3 para que bancos antigos existentes sejam atualizados mais uma vez.
// SeedPromptIfEmpty só vale na primeira inserção, versões já existentes em bancos antigos não a recebem, por isso usa-se a gestão de versões para [acrescentar uma nova versão] e trocar para ela, a versão antiga continua no histórico e pode ser recuperada.
// Guardado por flag em settings → só roda uma vez. Bancos novos não precisam de tratamento. Totalmente isomórfico a reseedGoalsPrompt.
func (s *Server) reseedWorkerPrompt() {
	const flag = "worker_prompt_compact_v4"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // tenta uma única vez, com sucesso ou não
	a, err := s.m.pg.GetAgentByKey("worker")
	if err != nil || a == nil {
		return // quando o banco novo ainda não criou a linha do agent, seedPrompts semeia direto o padrão mais recente, esta migração é desnecessária
	}
	tmpl := agent.BuiltinPromptSeeds()["worker"]
	if tmpl == "" {
		return
	}
	// Banco novo: seedPrompts já semeou o padrão mais recente → a versão atual já é igual ao padrão do código, não é preciso acrescentar versão duplicada.
	if cur, err := s.m.pg.CurrentPrompt(a.ID); err == nil && cur == tmpl {
		return
	}
	if _, err := s.m.pg.ResetPromptToDefault(a.ID, tmpl); err != nil {
		log.Printf("[prompts] falha ao reaplicar o novo padrão do prompt do worker: %v", err)
		return
	}
	log.Printf("[prompts] nova versão padrão acrescentada ao prompt do worker (a seção de consulta de contexto passa a se restringir a list_assets/list_findings, removendo list_facts/node_detail/asset_neighbors, uma única vez)")
}

// reporterToolCallMessage deve exigir, incondicionalmente, ler get_finding_traffic uma vez antes de escrever o relatório.
// Essa ferramenta é somente leitura e "não depende do interruptor de captura"; com a vinculação automática ligada ou não, lê a evidência vinculada manualmente. Se aqui
// se escrevesse "só ler se a vinculação automática estiver ativa", na configuração padrão desligada o reporter não passaria evidence_version,
// e SetFindingReportVersionByNodeID gravaria -1 pela semântica legacy, e os detalhes da vulnerabilidade e a exportação em Markdown
// ficariam para sempre com "evidência alterada, relatório a atualizar", sem nenhuma entrada na UI que consiga limpar isso.
const reporterToolCallMessage = "Acabou de ser registrada uma vulnerabilidade via report_finding. Leia, do JSON retornado, finding_id (ID do registro de vulnerabilidade independente) e finding_node_id (ID do nó de exploração)," +
	"primeiro use get_finding_traffic(finding_id) para ler a lista de evidências atual e sua version (lista vazia é normal, escreva o relatório como de costume);" +
	"se as diretrizes de execução habilitarem a vinculação automática, antes de ler verifique e associe o tráfego desta vulnerabilidade. Os detalhes do nó usam finding_node_id." +
	"Por fim chame update_finding_report(finding_id=finding_node_id, report, evidence_version=versão realmente lida) para salvar," +
	"evidence_version deve ser passada, senão o relatório será marcado permanentemente como a atualizar. Não misture os dois tipos de numeração."

// Mensagem de trigger da versão antiga (0.3.8 e anteriores). Só os registros ainda literalmente idênticos a ela são sobrescritos pela migração; os alterados pelo usuário ficam como estão.
const reporterToolCallMessageV1 = "上面刚有一个漏洞被 report_finding 登记。请从触发上下文里取出 finding_id" +
	"（工具返回 \"finding recorded: <id>\" 里的数字）与任务 id，按你的职责撰写该漏洞的详细报告，" +
	"最后调用 update_finding_report(finding_id, report) 保存。"

// upgradeReporterTriggerMessage atualiza para a nova versão as mensagens de trigger do reporter que em bancos antigos ainda são o texto padrão.
// seedReporterAgent é guardado por reporter_agent_seed_v1 e só grava o trigger ao criar o agent, então
// bancos atualizados não recebem o novo texto — o schema da ferramenta foi completado com evidence_version por seedFindingTrafficTools,
// mas nada diz ao reporter para usá-lo. Uma única vez, e só sobrescreve textos não alterados.
func (s *Server) upgradeReporterTriggerMessage() {
	const flag = "reporter_trigger_evidence_version_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // tenta uma única vez
	triggers, err := s.m.pg.ListTriggersFor("reporter")
	if err != nil {
		log.Printf("[reporter] falha ao ler triggers: %v", err)
		return
	}
	for _, t := range triggers {
		if !t.OnToolCall || t.ToolCallMessage != reporterToolCallMessageV1 {
			continue // alterado pelo usuário ou não é um trigger de finding, não mexe.
		}
		t.ToolCallMessage = reporterToolCallMessage
		if err := s.m.pg.UpdateTrigger(t); err != nil {
			log.Printf("[reporter] falha ao atualizar a mensagem do trigger: %v", err)
			return
		}
		log.Printf("[reporter] mensagem do trigger atualizada para ler e devolver evidence_version")
	}
}

// seedReporterAgent pré-instala um agent personalizado de "redação de relatório" (builtin=false, editável/excluível na UI):
// vincula update_finding_report + ferramentas de consulta de tarefa, e pendura um trigger "disparado quando report_finding é chamado" —
// cada vulnerabilidade registrada o aciona para escrever o relatório detalhado. Uma única vez (guardado por flag em settings): depois que o usuário o excluir, não é recriado.
// Dependência: as ferramentas de orchestration já foram gravadas por SeedTool acima desta função, por isso o vínculo funciona.
func (s *Server) seedReporterAgent() {
	const flag = "reporter_agent_seed_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	defer func() { _ = s.m.pg.SetSetting(flag, "true") }() // tenta uma única vez, com sucesso ou não

	if exist, _ := s.m.pg.GetAgentByKey("reporter"); exist != nil {
		return // a key já está ocupada (o usuário a criou à mão) — não sobrescreve
	}
	a, err := s.m.pg.CreateAgent("reporter", "Redação de relatório",
		"Redação de relatório detalhado de vulnerabilidade: disparado automaticamente ao encontrar uma vulnerabilidade, consulta a evidência e o processo de execução, escreve o relatório em Markdown e o grava de volta.")
	if err != nil {
		log.Printf("[reporter] falha ao criar o agent: %v", err)
		return
	}
	if err := s.m.pg.SeedPromptIfEmpty(a.ID, agent.ReporterDefaultPrompt); err != nil {
		log.Printf("[reporter] falha ao semear o prompt: %v", err)
	}
	// Política de execução do trigger: parallel + none — uma vulnerabilidade, um relatório; vários findings rodam em paralelo, cada um escrevendo o seu.
	// merge precisa ser none: senão (padrão all) uma leva de findings seria mesclada em uma única execução, e o paralelismo perderia o sentido.
	// maxParallel=5: no máximo 5 sessões de relatório ao mesmo tempo, evitando um pico de chamadas de LLM.
	if err := s.m.pg.SetAgentTriggerBehavior("reporter", "parallel", "none", 5); err != nil {
		log.Printf("[reporter] falha ao definir a política de execução do trigger: %v", err)
	}
	// Vincula as ferramentas de que ele precisa: escrever relatório + ler evidência/processo de execução/panorama.
	if err := s.m.pg.AddAgentToToolBinding("reporter", []string{
		"update_finding_report", "get_task_node_detail", "list_task_findings",
		"get_task_worker_trace", "list_task_worker_traces", "search_task_worker_traces",
		"get_task_graph",
	}); err != nil {
		log.Printf("[reporter] falha ao vincular ferramentas: %v", err)
	}
	// Trigger: disparado quando report_finding é chamado (a ferramenta retorna "finding recorded: <id>" com o finding_id,
	// o id da tarefa também está na mensagem do trigger).
	if _, err := s.m.pg.CreateTrigger(&db.AgentTrigger{
		AgentKey:        "reporter",
		Enabled:         true,
		OnToolCall:      true,
		ToolNames:       []string{"report_finding"},
		ToolCallMessage: reporterToolCallMessage,
	}); err != nil {
		log.Printf("[reporter] falha ao criar o trigger: %v", err)
	}
	log.Printf("[reporter] agent de \"redação de relatório\" + trigger de finding pré-instalados")
}

// seedAutoReportFindingBinding adds "auto" to report_finding's binding ONCE so
// conversation-context agents can call it without requiring an intent_id.
func (s *Server) seedAutoReportFindingBinding() {
	const flag = "auto_report_finding_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("auto", []string{"report_finding"}); err != nil {
		log.Printf("[auto] falha no vínculo padrão de report_finding: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedPlannerDefaultBindings adds "planner" to report_finding's binding ONCE
// (guarded by a settings flag), so existing DBs — whose report_finding row was
// seeded as worker-only — also let the planner record findings. Fresh DBs already
// get it via PlannerTools(); this only backfills without overriding a user unbind.
func (s *Server) seedPlannerDefaultBindings() {
	const flag = "planner_report_finding_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("planner", []string{"report_finding"}); err != nil {
		log.Printf("[planner] falha no vínculo padrão de report_finding: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedPlannerListAssetsBinding adds "planner" to list_assets's binding ONCE
// (guarded by a settings flag), so existing DBs — whose list_assets row was seeded
// as auto/pentest-only — also let the planner query the asset store by DSL. Fresh
// DBs already get it via PlannerTools(); this only backfills without overriding a
// user unbind.
func (s *Server) seedPlannerListAssetsBinding() {
	const flag = "planner_list_assets_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("planner", []string{"list_assets"}); err != nil {
		log.Printf("[planner] falha no vínculo padrão de list_assets: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedCompanyScopeRebind changes add_company_scope's default binding ONCE on
// existing DBs (guarded by a settings flag): the tool moves off worker and onto
// planner — defining a company's asset scope is a planning/main/auto concern, not
// something a worker does mid-exploration. Fresh DBs already get planner via
// PlannerTools() and lack worker via WorkerTools(); this only backfills old rows.
// One-shot + flag-guarded so a user who later re-binds worker isn't overridden.
func (s *Server) seedCompanyScopeRebind() {
	const flag = "company_scope_rebind_v1" // troca do vínculo padrão worker→planner
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("planner", []string{"add_company_scope"}); err != nil {
		log.Printf("[planner] falha no vínculo padrão de add_company_scope: %v", err)
		return
	}
	if err := s.m.pg.RemoveAgentFromTool("worker", "add_company_scope"); err != nil {
		log.Printf("[worker] falha ao desvincular add_company_scope: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedWorkerReadToolsUnbind strips the read-context tools off worker's default
// binding ONCE on existing DBs (guarded by a settings flag): a worker executes one
// intent and writes back — reading facts/companies and listing all workers' traces is
// a planning/main concern, not the executor's. Fresh DBs already lack these via
// WorkerTools(); this only backfills old rows without overriding a user who
// deliberately re-binds worker. Each RemoveAgentFromTool is per-tool +
// membership-guarded, so planner/mainagent bindings of the same tool are untouched.
//
// NOTE: search_all_worker_traces / get_worker_trace / node_detail are intentionally NOT
// unbound — worker owns them for cross-work look-back + node drill-down (see WorkerTools).
// They used to be in this list back when worker lacked them; seedWorkerReadbackRebind
// repairs DBs whose old run stripped them.
func (s *Server) seedWorkerReadToolsUnbind() {
	const flag = "worker_readtools_unbind_v1"
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	for _, k := range []string{
		"list_facts", "list_companies", "list_worker_traces",
	} {
		if err := s.m.pg.RemoveAgentFromTool("worker", k); err != nil {
			log.Printf("[worker] falha ao desvincular %s do worker: %v", k, err)
			return // em caso de erro não grava o flag, tenta de novo na próxima inicialização
		}
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedWorkerReadbackRebind re-binds the cross-work look-back / drill-down tools onto
// worker ONCE (guarded by a settings flag): an earlier seedWorkerReadToolsUnbind wrongly
// stripped search_all_worker_traces / get_worker_trace / node_detail from worker after
// they had been added to WorkerTools(), so any DB that ran that migration lost them.
// Fresh DBs already have them via WorkerTools() and this is a harmless no-op there.
// One-shot + flag-guarded so a user who later deliberately unbinds them isn't overridden.
func (s *Server) seedWorkerReadbackRebind() {
	const flag = "worker_readback_rebind_v2" // v2: acrescenta node_detail
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	if err := s.m.pg.AddAgentToToolBinding("worker", []string{
		"search_all_worker_traces", "get_worker_trace", "node_detail",
	}); err != nil {
		log.Printf("[worker] falha ao revincular ferramentas de retrospectiva/detalhes: %v", err)
		return // em caso de erro não grava o flag, tenta de novo na próxima inicialização
	}
	_ = s.m.pg.SetSetting(flag, "true")
}

// seedAutoDefaultBindings adds "auto" to the task-op + platform tools' bindings
// ONCE (guarded by a settings flag), so existing DBs whose tool rows were seeded
// before Auto existed still give Auto its default toolset — without re-adding it
// after a user deliberately unbinds.
func (s *Server) seedAutoDefaultBindings() {
	const flag = "auto_default_bindings_v3" // v3: substitui os nomes antigos das ferramentas de ativos, acrescenta insert_assets/add_company_scope
	if v, _, _ := s.m.pg.GetSetting(flag); v == "true" {
		return
	}
	keys := make([]string, 0, len(platformToolKeys)+12)
	for _, t := range s.orchestrationTools() {
		keys = append(keys, t.Name())
	}
	keys = append(keys, platformToolKeys...)
	// Ferramentas de ativos: o Auto, ao operar a plataforma, costuma precisar ver/registrar ativos e gerir o escopo de empresas.
	keys = append(keys, "insert_assets", "add_company_scope", "list_assets")
	if err := s.m.pg.AddAgentToToolBinding("auto", keys); err != nil {
		log.Printf("[auto] falha no vínculo padrão: %v", err)
		return
	}
	_ = s.m.pg.SetSetting(flag, "true")
}
