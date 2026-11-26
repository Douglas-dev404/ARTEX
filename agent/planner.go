package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// Planner is the event-driven LLM planner (docs §4.3): each time the asset or
// exploration graph changes (debounced), it reads the exploration route, queries
// assets, judges whether the task goal is met, and emits 0..N exploration intents
// into the frontier. It is the sole intent generator.
type Planner struct {
	findingRecorder   FindingRecorder
	prov              llm.Provider
	model             string
	tx                *transcript.Store                      // raw LLM conversation persistence (nil = off)
	window            int                                    // context window in tokens (for compaction)
	windowFn          func() int                             // optional dynamic task-chain minimum
	maxTurns          int                                    // max agent turns per run (0 = unlimited)
	killWork          func(intentID int64) error             // engine callback to terminate a running work (nil = off)
	steerWork         func(intentID int64, msg string) error // engine callback to steer a running work mid-run (nil = off)
	proxyAddr         string                                 // recording proxy for WebFetch (empty = direct)
	proxyCACert       string                                 // recording proxy's CA cert path (HTTPS verify)
	webSearch         WebSearchOpts                          // web_search tool backend selection (off by default)
	workDir           string                                 // shared work dir (surfaced in prompt as artifact-output target)
	injectConstraints func() bool                            // resolver: inject task operation constraints into system prompt? (nil = yes)
	nonStreamingFn    func() bool                            // resolver: use non-streaming (Complete) path? (nil = streaming)
	noaEnabledFn      func() bool                            // resolver: use experimental noa compaction? (nil = off)
	maxTokensFn       func() int                             // resolver: per-reply output cap (nil/0 = send no cap)
	compactor         *Compactor                             // cold-node compaction (§7); nil = disabled

	// todos keeps ONE plan-scratchpad per task (keyed by exploration id) so the
	// planner's multi-step plan survives across wake-ups — each Plan() is a fresh
	// session, but the shared store lets it record a serial exploit chain once and
	// dispatch it step-by-step over rounds instead of front-loading it in parallel.
	todoMu sync.Mutex
	todos  map[int64]*actool.TodoStore
}

func NewPlanner(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *Planner {
	return &Planner{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns, todos: map[int64]*actool.TodoStore{}}
}

func (p *Planner) SetCompactionWindowResolver(fn func() int) { p.windowFn = fn }

// SetCompactor wires the cold-node compactor (cold-digest §7). Called each
// planner wake-up to advance the round counter, maintain cold stamps, and
// (off the hot path) fold cold nodes into digests. nil = feature disabled.
func (p *Planner) SetCompactor(c *Compactor) { p.compactor = c }

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default).
func (p *Planner) SetNonStreaming(fn func() bool) { p.nonStreamingFn = fn }

func (p *Planner) nonStreaming() bool { return p.nonStreamingFn != nil && p.nonStreamingFn() }

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (p *Planner) SetNoaEnabled(fn func() bool) { p.noaEnabledFn = fn }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (p *Planner) SetMaxTokens(fn func() int) { p.maxTokensFn = fn }

func (p *Planner) maxTokens() int {
	if p.maxTokensFn == nil {
		return 0
	}
	return p.maxTokensFn()
}

func (p *Planner) compactionWindow() int {
	if p.windowFn != nil {
		return p.windowFn()
	}
	return p.window
}

// SetProxy points the planner's WebFetch at the recording proxy plus the CA cert
// it trusts to verify HTTPS through it (empty addr = direct).
func (p *Planner) SetProxy(addr, caCert string) { p.proxyAddr, p.proxyCACert = addr, caCert }

// SetWebSearch selects the web_search backend for the planner (off by default).
func (p *Planner) SetWebSearch(o WebSearchOpts) { p.webSearch = o }

// SetConstraintInject wires a resolver deciding whether this task's operation
// constraints get injected into the planner system prompt. Read per round so the
// settings toggle takes effect without rebuilding the agent. nil = inject (default).
func (p *Planner) SetConstraintInject(fn func() bool) { p.injectConstraints = fn }

// wantConstraints reports whether constraint injection is enabled (default yes).
func (p *Planner) wantConstraints() bool { return p.injectConstraints == nil || p.injectConstraints() }

// todoFor returns the task's persistent planning todo store, creating it on first
// use. Shared across all of this task's planner wake-ups.
func (p *Planner) todoFor(expID int64) *actool.TodoStore {
	p.todoMu.Lock()
	defer p.todoMu.Unlock()
	s := p.todos[expID]
	if s == nil {
		s = actool.NewTodoStore()
		p.todos[expID] = s
	}
	return s
}

// SetKillWork wires the engine's per-work terminate callback so the planner's
// kill_work tool can stop a single running worker.
func (p *Planner) SetKillWork(fn func(intentID int64) error) { p.killWork = fn }

// SetSteerWork wires the engine's per-work steering callback so the planner's
// steer_work tool can inject a mid-run course-correction into a running worker.
func (p *Planner) SetSteerWork(fn func(intentID int64, msg string) error) { p.steerWork = fn }

// renderPlannerTodos formats the persistent planning todo for injection into the
// wake-up prompt (empty when there are no todos yet — first wake-up).
func renderPlannerTodos(items []actool.Todo) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[Sua lista de pendências de planejamento (mantida entre despertares, escrita por você na rodada anterior)]:\n")
	for _, it := range items {
		mark := map[actool.TodoStatus]string{actool.TodoPending: "☐", actool.TodoInProgress: "▶", actool.TodoCompleted: "✔"}[it.Status]
		if mark == "" {
			mark = "☐"
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", mark, it.Content))
	}
	b.WriteString("Avance com base nisso: só despache intenções para o próximo passo cujos [passos pré-requisitos já foram concluídos / cujos facts dos quais depende já existem]; use TodoWrite para atualizar a lista (marque como completed os passos já satisfeitos por facts). Não despache de novo passos que já estão pending/in_progress na lista.")
	return b.String()
}

// TriggerEvent describes what concretely caused this planning round to fire, so
// the planner looks first at the actual change instead of re-scanning the whole
// overview. Kind:
//
//	"done"    — a worker finished intent IntentID (its output conclusion is fetched).
//	"finding" — a worker reported a finding on intent IntentID (Detail = resumo).
//	"goal"    — the human (via set_goals do agent principal) added one OR MORE goals in a
//	            single call (Goals = texto dos objetivos adicionados desta vez, 1+; set_goals suporta lote).
//	"goal_deleted" — the human deleted a goal from o gerenciamento de objetivos da visão geral (Detail = texto do objetivo excluído).
//	"goal_edited"  — the human edited a goal from o gerenciamento de objetivos da visão geral (OldGoal→NewGoal = texto).
//	"cancelled" — the human deleted intent IntentID (Detail = motivo da exclusão). The intent is
//	            stopped (not deleted) and the reason is attached to it as a fact.
type TriggerEvent struct {
	Kind     string
	IntentID int64
	Detail   string
	Summary  string   // exclusivo de Kind=="cancelled": resumo da intenção capturado antes da exclusão (após exclusão definitiva o nó não existe mais e não pode ser consultado)
	Goals    []string // exclusivo de Kind=="goal": texto dos objetivos adicionados por este set_goals (um ou vários)
	OldGoal  string   // exclusivo de Kind=="goal_edited": texto do objetivo antes da edição
	NewGoal  string   // exclusivo de Kind=="goal_edited": texto do objetivo depois da edição
	Hints    []string // exclusivo de Kind=="hint": texto dos hints adicionados por este add_hint (um ou vários)
}

// renderTriggers spells out the change(s) that fired this round: for a finished
// worker — which intent + its output conclusion; for a finding — which intent +
// what was found. Empty for time/heartbeat wakes. Reads the store (best-effort;
// a blank field never blocks the round).
func renderTriggers(ts *db.ExplorationStore, evs []TriggerEvent) string {
	if len(evs) == 0 || ts == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n[Mudanças reais que dispararam esta rodada (veja aqui primeiro, depois decida se deve complementar a direção)]:")
	for _, ev := range evs {
		switch ev.Kind {
		case "goal":
			if len(ev.Goals) == 1 {
				b.WriteString(fmt.Sprintf("\n- A pessoa (agent principal) adicionou um objetivo: %s — novo objetivo a ser alcançado; complemente a direção de exploração com base nele (se ainda não houver intenção correspondente).", ev.Goals[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- A pessoa (agent principal) adicionou %d objetivos: %s — todos são novos objetivos a serem alcançados; complemente, um a um, a direção de exploração dos objetivos que ainda não têm intenção correspondente.", len(ev.Goals), strings.Join(ev.Goals, "; ")))
			}
		case "hint":
			if len(ev.Hints) == 1 {
				b.WriteString(fmt.Sprintf("\n- A pessoa (agent principal) adicionou uma dica estratégica: %s — já foi anexada ao grafo de exploração; ajuste/complemente a direção de exploração com base nela (se ainda não houver intenção correspondente).", ev.Hints[0]))
			} else {
				b.WriteString(fmt.Sprintf("\n- A pessoa (agent principal) adicionou %d dicas estratégicas: %s — todas já foram anexadas ao grafo de exploração; ajuste/complemente, uma a uma, a direção de exploração com base nelas.", len(ev.Hints), strings.Join(ev.Hints, "; ")))
			}
		case "goal_deleted":
			b.WriteString(fmt.Sprintf("\n- A pessoa excluiu este objetivo: %s — o objetivo foi removido; reavalie com base nisso os objetivos/direções restantes (não é mais necessário despachar intenções para ele).", ev.Detail))
		case "goal_edited":
			b.WriteString(fmt.Sprintf("\n- A pessoa alterou o objetivo, de \"%s\" para \"%s\" — ajuste a direção de exploração conforme o novo objetivo (se a direção original não se aplica mais, pare de despachá-la).", ev.OldGoal, ev.NewGoal))
		case "finding":
			b.WriteString(fmt.Sprintf("\n- O worker da intenção #%d (%s) reportou um finding: %s", ev.IntentID, intentSummary(ts, ev.IntentID), ev.Detail))
		case "cancelled":
			// Para o conteúdo da intenção, dá-se preferência ao Summary capturado na exclusão (após exclusão definitiva o nó não existe mais e intentSummary não o encontra).
			sm := ev.Summary
			if sm == "" {
				sm = intentSummary(ts, ev.IntentID)
			}
			b.WriteString(fmt.Sprintf("\n- A intenção #%d foi excluída pelo usuário; o conteúdo da intenção era: %s; o motivo da exclusão foi: %s. Esta intenção foi excluída (não será mais executada); replaneje com base nisso.", ev.IntentID, sm, ev.Detail))
		default: // "done"
			b.WriteString(fmt.Sprintf("\n- O worker da intenção #%d (%s) terminou, conclusão da saída: %s", ev.IntentID, intentSummary(ts, ev.IntentID), workerOutput(ts, ev.IntentID)))
			if fids := factIDsYielded(ts, ev.IntentID); fids != "" {
				b.WriteString(fmt.Sprintf("; ids dos fatos novos produzidos por esta intenção: %s ", fids))
			}
		}
	}
	b.WriteString("\n(Para detalhes completos, consulte novamente com node_detail / get_worker_output / list_findings.)")
	return b.String()
}

// factIDsYielded lists the fact ids an intent produced this run as "#12, #15", so the
// planner can jump straight to the round's incremental facts. Empty (best-effort) when
// the intent yielded no facts or the lookup fails.
func factIDsYielded(ts *db.ExplorationStore, id int64) string {
	ids, err := ts.FactsYielded(id)
	if err != nil || len(ids) == 0 {
		return ""
	}
	parts := make([]string, len(ids))
	for i, fid := range ids {
		parts[i] = fmt.Sprintf("#%d", fid)
	}
	return strings.Join(parts, ", ")
}

// intentSummary reads an intent node's one-line summary (best-effort, "?" on miss).
func intentSummary(ts *db.ExplorationStore, id int64) string {
	n, err := ts.GetNode(id)
	if err != nil || n == nil {
		return "?"
	}
	var p map[string]any
	if json.Unmarshal(n.Payload, &p) == nil {
		if s, ok := p["summary"].(string); ok && s != "" {
			return s
		}
	}
	return "?"
}

// workerOutput returns the finished worker's conclusion for an intent — the last
// 'result' (else 'text') activity's full detail, truncated. Same source get_worker_output uses.
func workerOutput(ts *db.ExplorationStore, id int64) string {
	acts, _, err := ts.ActivityList(&id, 0, 1000)
	if err != nil {
		return "(falha ao obter a saída)"
	}
	var pick *db.Activity
	for i := range acts {
		if acts[i].Kind == "result" {
			pick = &acts[i]
		} else if acts[i].Kind == "text" && pick == nil {
			pick = &acts[i]
		}
	}
	if pick == nil {
		return "(este work ainda não tem registro de saída)"
	}
	out, _ := ts.ActivityDetail(pick.ID)
	if out == "" {
		out = pick.Summary
	}
	return truncOutput(out, 800)
}

// truncOutput caps a worker-output blob so the trigger context doesn't bloat the
// system prompt every round; full text is one get_worker_output call away.
func truncOutput(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + " … (truncado; texto completo em get_worker_output)"
}

// renderGraphOverview folds the pre-computed graph_overview snapshot into the
// wake-up prompt so the planner starts each round with the full situation in
// hand — saving the round-trip it would otherwise spend calling the tool. It is
// the exact same JSON graph_overview would return; deeper detail is still one
// tool call away (node_detail / list_facts / …).
func renderGraphOverview(data map[string]any) string {
	b, err := json.Marshal(data)
	if err != nil {
		return "" // fall back to the model calling graph_overview itself
	}
	return "\n\n[Panorama desta rodada (pré-carregado de graph_overview, equivalente ao retorno de você chamar essa ferramenta; para detalhes, chame node_detail/list_facts etc. sob demanda)]:\n" + string(b)
}

// plannerDefaultTmpl is the built-in EDITABLE body (seção [A]) of the planner prompt,
// seeded into agent_prompts. Goal is a {{.Goal}} template var; the convenção de saída de artefatos intermediários
// tail is code-owned (artifactSpec) and appended by plannerSystem after rendering.
const plannerDefaultTmpl = `Você é o "planejador" de um sistema autorizado de teste de intrusão de uma plataforma de segurança cibernética, e é despertado com frequência (desperta sempre que o grafo muda). Responsabilidades: ler o panorama → julgar os objetivos → **só quando houver de fato uma nova direção não coberta**, acrescentar intenções de exploração. Você é o planejador, não o executor: todos os produtos desta rodada só podem ser [gerar/explicitar intenções] ou [julgar objetivos], jamais fazer o trabalho dentro do plan.

Objetivo da tarefa: {{.Goal}}

**Quantas intenções esta rodada deve produzir (pense nisto primeiro)**:
- **Piso rígido (prioridade máxima)**: enquanto [o objetivo não foi atingido] e [não há nenhuma intenção open ou running no momento] (frontier_open=0 e running_intents vazio), esta rodada [deve] produzir pelo menos uma intenção que avance rumo ao objetivo — sem nenhum work em execução para esperar, e sem nenhuma direção na fila, produzir 0 intenção = tarefa paralisada; mesmo que as direções conhecidas estejam só em recent_done, é preciso, conforme o julgamento de done/exhausted/blocked abaixo, abrir uma nova ou continuar uma.
- Além do piso rígido, **produzir 0 intenção é um resultado normal, mas precisa de justificativa legítima** (não é o padrão de "despachar menos é mais seguro"): ① **já coberto** — as direções em que você pensou já são tratadas por intenções ainda open/running (gerar de novo, com outras palavras, uma intenção já existente é um erro grave); ② **aguardando dependência** — o próximo passo depende da produção de um work em execução que ainda não saiu (despachar à força faria o downstream ficar sem o pré-requisito e girar em vazio; deve-se esperar o grafo ser atualizado no próximo despertar para então despachar).
- Inversamente: havendo uma nova direção [não coberta e que não depende de work em execução], ou o objetivo não foi atingido e ainda existem superfícies não testadas dentro do escopo, deve-se despachar — não trate 0 intenção como padrão de preguiça.

**Fluxo de decisão a cada despertar**:

1. **O panorama completo já está anexado abaixo deste prompt** (é o retorno de graph_overview, não precisa chamá-lo de novo): task (título original + objetivo/nó raiz), contagem de ativos, goals + estado, intenções open/running/recent_done, sites_without_endpoints (sites sem endpoints, sugerem direções possivelmente a explorar), facts (quantidade de fatos de exploração, categoria distinta de vulnerabilidades), recent_facts ({id,summary,confidence?}).
   - **Escopo**: os nós de exploração (goals/intenções/facts/findings) contêm apenas esta tarefa; **o grafo de ativos é compartilhado globalmente** (uma só cópia para várias tarefas, a contagem de ativos é global dentro do escopo, não exclusiva desta tarefa) — ao aparecerem ativos não relacionados a esta tarefa, ignore-os.
   - **Linhagem**: cada intenção traz parents (upstream: derivada de quais fatos/intenções) e yields (downstream: quais fatos/descobertas produziu), e cada item de recent_facts traz from_intent; com isso, entenda "quais fatos vieram de qual direção e se é possível sintetizar uma nova direção".
   - **Observações negativas/duvidosas** (em recent_facts, como "porta fechada/não injetável") são observações do worker, não conclusões definitivas: antes de aceitá-las, use node_detail(id) para ver o evidence — só se o evidence for sólido, confidence=observed e os meios estiverem esgotados considere a direção temporariamente bloqueada; se o evidence estiver ausente, for só "parece que sim/explorado uma vez só", ou confidence=inferred, trate como [ainda não esclarecido] e, se estiver no escopo e nenhuma outra intenção cobrir, despache por padrão uma intenção de revisão para confirmar ou refutar (**a mesma direção negativa deve ser revisada no máximo uma vez**; se depois da revisão ainda for negativa e a evidência for razoável, respeite a conclusão e não despache mais).
   - **Só chame sob demanda quando precisar de detalhes mais profundos**: list_facts (paginado, mais recentes primeiro, padrão 20, filtrável por q, paginação por before, com total/has_more), list_findings (todas as vulnerabilidades), node_detail(id) (evidência/detalhes completos; a lista/recent_facts só dão resumo), list_assets (pull: busca por q, filtro por type/company_id/task_id, paginado, ou id/ids direto), asset_neighbors. Os ativos são compartilhados globalmente; não puxe tudo por padrão.

2. **Julgar os objetivos (responsabilidade central)**: o campo goals já contém os objetivos e seu estado; para objetivo ainda não atingido que foi comprovado por alguma descoberta/fato, chame prove_goal(goal_id, evidence_id, reason) para marcar como met. **Quando o que você marca for justamente o último objetivo não concluído, o sistema julga automaticamente a tarefa inteira como concluída** — o encerramento é conduzido apenas pelos prove_goal um a um, não existe outro meio de "concluir com um clique".
   - ⚠️ **Conferência de aceitação quantificada (proibido carimbar antecipadamente)**: quando o objetivo contém condições quantificáveis (cobertura de X%, obter N flags, obter certa permissão), antes de prove_goal [é obrigatório] conferir os valores medidos no graph_overview acima (coverage.pct, contagem de findings_total etc.): se não atingiu, [é proibido] prove_goal — despache intenções para cobrir a diferença; não marque met antecipadamente com a justificativa de "em grande parte atingido/o núcleo já foi dominado". Exemplo: se exige cobertura de 100% e o valor medido é coverage.pct=40% → não atingido, continue despachando intenções de teste complementar.

3. **(Opcional, só na abertura, muito leve) Compreensão por sondagem**: só quando o grafo quase não tem fact (recent_facts praticamente vazio, a tarefa mal começou) e, apenas com o panorama, não for possível especificar a intenção inicial, use Bash etc. para fazer sondagens mínimas e somente leitura no alvo (como 1–2 curl para ver a página inicial/fingerprint). **O único produto legítimo é uma descrição de intenção mais precisa** — jamais a descoberta/validação/exploração de vulnerabilidades, nem resultados de enumeração de endpoints/diretórios/parâmetros (isso é trabalho do worker, escreva como intenção e despache). Três limites rígidos:
   - Se o grafo já tem fact produzido por worker (facts>0 / recent_facts não vazio) → [é proibido] sondar por conta própria de novo; todo julgamento se baseia nos fatos existentes, e o produto desta rodada só pode ser "despachar nova intenção" ou "encerrar"; se quiser aprofundar uma pista → despache uma intenção para o worker investigar, não faça curl você mesmo.
   - Mesmo na abertura, sonde no máximo ≤3 vezes e pare, apenas para esclarecer a intenção inicial; ao perceber que está em "verificação aprofundada" e não em "definir rapidamente a direção" (enumerar endpoints/diretórios um a um, testar ids um a um, cadeias de decodificação, sondar repetidamente a mesma interface, qualquer teste de validação de injeção/escalada/vulnerabilidade — tudo isso é trabalho pesado do worker), pare imediatamente e escreva como intenção.
   - O que pode ser julgado a partir dos fatos/panorama existentes não precisa de sondagem alguma.

4. **Decidir quais novas direções complementar**: **a "contenção" aqui se refere apenas a [não repetir intenções já existentes], não a "despache o mínimo possível"** — com o objetivo não atingido, a pergunta padrão é "para se aproximar do objetivo, que abordagens mais profundas, mais agressivas e ainda não cobertas existem", e não "dá para encerrar?". A intenção é uma [direção de exploração aberta] (não um tipo fixo/menu); combinando fatos conhecidos, ativos e objetivo, julgue a direção por conta própria e compare uma a uma com open + running + recent_done:
   - Já coberto por open/running → não gere de novo (está em tratamento).
   - Já apareceu em recent_done → **primeiro veja o state da intenção (cada uma o traz) para distinguir como parou, e então decida**:
     · **done (terminou normalmente)**: já coberto → não redespache tal qual; se é beco sem saída, veja a conclusão dos facts que ela yields, e não o state; só quando aparecer um [novo mecanismo material] (novo fato/ativo/parâmetro/abordagem claramente diferente) redespache, e o summary deve deixar claro a diferença em relação à vez anterior; trocar a redação ou "tentar de novo, vai que dá" não conta, proibido repetir.
     · **exhausted (orçamento esgotado, cortado no meio da exploração, só gravou parte de volta) / blocked (falha do modelo ou da rede, praticamente não explorou)**: ambos não chegaram a um bom fim no meio do caminho, informação incompleta — primeiro use get_worker_trace / get_worker_output para ver até onde de fato chegou e onde travou, e então escolha entre: perto de um avanço mas cortado pelo orçamento → despache "continuar do progresso anterior"; falha puramente externa que impediu a execução (blocked costuma ser) → redespache diretamente a mesma direção; travando sempre no mesmo ponto → troque de abordagem/direção. A base é sempre o progresso real no trace, não o state em si.
   - Direção totalmente nova sem nenhuma intenção cobrindo → gere.
   - Todas as direções conhecidas estão cobertas por intenções ainda open/running → não gere, encerre direto (há work em execução/na fila, espere que avancem); mas se só resta cobertura de recent_done, sem mais open/running e com o objetivo não atingido → pelo piso rígido do topo é obrigatório abrir nova ou continuar.
   - **Profundidade acima de cobertura**: coverage é um piso/item de aceitação, não o objetivo da exploração em si; ao descobrir uma entrada de alto valor (que possa levar a RCE/escalada de privilégio/exfiltração de dados), priorize despachar intenções para [furar fundo] aquele caminho, em vez de espalhar para nivelar a cobertura, testando superficialmente ativo por ativo.
   - **Mantenha rotas diversas, não convirja cedo demais**: com o objetivo não atingido, se as intenções existentes estão todas espremidas na mesma rota/entrada e existe uma direção não coberta [essencialmente diferente] (outra superfície de entrada/outra classe de ativo/outra cadeia de exploração), priorize complementar essa direção divergente, em vez de acrescentar intenções sinônimas na mesma linha (olhe a diferença substancial, não a redação); se essa direção divergente já está coberta por intenções existentes, ainda assim não gere. O ideal é que 2–3 rotas de mecanismos diferentes coexistam (como "atacar pela cadeia de upload" e "atacar pelo bypass de autenticação"), e só concentrar recursos numa delas depois que ela entregar evidência de [aproximação do objetivo]. **Mas a diversidade sempre se subordina às [Restrições de operação] do topo**: superfícies de entrada/portas/hosts/operações excluídas pelas restrições, mesmo sendo essencialmente diferentes, jamais geram intenção.

   **Cadeia de exploração serial: despache por etapas, não divida em paralelo.** Cadeia serial fortemente dependente (①→②→③, o passo seguinte depende da produção real do anterior): não despache tudo em paralelo de uma vez (o downstream, sem o pré-requisito que ainda não existe, só repetiria/giraria em vazio); use TodoWrite para registrar a cadeia inteira como pendências (uma por passo), e nesta rodada despache apenas o passo cujo "pré-requisito já está satisfeito" (normalmente o primeiro); depois que ele produzir fact, no próximo despertar (a dica trará a lista de pendências) despache o próximo passo e marque como completed os já satisfeitos. "A mesma coisa" não deve ser dividida em duas ("confirmar o ponto de disparo" e "disparar o ponto de disparo" são o mesmo passo); só as dimensões [paralelas e independentes entre si] (como enumerar vários endpoints sem relação) usam múltiplas intenções em paralelo.

5. **Enviar**: use [uma única] chamada de add_intent para enviar em lote as novas direções selecionadas (array intents, no máximo 4 de maior valor; não chame várias vezes uma a uma):
   - **summary**: uma frase em linguagem natural descrevendo a direção (endereço completo do alvo de teste + o que fazer + por quê), sem usar categoria fixa; a deduplicação depende principalmente da comparação dele com as intenções existentes.
   - **asset_ids**: ids dos ativos-alvo que esta direção vai testar/atacar (passe sempre que possível, 0/1/vários, vindos de list_assets) — sempre que a direção girar em torno de ativos concretos (site/interface/parâmetro/host), passe obrigatoriamente, usado para deduplicação de cobertura e ligação à cadeia de ativos; se cruza vários ativos, passe todos; só reconhecimento global puro, sem ativo concreto, deixa vazio.
   - **parent_ids**: de quais nós upstream esta direção foi sintetizada (opcional, 0/1/vários) — se vários fatos combinados geram uma intenção, passe todos; se derivada de alguma intenção/descoberta upstream, passe também o id dela; direção totalmente nova de topo, deixe vazio.

Sem repetir, sem forçar; mas com o objetivo não atingido e havendo abordagens mais profundas e não cobertas, despache quando deve despachar. Conciso, focado, eficiente.`

func plannerSystem(goal, dataDir, workDir string) string {
	body := renderSystem("planner", plannerDefaultTmpl, PlannerVars{Goal: goal, DataDir: dataDir, Now: nowStr()})
	return body + artifactSpec(workDir)
}

// Plan runs one planning round. emit, if non-nil, receives the planner's execution
// steps (so users can see how it reads the situation and judges goals — the
// planner is the intent generator and was previously a black box). Returns whether
// the planner judged the goal met.
// triggers carries the concrete change(s) that fired this round — worker(s) done
// and/or finding(s) reported (may be several — the engine debounces a burst; empty
// for time/heartbeat wakes). They are spelled out at the top of the prompt so the
// planner looks first at the actual change (which intent, its output/finding).
func (p *Planner) Plan(ctx context.Context, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, goal string, triggers []TriggerEvent, emit func(db.Activity)) (met bool, reason string, err error) {
	// cold-digest §2.3/§7: advance this task's planner-round counter, maintain the
	// cold_since_round stamps, and (if a threshold is hit) kick off background
	// compaction. Synchronous part is cheap (a few queries); the LLM compaction
	// runs in a detached goroutine so it never adds latency to this round.
	p.compactor.OnPlannerRound(ctx, ts)
	tsx := NewToolSet(ts, "planner")
	tsx.SetFindingRecorder(p.findingRecorder)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetTaskID(taskID)
	tsx.SetCoverageEnabled(as == nil || as.CoverageEnabled(taskID))
	tsx.killWork = p.killWork   // enable kill_work tool (nil = unavailable)
	tsx.steerWork = p.steerWork // enable steer_work tool (nil = unavailable)
	if origin, _ := ts.OriginFactID(); origin > 0 {
		tsx.SetOwnerNode(origin) // planner-side anchors default to the task root (origin fact)
	}
	// Ferramentas de domínio + conjunto básico de ferramentas padrão (Read/Write/Edit/MultiEdit/LS/Glob/Grep/Bash)
	// Com a função de cobertura de ativos desativada, remove add_task_scope/list_untested_assets (não entram no prompt).
	base := append(tsx.DropCoverageTools(tsx.PlannerTools()), actool.DefaultTools()...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts)})
	tools, def, cleanup := AugmentTools(ctx, "planner", base)
	defer cleanup()
	// O panorama-chave (intenção recém-concluída + grafo completo pré-carregado) passa a ir na [entrada de user desta rodada] (ver input abaixo), o system
	// mantém apenas o corpo estático de planejamento. O move-out deixa o system estável a cada rodada, melhor para o cache; o custo é que, se uma única rodada ficar longa, o panorama pode
	// ser comprimido pela compaction (a rodada única do planner costuma ser curta, risco baixo). situational é concatenado ao input abaixo.
	situational := renderTriggers(ts, triggers) + renderGraphOverview(tsx.graphOverviewData())
	// Deadline de nível de tarefa / modo de fim de jogo (injetado via ctx, ver taskclock.go). Na rodada de fim de jogo, o prompt de encerramento por timeout da tarefa do
	// planner é concatenado, como [instrução de operação desta rodada], à entrada de user desta rodada (junto com situational), para que ele faça só o último
	// julgamento de objetivos, sem produzir novas intenções.
	tc := taskClockFrom(ctx)
	if tc.Final {
		situational += "\n\n[Encerramento final da tarefa (instrução especial desta rodada, sobrepõe o fluxo de planejamento regular acima)]:" + resolveTaskTimeoutWrapup("planner")
	}
	// Diretório de trabalho desta tarefa <workDir>/tasks/<taskID>, criado antes.
	taskDir := ensureRunDir(p.workDir, taskID, 0)
	ctx = intercept.WithReviewContext(ctx, taskDir, intercept.ReviewBackground{})
	sysBody := plannerSystem(goal, p.workDir, taskDir)
	if p.wantConstraints() {
		sysBody += constraintBlock(ts) // Restrições de operação (se houver) são injetadas no prompt do sistema, delimitando a fronteira de exploração
	}
	system, boundary := deferredSystem(sysBody, def)
	// O planner não tem orçamento próprio de tempo de relógio; havendo deadline, limita MaxDuration ao restante, para que a rodada de planejamento em execução entre no
	// encerramento quando a tarefa chega ao prazo (por timeout → prompt de timeout da tarefa, por passos → prompt per-run).
	maxDur, clamped := clampMaxDuration(tc.DeadlineUnix, 0)
	settle := wrapupSettlement("planner", nil)
	if tc.DeadlineUnix > 0 {
		settle = wrapupSettlementForTask("planner", nil, clamped)
	}
	opts := agentcore.Options{
		Provider:        p.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		EnableWebFetch:  true, // passa pelo proxy de registro para deixar rastro; carrega a CA do proxy para validar certificados HTTPS reassinados pelo MITM
		WebFetchProxy:   p.proxyAddr,
		WebFetchCACert:  p.proxyCACert,
		// Busca na web (opcional). ddgs não precisa de key; brave-free precisa de BraveKey; tavily precisa de TavilyKey.
		// WebSearchProxy é um proxy de saída independente (http/https/socks5), sem relação com o proxy MITM que registra o tráfego; vazio = conexão direta.
		EnableWebSearch:       p.webSearch.Enabled,
		WebSearchBackend:      p.webSearch.Backend,
		BraveSearchAPIKey:     p.webSearch.BraveKey,
		TavilySearchAPIKey:    p.webSearch.TavilyKey,
		DeepSeekSearchBaseURL: p.webSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  p.webSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   p.webSearch.DeepSeekModel,
		WebSearchProxy:        p.webSearch.Proxy,
		BashEnv:               proxyEnv(p.proxyAddr, p.proxyCACert), // Subcomandos do Bash passam pelo proxy por padrão + confiam na CA
		WorkingDir:            taskDir,                              // diretório de trabalho desta tarefa <workDir>/tasks/<taskID>
		ToolOutputDir:         cmdOutDir(taskDir),
		MaxTurns:              p.maxTurns, // 0 = unlimited (configurable in agent management)
		MaxDuration:           maxDur,     // 0=sem limite; havendo deadline = restante até o deadline
		Compaction:            compactionConfig(p.compactionWindow()),
		// Pendências de planejamento compartilhadas entre despertares: faz a cadeia serial persistir entre rodadas (a session é nova, o store não).
		Todos: p.todoFor(ts.ID()),
		// Ao atingir o orçamento de passos da [rodada] → o SDK roda o encerramento: consolida as conclusões já esclarecidas nesta rodada (add_intent do que deve ser despachado,
		// prove_goal do que pode ser comprovado, TodoWrite da cadeia serial), em vez de parar de planejar — o planner continuará sendo despertado repetidamente depois.
		// Quando clamped (limitado pelo deadline da tarefa) usa PromptByReason (ver wrapupSettlementForTask).
		Settlement:   settle,
		NonStreaming: p.nonStreaming(), // quando este profile escolhe não streaming, usa Provider.Complete
		MaxTokens:    p.maxTokens(),    // 0 = não envia limite; o padrão do servidor decide
	}
	if p.tx != nil { // persist raw LLM conversation; one accumulating file per task's planner
		opts.Transcript = p.tx
		opts.SessionID = fmt.Sprintf("exp%d-planner", ts.ID())
	}
	// Recurso experimental: quando ativado, o noa assume a compressão de contexto (arquivos em <workDir>/noa/<SessionID>, persistentes).
	noaSession := fmt.Sprintf("exp%d-planner", ts.ID())
	enableNoa(&opts, p.noaEnabledFn, p.workDir, noaSession, noaWarn(noaSession))
	// O panorama (intenção recém-concluída + grafo completo) agora é concatenado à entrada de user desta rodada (ver input abaixo). No user há também a
	// instrução + pendências entre despertares (todo é o bloco de anotações de planejamento do próprio modelo, regenerável; basta colocá-lo no user).
	// A abertura se divide em dois casos conforme "esta rodada tem ou não mudança concreta": com mudança → aponta para o bloco [mudanças reais] abaixo; sem mudança
	// (inspeção periódica por heartbeat / hint / retomada etc.) → não alegar falsamente que "o grafo mudou", e sim sugerir revisar de passagem as intenções em execução.
	lead := "Houve uma mudança concreta (veja [Mudanças reais que dispararam esta rodada] abaixo); planeje o próximo passo com base nela:"
	if len(triggers) == 0 {
		lead = "Esta rodada é um despertar por **inspeção periódica (heartbeat no prazo) / sem sinal de mudança concreta** — o grafo não necessariamente tem mudanças novas. Aproveite para revisar as intenções em execução: as que ficaram muito tempo sem progresso ou saíram do rumo, corrija com steer_work; as de direção totalmente errada, interrompa com kill_work para estancar a perda; depois julgue os objetivos e decida se complementa a direção:"
		// Ao despertar por heartbeat/sem mudança, se o grafo inteiro não tem mais nenhuma intenção open ou running → a exploração está paralisada (nenhum worker em execução,
		// nem direção na fila). Avise explicitamente o planner e force-o a produzir nesta rodada novas direções, em vez de só revisar as intenções em execução e girar uma rodada em vazio.
		if active, err := ts.HasActiveIntent(); err == nil && !active {
			lead = "Esta rodada é um despertar por **inspeção periódica (heartbeat no prazo)**, e atualmente **não há nenhuma intenção open ou running** — nenhum worker em execução e nenhuma direção na fila, a exploração está paralisada. Você **deve** produzir nesta rodada uma ou mais novas intenções que avancem rumo ao objetivo e **não dupliquem** as intenções já existentes no grafo (não é permitido produzir 0 intenção); primeiro julgue, com base no panorama abaixo, se o objetivo já foi atingido e, se não, complemente imediatamente as direções:"
		}
	}
	input := lead + situational + "\n\nCom base no panorama acima, julgue os objetivos. Quando o objetivo foi [realmente atingido] (obteve o resultado do objetivo/confirmou a vulnerabilidade-alvo), marque um a um com prove_goal. **Piso rígido: enquanto o objetivo não foi atingido e não há nenhuma intenção open ou running no momento (frontier_open=0 e running_intents vazio), esta rodada deve produzir pelo menos uma intenção que avance rumo ao objetivo — sem work em execução para esperar nem direção na fila, produzir 0 intenção = tarefa paralisada. Somente quando já há intenções open/running avançando, ou o objetivo foi atingido, esta rodada pode não produzir novas intenções.**" +
		renderPlannerTodos(opts.Todos.List())
	// O MaxDuration agora interrompe as ferramentas em execução quando o tempo de relógio chega ao prazo e entra no encerramento ali mesmo (em ctx vivo); uma rodada única travada não
	// contorna mais o encerramento, sem necessidade de ctx rígido externo de salvaguarda. O ctx só carrega pause / kill / shutdown.
	_, _, err = captureRun(ctx, opts, input,
		func(r db.Activity) {
			if emit != nil {
				r.Worker = "planner" // planner activity has no intent_id (it generates them)
				emit(r)
			}
		})
	return tsx.GoalMet, tsx.Reason, err
}
