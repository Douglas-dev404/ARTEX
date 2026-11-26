package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
	acperm "github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
)

// compactIntents distills intents to {id, summary, state, asset_ids, parents,
// yields} so the planner sees both the direction and its LINEAGE — parents (the
// upstream nodes it derived from: facts/intents/findings) and yields (the facts/
// findings it produced) — without pulling full payloads. parentsOf/yieldsOf are
// built from the exploration edges in graph_overview.
func compactIntents(ns []*db.Node, parentsOf, yieldsOf map[int64][]int64) []map[string]any {
	out := make([]map[string]any, 0, len(ns))
	for _, n := range ns {
		var p map[string]any
		_ = json.Unmarshal(n.Payload, &p)
		m := map[string]any{"id": n.ID, "summary": p["summary"], "state": n.State}
		if n.Inherited {
			m["source_task_id"] = n.SourceTaskID
			m["inherited"] = true
		}
		// asset_ids is the structured "which assets this direction covers" signal for
		// dedup; fall back to legacy payload keys (target_ids plural, then target_id
		// single) so intents stored before the rename still surface their anchors.
		if tg, ok := p["asset_ids"]; ok && tg != nil {
			m["asset_ids"] = tg
		} else if tg, ok := p["target_ids"]; ok && tg != nil {
			m["asset_ids"] = tg
		} else if tg, ok := p["target_id"]; ok && tg != nil && tg != "" {
			m["asset_ids"] = []any{tg}
		}
		if ps := parentsOf[n.ID]; len(ps) > 0 {
			m["parents"] = ps // upstream: de quais nós esta intenção deriva (vários fatos podem juntos produzir uma intenção)
		}
		if ys := yieldsOf[n.ID]; len(ys) > 0 {
			m["yields"] = ys // downstream: quais fatos/descobertas esta intenção produziu
		}
		out = append(out, m)
	}
	return out
}

// ToolSet exposes the PG-backed dual graph (asset + exploration) to an LLM agent.
// One ToolSet is created per planner/worker run; per-run signals live here.
type ToolSet struct {
	findingRecorder FindingRecorder
	as              *db.AssetStore   // asset store (optional; nil = asset tools not available)
	cs              *db.CompanyStore // company store (optional)
	ts              *db.ExplorationStore
	worker          string
	taskID          int64 // PG tasks.id; 0 when unknown (tests / orchestrator cross-task reads)
	// coverageDisabled mirrors tasks.coverage_enabled=false. Stored inverted so the
	// zero value (all existing ToolSet constructions) means ENABLED — matching the
	// DB default (true). When true: graphOverviewData drops the coverage block, the
	// auto-scope hook (insertAssets) is skipped, and add_task_scope/list_untested_assets
	// are filtered out of the agent's tool list. The scope field stays regardless.
	coverageDisabled bool
	// ownerNode is the exploration node that writes attach to: assets this run
	// touches get anchored to it as lineage/provenance (NOT visibility — the asset
	// graph is global and shared). Worker = its claimed intent; planner = begin root.
	ownerNode int64
	GoalMet   bool
	Reason    string
	writes    WriteCounts
	// killWork, if set, terminates a running work by intent id (engine callback,
	// wired by the planner). nil = the kill_work tool reports unavailable.
	killWork func(intentID int64) error
	// steerWork, if set, queues a mid-run course-correction for the work running an
	// intent id (engine callback, wired by the planner): the worker injects it before
	// its next tool call and re-plans, without being killed. nil = tool unavailable.
	steerWork func(intentID int64, msg string) error
	// enrich, if set, receives async auto-completion triggers (DNS resolve for a
	// domain, HTTP probe for a site). nil = no engine enrichment.
	enrich EnrichTrigger
	// notify, if set, wakes the task's planner after a graph change that should be
	// re-planned promptly (currently: a new hint). nil = no wake (the hint is still
	// stored and read on the next round triggered by other events). debounced.
	notify func()
	// notifyFinding, if set, wakes the task's planner when this run reports a finding,
	// carrying (intentID, summary) so the round can spell out which intent found what.
	// Wired for workers; nil elsewhere → falls back to notify (bare wake).
	notifyFinding func(intentID int64, summary string)
	// resumeTask, if set, revives the task after a graph change that should make a
	// stopped task run again (currently: set_goals adds a goal). It flips a terminal/
	// paused task back to running and (re)starts the engine loops — a plain notify()
	// can't, because the planner's terminal gate swallows wakes. Wired ONLY for the
	// main agent (human steering); nil for the goals decomposer and workers.
	resumeTask func()
	// notifyGoal, if set, wakes the planner AND records ONE "a pessoa adicionou N objetivos: ..." trigger
	// for a whole set_goals call (batch-aware — one call, one trigger, not one per goal)
	// so the next round spells out the added goals (instead of the planner having to
	// spot new open goals in the overview). Wired ONLY for the main agent; nil for the
	// goals decomposer (round-0 has no running planner to inform) and workers → those
	// fall back to the bare notify.
	notifyGoal func(texts []string)
	// notifyHint, if set, wakes the planner AND records ONE "a pessoa adicionou N dicas estratégicas: ..."
	// trigger for a whole add_hint call (batch-aware — one call, one trigger) so the next
	// round is told the round was fired by a new hint and spells the hint out, instead of
	// the planner having to spot it folded into the graph overview. Wired for the main
	// agent + cross-task orchestration; nil elsewhere → falls back to the bare notify.
	notifyHint func(texts []string)
}

// SetNotifyGoal wires the goal-add trigger callback (see ToolSet.notifyGoal). Set only
// by the main-agent chat, so runtime-added goals are announced to the planner by name.
func (t *ToolSet) SetNotifyGoal(fn func([]string)) { t.notifyGoal = fn }

// SetNotifyHint wires the hint-add trigger callback (see ToolSet.notifyHint). Set by
// the main-agent chat and cross-task orchestration, so a runtime-added hint fires a
// planner round announced by name instead of a bare wake.
func (t *ToolSet) SetNotifyHint(fn func([]string)) { t.notifyHint = fn }

// SetResumeTask wires the task-revive callback (see ToolSet.resumeTask). Set only by
// the main-agent chat, so runtime-added goals can pull a finished task back to running.
func (t *ToolSet) SetResumeTask(fn func()) { t.resumeTask = fn }

// SetNotify wires the planner-wake callback (see ToolSet.notify). Set by callers
// that hold the task handle (main-agent chat, cross-task orchestration).
func (t *ToolSet) SetNotify(fn func()) { t.notify = fn }

// SetNotifyFinding wires the finding-wake callback (see ToolSet.notifyFinding).
func (t *ToolSet) SetNotifyFinding(fn func(int64, string)) { t.notifyFinding = fn }

// EnrichTrigger is the enrichment engine seen from the tool layer (see package
// enrich). Kept as an interface here to avoid coupling agent → enrich.
type EnrichTrigger interface {
	ResolveDomain(id int64, host string)
	ProbeSite(id int64, url string)
}

// WriteCounts breaks down what a worker persisted this run, by node kind, so the
// engine can log an accurate "wrote back" summary instead of lumping assets and
// findings under "facts" (record_fact → Facts, insert_assets → Assets,
// report_finding → Findings; each element of a batch counts once).
type WriteCounts struct {
	Facts    int
	Assets   int
	Findings int
}

// Total is every node persisted this run, regardless of kind — the
// "explored but persisted nothing" signal (Total == 0).
func (w WriteCounts) Total() int { return w.Facts + w.Assets + w.Findings }

// String renders the per-kind breakdown for logs, e.g. "fatos1 ativos25 vulnerabilidades0".
func (w WriteCounts) String() string {
	return fmt.Sprintf("fatos%d ativos%d vulnerabilidades%d", w.Facts, w.Assets, w.Findings)
}

// Writes reports what this run wrote back, split by node kind (so the engine can
// tell "explored but persisted nothing" apart from a completed intent, and log an
// honest breakdown instead of calling assets/findings "facts").
func (t *ToolSet) Writes() WriteCounts { return t.writes }

func NewToolSet(ts *db.ExplorationStore, worker string) *ToolSet {
	return &ToolSet{ts: ts, worker: worker}
}

// SetTaskID sets the PG task id on this ToolSet so that report_finding can
// dual-write to the standalone findings table (which survives task deletion).
func (t *ToolSet) SetTaskID(id int64) { t.taskID = id }

// SetCoverageEnabled records whether this task has the asset-coverage feature on
// (default enabled). Passing false makes graphOverviewData omit the coverage block
// and DropCoverageTools filter the two coverage-only tools out of the agent's tool
// list. It does NOT stop scope accumulation: insertAssets' auto-scope hook runs
// either way, because task_scope is the task's range boundary (the filter basis for
// asset queries), not merely a coverage denominator.
func (t *ToolSet) SetCoverageEnabled(enabled bool) { t.coverageDisabled = !enabled }

// CoverageDisabled reports whether the coverage feature is off for this task.
func (t *ToolSet) CoverageDisabled() bool { return t.coverageDisabled }

// coverageOnlyTools are the LLM tools that only make sense when asset coverage is
// on. When the feature is off they are filtered out of the agent's tool list so
// they neither pollute the prompt nor let the model build a disabled denominator.
// add_task_scope is deliberately NOT here: task_scope is the task's range boundary
// (the filter basis for asset queries), not merely a coverage denominator, so the
// agents that own definição de escopo keep it either way — in lockstep with insertAssets'
// auto-scope hook, which also runs regardless of the switch.
var coverageOnlyTools = map[string]bool{"list_untested_assets": true}

// DropCoverageTools returns tools with the coverage-only ones removed when this
// task has the feature disabled; otherwise it returns tools unchanged.
func (t *ToolSet) DropCoverageTools(tools []actool.CoreTool) []actool.CoreTool {
	if !t.coverageDisabled {
		return tools
	}
	out := tools[:0:0]
	for _, tool := range tools {
		if coverageOnlyTools[tool.Name()] {
			continue
		}
		out = append(out, tool)
	}
	return out
}

// Cross-task reuse: exported accessors returning the per-task tool logic bound to
// THIS ToolSet's store. Host-side orchestration tools build a ToolSet for an
// arbitrary task, then Call these — so cross-task reads/hint reuse the exact
// same logic as the in-task tools. (readTool ignores ToolContext, so Call(…,nil)
// is safe; add_hint is a writeTool but also doesn't deref the context here.)
func (t *ToolSet) GraphOverviewTool() actool.CoreTool      { return t.graphOverview() }
func (t *ToolSet) ListFindingsTool() actool.CoreTool       { return t.listFindings() }
func (t *ToolSet) GetWorkerTraceTool() actool.CoreTool     { return t.getWorkerTrace() }
func (t *ToolSet) ListWorkerTracesTool() actool.CoreTool   { return t.listWorkerTraces() }
func (t *ToolSet) SearchWorkerTracesTool() actool.CoreTool { return t.searchAllWorkerTraces() }
func (t *ToolSet) NodeDetailTool() actool.CoreTool         { return t.nodeDetail() }
func (t *ToolSet) AddHintTool() actool.CoreTool            { return t.addHint() }

// SetEnrich wires the async enrichment engine (DNS/HTTP auto-completion).
func (t *ToolSet) SetEnrich(e EnrichTrigger) { t.enrich = e }

// SetOwnerNode sets the exploration node that writes anchor to (worker: its
// intent node; planner/main: the begin root). Assets created/referenced while
// ownerNode is set are anchored to it as lineage (not visibility).
func (t *ToolSet) SetOwnerNode(id int64) { t.ownerNode = id }

// anchorOwner records a lineage edge from this run's owner node to an asset
// (no-op if unset). Provenance only — the asset graph is global and shared, so
// this no longer affects which assets a task can read.
func (t *ToolSet) anchorOwner(assetID int64) {
	if t.ts != nil && t.ownerNode > 0 && assetID > 0 {
		_ = t.ts.Anchor(t.ownerNode, assetID)
	}
}

// pid parses an id that may arrive as a JSON number or string ("" / 0 → 0).
func pid(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		return v
	}
	return 0
}

// pidList parses a list of ids (number|string), dropping zeros/invalids.
func pidList(raw []json.RawMessage) []int64 {
	var out []int64
	for _, r := range raw {
		if v := pid(r); v > 0 {
			out = append(out, v)
		}
	}
	return out
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		m["required"] = required
	}
	return m
}
func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func intp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func idp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func readTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: name, Description: desc, Schema: schema,
		ReadOnly:    func(json.RawMessage) bool { return true },
		Concurrent:  func(json.RawMessage) bool { return true },
		Permissions: func(context.Context, json.RawMessage, acperm.Context) acperm.Decision { return acperm.Allowed() },
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

func writeTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return actool.Build(actool.Spec{
		Name: name, Description: desc, Schema: schema,
		Permissions: func(context.Context, json.RawMessage, acperm.Context) acperm.Decision { return acperm.Allowed() },
		Run: func(ctx context.Context, in json.RawMessage, _ *actool.ToolContext) (actool.Result, error) {
			return run(ctx, in)
		},
	})
}

// readExpTool / writeExpTool build a domain tool whose handler dereferences the
// task-bound ExplorationStore. Two ToolSets carry a nil store: the catalog's
// seed-only shell (never called) and the server-level one behind buildDomainReg,
// which the tools table can bind to ANY agent — including ones that never run
// inside a task (auto/pentest/reporter/agent personalizado/pergunta paralela). Refusing there
// keeps a mis-bound tool a bad tool call; without the guard it was a nil deref,
// and tool handlers run on the harness's own goroutine, so the panic is out of
// reach of every recover() in the server and kills the whole process.
func (t *ToolSet) readExpTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return readTool(name, desc, schema, t.needExploration(name, run))
}

func (t *ToolSet) writeExpTool(name, desc string, schema map[string]any, run func(context.Context, json.RawMessage) (actool.Result, error)) actool.CoreTool {
	return writeTool(name, desc, schema, t.needExploration(name, run))
}

// needExploration wraps a handler so it only runs with an exploration store.
// Tools that degrade more usefully than "unavailable" (report_finding points at
// add_task_hint, set_goals/set_constraints at the task itself) keep their own
// bespoke guard instead.
func (t *ToolSet) needExploration(name string, run func(context.Context, json.RawMessage) (actool.Result, error)) func(context.Context, json.RawMessage) (actool.Result, error) {
	return func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
		if t.ts == nil {
			return actool.Errorf(name + " requer contexto da tarefa (grafo de exploração): o agent atual não está sendo executado dentro de uma tarefa, não consegue obter o grafo de exploração da tarefa, e esta ferramenta não está disponível. Use-a dentro de uma tarefa, ou use as ferramentas de leitura entre tarefas que recebem task_id (get_task_node_detail / list_task_findings / get_task_graph etc.)."), nil
		}
		return run(ctx, in)
	}
}

func jsonResult(v any) (actool.Result, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	return actool.Text(string(b)), nil
}

// --- read tools (planner + worker) ---

func (t *ToolSet) graphOverview() actool.CoreTool {
	return t.readExpTool("graph_overview",
		"(Grafo da cadeia de exploração) Resumo destilado do panorama de exploração: contagem de ativos, sites sem interfaces, frontier, descobertas, hints (dicas estratégicas de humanos/agent principal, que devem ser incorporadas ao gerar intenções). Ao planejar, chame-o primeiro.",
		obj(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			return jsonResult(t.graphOverviewData())
		})
}

// graphOverviewData computes the distilled situational snapshot shared by the
// graph_overview tool and the planner's wake-up prompt (which pre-injects it so
// the model needn't spend a turn calling the tool — every plan round starts with
// an empty context and always needs this first).
func (t *ToolSet) graphOverviewData() map[string]any {
	out := map[string]any{}
	// goals summary folded in so the planner needn't call list_goals each round.
	goals, _ := t.ts.ListByKind(db.KindGoal, 100)
	gsum := make([]map[string]any, 0, len(goals))
	for _, g := range goals {
		var p map[string]any
		_ = json.Unmarshal(g.Payload, &p)
		gsum = append(gsum, map[string]any{"id": g.ID, "state": g.State, "text": p["text"]})
	}
	out["goals"] = gsum
	// hints: dicas estratégicas que humanos/agent principal penduram no grafo via add_hint; folded in so the
	// planner reads them every round when generating intents (caso contrário seria só escrita, sem leitura).
	hints, _ := t.ts.ListByKind(db.KindHint, 50)
	hsum := make([]map[string]any, 0, len(hints))
	for _, h := range hints {
		var p map[string]any
		_ = json.Unmarshal(h.Payload, &p)
		hint := map[string]any{"id": h.ID, "state": h.State, "text": p["text"]}
		if findingTrafficBindingEnabled() && p["traffic_refs"] != nil {
			hint["traffic_refs"] = p["traffic_refs"]
		}
		hsum = append(hsum, hint)
	}
	out["hints"] = hsum
	// lineage from the exploration edges: an intent's parents (what it
	// derived_from — possibly several facts combined) and its yields (the
	// facts/findings it produced). factFrom maps a fact → the intent that
	// produced it. This is the relationship layer the flat lists lacked.
	edges, _ := t.ts.Edges(5000)
	parentsOf := map[int64][]int64{}
	yieldsOf := map[int64][]int64{}
	factFrom := map[int64]int64{}
	for _, e := range edges {
		switch e.Rel {
		case db.RelDerivedFrom, db.RelSpawns: // upstream: derived_from (fact/finding/intent→intent) or spawns (origin fact→goal, legacy begin→intent)
			parentsOf[e.To] = append(parentsOf[e.To], e.From)
		case db.RelYields: // intent --yields--> fact/finding
			yieldsOf[e.From] = append(yieldsOf[e.From], e.To)
			factFrom[e.To] = e.From
		}
	}
	// cold-digest §6: members folded into an active digest are shown via cold_digests
	// (below), not the flat recent_* lists. `covered` maps member id → its digest id.
	// §6 render-time revival check: a covered member that has become hot again (a new
	// intent derived from it) must reappear this round — so `hidden` folds a member out
	// only when it is covered AND still cold.
	covered, _ := t.ts.CoveredMembers()
	// Render-time hot set (ancestor of a live intent / fact under a live intent).
	// §6 revival check: a covered member that revived (now hot) must NOT stay folded
	// — hidden() only folds a member out when it is covered AND still cold. Computed
	// every round (cheap for real graph sizes); nil map degrades safely.
	var hotAtRender map[int64]bool
	if cg, _, err := loadColdGraph(t.ts); err == nil {
		hotAtRender = cg.hotSet()
	}
	hidden := func(id int64) bool { _, c := covered[id]; return c && !hotAtRender[id] }
	const openIntentsCap = 30
	fr, _ := t.ts.Frontier(openIntentsCap) // priority DESC, id ASC — as N de maior prioridade; o total real está em frontier_open
	out["open_intents"] = compactIntents(fr, parentsOf, yieldsOf)
	all, _ := t.ts.ListByKind(db.KindIntent, 300)
	var running, recentDone []*db.Node
	for _, n := range all {
		switch n.State {
		case "running":
			running = append(running, n)
		case "done", "blocked", "exhausted":
			if hidden(n.ID) {
				continue // in a cold_digest and still cold — shown via cold_digests (§6.2)
			}
			recentDone = append(recentDone, n) // mais recentes primeiro (all em ordem decrescente de id); os dobrados já foram removidos, e na saída corta-se nas N mais recentes
		}
	}
	out["running_intents"] = compactIntents(running, parentsOf, yieldsOf)
	// done_intents_total: total de intenções encerradas (done/blocked/exhausted), com nome
	// paralelo a recent_done_intents — esta é apenas a visão truncada da janela mais recente. As duas chaves lado a lado se autodescrevem: "o que se vê é N do total",
	// para que o planner, ao deduplicar, não tome "não exibido" por "nunca despachado", sem precisar explicar à parte no prompt.
	if dt, err := t.ts.CountFinishedIntents(); err == nil {
		out["done_intents_total"] = dt
	}
	// frontier_open: total real de intenções abertas (open_intents é apenas a visão truncada das N de maior prioridade).
	if fo, err := t.ts.CountOpenIntents(); err == nil {
		out["frontier_open"] = fo
	} else {
		out["frontier_open"] = len(fr)
	}
	// findings (confirmed vulns) and facts (worker exploration results) are
	// now distinct node kinds. recent_facts surfaces fact summaries (esp.
	// negative results) so the planner sees them in one call; full content
	// via node_detail(id).
	vulnNodes, _ := t.ts.ListByKind(db.KindFinding, 1000)
	factNodes, _ := t.ts.ListByKind(db.KindFact, 1000) // newest first
	out["findings_total"] = len(vulnNodes)             // total de vulnerabilidades confirmadas (a avaliação do objetivo olha para ele); detalhes em finding_list (janela mais recente)
	out["facts"] = len(factNodes)                      // quantidade de fatos/conclusões de exploração (incluindo conclusões negativas)
	// findings são o produto de maior valor da tarefa → o panorama traz a janela mais recente (≤10, vulnNodes já em ordem decrescente de id, isto é, mais recentes primeiro),
	// para que o planner, ao julgar o objetivo a cada rodada, veja de relance as vulnerabilidades confirmadas mais recentes; para o total/mais antigas use list_findings.
	// Cada item mantém só {id, summary, from_intent?}: from_intent é a intenção que produziu esta vulnerabilidade.
	// evidence/assets/vulnclass/severity/state etc. continuam obteníveis com list_findings / node_detail(id).
	const findingListCap = 10
	findingList := make([]map[string]any, 0, findingListCap)
	for _, n := range vulnNodes {
		if len(findingList) >= findingListCap {
			break
		}
		var fp map[string]any
		_ = json.Unmarshal(n.Payload, &fp)
		m := map[string]any{"id": n.ID, "summary": fp["summary"]}
		if from := factFrom[n.ID]; from > 0 {
			m["from_intent"] = from // qual intenção produziu esta vulnerabilidade
		}
		findingList = append(findingList, m)
	}
	out["finding_list"] = findingList
	// recent_facts: a janela mais recente entre os fatos não dobrados (≤N, factNodes em ordem decrescente de id, isto é, mais recentes primeiro). Os já dobrados em
	// digest e ainda frios (hidden) vão por cold_digests, sem repetir aqui. Cada item {id, summary, from_intent?,
	// confidence?}; detalhes como evidence via node_detail(id). Os mais antigos, folheie com list_facts.
	const recentFactsCap = 20
	recentFacts := make([]map[string]any, 0, recentFactsCap)
	for _, n := range factNodes {
		if len(recentFacts) >= recentFactsCap {
			break
		}
		if hidden(n.ID) {
			continue // já dobrado em digest e ainda frio — ver cold_digests
		}
		m := compactNode(n)
		if from := factFrom[n.ID]; from > 0 {
			m["from_intent"] = from // qual intenção produziu este fato
		}
		// confidence entra no panorama: para o planejador ver de relance qual conclusão é apenas inferred (especialmente conclusões negativas,
		// não tratar como caso fechado); evidence é longo, fica para node_detail(id).
		var fp map[string]any
		if json.Unmarshal(n.Payload, &fp) == nil {
			if c, ok := fp["confidence"].(string); ok && c != "" {
				m["confidence"] = c
			}
		}
		recentFacts = append(recentFacts, m)
	}
	out["recent_facts"] = recentFacts
	// recent_done_intents: a janela mais recente entre as intenções encerradas não dobradas (≤N, recentDone já em ordem decrescente de id).
	// Para as mais antigas veja a contagem done_intents_total + node_detail(id).
	const recentDoneCap = 12
	if len(recentDone) > recentDoneCap {
		recentDone = recentDone[:recentDoneCap]
	}
	out["recent_done_intents"] = compactIntents(recentDone, parentsOf, yieldsOf)
	// cold-digest §6.1: body dos digests da zona fria dobrada, tomando os N primeiros em ordem decrescente de tempo do membro mais recente; os digests mais antigos truncados
	// só recebem o id puro (ainda expansíveis com expand_digest), evitando que a única saída da zona fria seja esticada sem limite.
	const coldDigestsCap = 15
	if cds, more := coldDigestsRecent(t.ts, coldDigestsCap); len(cds) > 0 {
		out["cold_digests"] = cds // [{id, body, member_count}] — leia o body diretamente (§6.1)
		if len(more) > 0 {
			out["cold_digests_more"] = more // ids dos digests mais antigos truncados; expanda com expand_digest(id)
		}
	}
	// the original task (root) so the planner always has it, not just the
	// decomposed goals.
	if description, goal, err := t.ts.Root(); err == nil {
		out["task"] = map[string]any{"description": description, "goal": goal}
	}
	// Direct source tasks are a live, read-only blackboard view. Keep their
	// summaries in a separate field so their intents never enter this task's
	// frontier or get mistaken for locally claimable work.
	out["related_tasks"] = t.relatedTaskOverviews()
	// coverage: referência grosseira da cobertura de teste dos ativos — entre os ativos dentro do escopo (task_scope), a proporção
	// tocada por fatos + by_type (total/testados por tipo). Para ver os ativos específicos não testados, o agent chama list_untested_assets sob demanda e julga por conta própria. Só existe no contexto de tarefa.
	// Quando a função de cobertura de ativos está desativada (coverageDisabled): mantém apenas host_count (informação de percepção do número de hosts-alvo),
	// descarta as métricas de cobertura denominator/tested/pct/by_type/note etc., para não poluir o contexto nem induzir ao uso de
	// add_task_scope/list_untested_assets, que estão ocultas.
	if t.as != nil && t.ts != nil && t.taskID > 0 {
		{
			m := map[string]any{}
			if !t.coverageDisabled {
				if cov, err := t.as.TaskCoverageWithSources(t.taskID); err == nil {
					m["denominator"] = cov.Denominator
					m["tested"] = cov.Tested
					m["by_type"] = cov.ByType
					m["note"] = "coverage é a cobertura de teste dos ativos (incluindo interfaces e outros ativos relacionados), estimativa grosseira, apenas para referência: inclui o scope e as âncoras de fatos da tarefa atual e das tarefas diretamente associadas; o scope associado é somente leitura. Ativos do tipo contêiner/enumeração em massa a deixam baixa, não conclua por ela que tudo já foi testado; use add_task_scope para acrescentar ao escopo desta tarefa e list_untested_assets para ver os ativos não testados [normalmente não é preciso chamar list_untested_assets, basta avançar conforme a tarefa]; "
					if cov.Denominator == 0 {
						m["pct"] = nil
						m["status"] = "escopo não ancorado"
					} else {
						m["pct"] = cov.Pct
					}
				}
			}
			if hosts, err := t.as.HostsByTaskWithSources(t.taskID); err == nil {
				// Só se informa o total de hosts, sem mais despejar a lista de hosts no graph_overview (em tarefas de grande escopo isso seria uma grande quantidade de strings
				// repetida a cada rodada, de valor limitado para a decisão de planejamento); hosts específicos consulte sob demanda com list_assets.
				m["host_count"] = len(hosts)
			}
			if len(m) > 0 {
				out["coverage"] = m
			}
		}
	}
	return out
}

func inheritedMap(m map[string]any, sourceTaskID int64) map[string]any {
	m["source_task_id"] = sourceTaskID
	m["inherited"] = true
	return m
}

const (
	relatedOverviewTotalTextRunes      = 48_000
	relatedOverviewMaxTextPerSource    = 8_000
	relatedOverviewMaxGoalsPerSource   = 8
	relatedOverviewMaxHintsPerSource   = 6
	relatedOverviewMaxFactsPerSource   = 12
	relatedOverviewMaxFindingsPerTask  = 6
	relatedOverviewMaxIntentsPerTask   = 8
	relatedOverviewMaxScopePerSource   = 12
	relatedOverviewMaxDigestsPerSource = 6
)

// overviewTextBudget bounds inherited prompt text while preserving a fair slice
// for every direct source. Full evidence remains available through the on-demand
// read tools, so truncation here does not discard persisted blackboard data.
type overviewTextBudget struct {
	remaining int
	truncated bool
}

func relatedOverviewBudgetForSources(sourceCount int) int {
	if sourceCount <= 0 {
		return 0
	}
	if sourceCount > db.MaxTaskSourceCount {
		sourceCount = db.MaxTaskSourceCount
	}
	perSource := relatedOverviewTotalTextRunes / sourceCount
	if perSource > relatedOverviewMaxTextPerSource {
		perSource = relatedOverviewMaxTextPerSource
	}
	return perSource
}

func (b *overviewTextBudget) take(value any, fieldLimit int) string {
	var text string
	switch value := value.(type) {
	case string:
		text = strings.TrimSpace(value)
	case nil:
		return ""
	default:
		text = strings.TrimSpace(fmt.Sprint(value))
	}
	if text == "" {
		return ""
	}
	if b.remaining <= 0 || fieldLimit <= 0 {
		b.truncated = true
		return ""
	}
	runes := []rune(text)
	limit := fieldLimit
	if limit > b.remaining {
		limit = b.remaining
	}
	if len(runes) > limit {
		b.truncated = true
		if limit == 1 {
			text = "…"
		} else {
			text = string(runes[:limit-1]) + "…"
		}
		runes = []rune(text)
	}
	b.remaining -= len(runes)
	return text
}

func recentTerminalIntents(store *db.ExplorationStore, limit int) []*db.Node {
	if limit <= 0 {
		return []*db.Node{}
	}
	const batch = 300
	cursor := int64(0)
	out := make([]*db.Node, 0, limit)
	for len(out) < limit {
		page, more, err := store.ListByKindPage(db.KindIntent, cursor, batch)
		if err != nil || len(page) == 0 {
			break
		}
		for _, intent := range page {
			switch intent.State {
			case "done", "blocked", "exhausted", "stopped":
				out = append(out, intent)
			}
			if len(out) >= limit {
				break
			}
		}
		if !more {
			break
		}
		cursor = page[len(page)-1].ID
	}
	return out
}

// relatedTaskOverviews distills persistent blackboard state from direct source
// tasks. It intentionally reads each source's local store methods, never its own
// related sources, so inheritance is one level only.
func (t *ToolSet) relatedTaskOverviews() []map[string]any {
	sources, err := t.ts.DirectSourceStores()
	if err != nil {
		return []map[string]any{}
	}
	if len(sources) > db.MaxTaskSourceCount {
		sources = sources[:db.MaxTaskSourceCount]
	}
	perSourceTextBudget := relatedOverviewBudgetForSources(len(sources))
	out := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		ts := source.Store
		// §2 cross-task: render the source task's OWN folded view — fold out the
		// members it has already folded, and surface its cold_digests read-only.
		hidden := hiddenMembersFor(ts)
		budget := overviewTextBudget{remaining: perSourceTextBudget}
		item := map[string]any{
			"source_task_id": source.Task.TaskID,
			"inherited":      true,
			"task": map[string]any{
				"description": budget.take(source.Task.Description, 800),
				"goal":        budget.take(source.Task.Goal, 800),
				"status":      source.Task.Status,
			},
		}
		stats, statsErr := ts.Stats()

		edges, _ := ts.Edges(5000)
		parentsOf := map[int64][]int64{}
		yieldsOf := map[int64][]int64{}
		factFrom := map[int64]int64{}
		for _, edge := range edges {
			switch edge.Rel {
			case db.RelDerivedFrom, db.RelSpawns:
				parentsOf[edge.To] = append(parentsOf[edge.To], edge.From)
			case db.RelYields:
				yieldsOf[edge.From] = append(yieldsOf[edge.From], edge.To)
				factFrom[edge.To] = edge.From
			}
		}

		goals, _ := ts.ListByKind(db.KindGoal, relatedOverviewMaxGoalsPerSource)
		goalSummary := make([]map[string]any, 0, len(goals))
		for _, goal := range goals {
			var payload map[string]any
			_ = json.Unmarshal(goal.Payload, &payload)
			goalSummary = append(goalSummary, inheritedMap(map[string]any{
				"id": goal.ID, "state": goal.State, "text": budget.take(payload["text"], 400),
			}, source.Task.TaskID))
		}
		item["goals"] = goalSummary

		hints, _ := ts.ListByKind(db.KindHint, relatedOverviewMaxHintsPerSource)
		hintSummary := make([]map[string]any, 0, len(hints))
		for _, hint := range hints {
			var payload map[string]any
			_ = json.Unmarshal(hint.Payload, &payload)
			hintSummary = append(hintSummary, inheritedMap(map[string]any{
				"id": hint.ID, "state": hint.State, "text": budget.take(payload["text"], 400),
			}, source.Task.TaskID))
		}
		item["hints"] = hintSummary

		facts, _ := ts.ListByKind(db.KindFact, relatedOverviewMaxFactsPerSource)
		findings, _ := ts.ListByKind(db.KindFinding, relatedOverviewMaxFindingsPerTask)
		intentNodes, _ := ts.ListByKind(db.KindIntent, 300)
		terminalIntent := make(map[int64]bool, len(intentNodes))
		for _, intent := range intentNodes {
			terminalIntent[intent.ID] = inheritedIntentSummaryState(intent.State)
		}
		item["facts"] = len(facts)
		item["findings"] = len(findings)
		if statsErr == nil {
			item["facts"] = stats[db.KindFact]
			item["findings"] = stats[db.KindFinding]
			if stats[db.KindGoal] > len(goals) || stats[db.KindHint] > len(hints) ||
				stats[db.KindFact] > len(facts) || stats[db.KindFinding] > len(findings) {
				budget.truncated = true
			}
		}
		recentFindings := make([]map[string]any, 0, len(findings))
		for _, finding := range findings {
			entry := inheritedMap(compactFinding(finding), source.Task.TaskID)
			entry["summary"] = budget.take(entry["summary"], 400)
			recentFindings = append(recentFindings, entry)
		}
		item["recent_findings"] = recentFindings
		recentFacts := make([]map[string]any, 0, len(facts))
		for _, fact := range facts {
			if hidden(fact.ID) {
				continue // folded into this source's cold_digests — shown there (§2/§6.2)
			}
			m := inheritedMap(compactNode(fact), source.Task.TaskID)
			m["summary"] = budget.take(m["summary"], 400)
			if from := factFrom[fact.ID]; from > 0 && terminalIntent[from] {
				m["from_intent"] = from
			}
			var payload map[string]any
			if json.Unmarshal(fact.Payload, &payload) == nil {
				if confidence, ok := payload["confidence"].(string); ok && confidence != "" {
					m["confidence"] = confidence
				}
			}
			recentFacts = append(recentFacts, m)
		}
		item["recent_facts"] = recentFacts

		recentDoneRaw := recentTerminalIntents(ts, relatedOverviewMaxIntentsPerTask)
		recentDone := recentDoneRaw[:0] // in-place filter: drop this source's folded intents (§2)
		for _, intent := range recentDoneRaw {
			if hidden(intent.ID) {
				continue
			}
			recentDone = append(recentDone, intent)
		}
		for _, intent := range recentDone {
			intent.Inherited = true
			intent.SourceTaskID = source.Task.TaskID
		}
		intentResults := compactIntents(recentDone, parentsOf, yieldsOf)
		for i, intent := range recentDone {
			intentResults[i]["summary"] = budget.take(intentResults[i]["summary"], 400)
			acts, _, err := ts.ActivityPageForTerminalIntent(intent.ID, 0, 20)
			if err != nil {
				continue
			}
			var resultSummary, textFallback string
			for _, activity := range acts {
				switch activity.Kind {
				case "result":
					resultSummary = activity.Summary
				case "text":
					textFallback = activity.Summary
				}
			}
			if resultSummary == "" {
				resultSummary = textFallback
			}
			if resultSummary != "" {
				intentResults[i]["result_summary"] = budget.take(resultSummary, 800)
			}
		}
		item["recent_intent_results"] = intentResults
		// §2 cross-task: the source task's folded cold region, read-only, newest-member
		// first & capped like the current task's. Members (and overflow digests) are
		// resolvable via expand_digest(id)/node_detail(id), which search source tasks.
		if cds, more := coldDigestsRecent(ts, relatedOverviewMaxDigestsPerSource); len(cds) > 0 {
			for _, cd := range cds {
				cd["inherited"] = true
				cd["source_task_id"] = source.Task.TaskID
			}
			item["cold_digests"] = cds
			if len(more) > 0 {
				item["cold_digests_more"] = more // ids dos digests mais antigos truncados; expand_digest(id) expande
			}
		}
		if statsErr == nil {
			item["node_stats"] = stats
		}

		if t.as != nil {
			if scopeRows, err := t.as.ListTaskScope(source.Task.TaskID); err == nil && len(scopeRows) > 0 {
				scopeCount := len(scopeRows)
				if len(scopeRows) > relatedOverviewMaxScopePerSource {
					scopeRows = scopeRows[:relatedOverviewMaxScopePerSource]
					budget.truncated = true
				}
				scope := make([]map[string]any, 0, len(scopeRows))
				for _, row := range scopeRows {
					entry := map[string]any{"kind": row.Kind, "source": budget.take(row.Source, 300)}
					switch {
					case row.Domain != "":
						entry["value"] = budget.take(row.Domain, 400)
					case row.Net != "":
						entry["value"] = budget.take(row.Net, 400)
					case row.Value != "":
						entry["value"] = budget.take(row.Value, 400)
					case row.CompanyID != nil:
						entry["company_id"] = *row.CompanyID
					}
					scope = append(scope, entry)
				}
				item["asset_scope"] = scope
				item["asset_scope_count"] = scopeCount
			}
			if coverage, err := t.as.TaskCoverage(source.Task.TaskID, source.Task.ExplorationID); err == nil {
				item["asset_coverage"] = map[string]any{
					"denominator": coverage.Denominator,
					"tested":      coverage.Tested,
					"pct":         coverage.Pct,
					"by_type":     coverage.ByType,
				}
			}
		}
		if budget.truncated {
			item["summary_truncated"] = true
		}
		out = append(out, item)
	}
	return out
}

func inheritedIntentSummaryState(state string) bool {
	switch state {
	case "done", "blocked", "exhausted", "stopped":
		return true
	default:
		return false
	}
}

// compactNode distills any exploration node to id + summary + state, dropping the
// big detail/evidence (fetch that on demand via node_detail).
func compactNode(n *db.Node) map[string]any {
	var p map[string]any
	_ = json.Unmarshal(n.Payload, &p)
	m := map[string]any{"id": n.ID, "state": n.State, "summary": p["summary"]}
	if n.Inherited {
		inheritedMap(m, n.SourceTaskID)
	}
	return m
}

// compactFinding is compactNode plus the vuln-specific vulnclass/severity.
func compactFinding(n *db.Node) map[string]any {
	var p map[string]any
	_ = json.Unmarshal(n.Payload, &p)
	m := map[string]any{"id": n.ID, "state": n.State, "summary": p["summary"]}
	if n.Inherited {
		inheritedMap(m, n.SourceTaskID)
	}
	if vc, ok := p["vulnclass"]; ok && vc != nil && vc != "" {
		m["vulnclass"] = vc
	}
	if sv, ok := p["severity"]; ok && sv != nil && sv != "" {
		m["severity"] = sv
	}
	return m
}

func (t *ToolSet) listFindings() actool.CoreTool {
	return t.readExpTool("list_findings", "Lista as [vulnerabilidades confirmadas] desta tarefa e das tarefas diretamente associadas (compacto: id+task_id+intent_id+vulnclass+severity+resumo+estado). Os itens de tarefas associadas trazem source_task_id/inherited=true e são somente leitura. Aqui só há vulnerabilidades; fatos de exploração comuns use list_facts, detalhes use node_detail(id).",
		obj(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			f, _ := t.ts.ListByKindWithSources(db.KindFinding, 500)
			if err := t.ts.PopulateFindingTrafficIDs(f); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			intentOf, _ := t.ts.FindingIntentsWithSources() // finding id -> id da intenção que o produziu
			taskID := t.taskID
			if taskID <= 0 {
				taskID, _ = t.ts.TaskID()
			}
			out := make([]map[string]any, 0, len(f))
			for _, n := range f {
				m := compactFinding(n)
				if n.FindingID > 0 {
					m["finding_id"], m["finding_node_id"], m["traffic_count"] = n.FindingID, n.ID, n.TrafficCount
				}
				if n.Inherited {
					m["task_id"] = n.SourceTaskID
				} else {
					m["task_id"] = taskID
				}
				if iid, ok := intentOf[n.ID]; ok {
					m["intent_id"] = iid
				}
				out = append(out, m)
			}
			return jsonResult(out)
		})
}

// factsPageSize is the default page size for list_facts. Facts pile up on long
// tasks; returning all of them at once (the old behaviour) could blow up the
// context, so default to the newest page and let the agent page/filter for more.
const factsPageSize = 20

func (t *ToolSet) listFacts() actool.CoreTool {
	return t.readExpTool("list_facts", "Lista paginada dos [fatos/conclusões de exploração] desta tarefa e das tarefas diretamente associadas, mais recentes primeiro (compacto: id+resumo+estado; resumos longos são truncados, para o texto completo use node_detail(id)). Todos os parâmetros são opcionais: limit (padrão 20, máximo 100), before (cursor; passe o next_before retornado pela página anterior para obter a página mais antiga; omitido/0 = página mais recente), q (filtra por palavra-chave do resumo). Retorna {facts, total, has_more, next_before}: total é o total após o filtro, e quando has_more=true use next_before para continuar paginando. Os itens de tarefas associadas trazem source_task_id/inherited=true e são somente leitura. Para vulnerabilidades veja list_findings.",
		obj(map[string]any{
			"limit":  intp("Quantidade de itens retornados, padrão 20, máximo 100"),
			"before": intp("Cursor de paginação: retorna apenas fatos mais antigos com id menor que este valor; omitido ou 0 = página mais recente"),
			"q":      str("Filtra por palavra-chave do resumo do fato (sem distinção de maiúsculas/minúsculas); omitido = sem filtro"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Limit  int    `json:"limit"`
				Before int64  `json:"before"`
				Q      string `json:"q"`
			}
			_ = json.Unmarshal(in, &a)
			limit := a.Limit
			if limit <= 0 {
				limit = factsPageSize
			}
			if limit > 100 {
				limit = 100
			}
			f, hasMore, total, err := t.ts.ListByKindPageWithSources(db.KindFact, a.Before, limit, strings.TrimSpace(a.Q))
			if err != nil {
				return actool.Result{}, err
			}
			out := make([]map[string]any, 0, len(f))
			for _, n := range f {
				out = append(out, compactFact(n))
			}
			res := map[string]any{"facts": out, "total": total, "has_more": hasMore}
			if hasMore && len(f) > 0 {
				res["next_before"] = f[len(f)-1].ID // passe-o de volta para obter a próxima página (mais antiga)
			}
			return jsonResult(res)
		})
}

// factSummaryMax caps a fact summary in list_facts output. Facts carry one-line
// conclusions, but nothing enforces brevity; a runaway summary must not bloat a
// whole page. Full text stays available via node_detail(id).
const factSummaryMax = 160

// compactFact is compactNode with the summary rune-capped for list_facts, so a
// page of facts stays bounded regardless of how long any single summary grew.
func compactFact(n *db.Node) map[string]any {
	m := compactNode(n)
	if s, ok := m["summary"].(string); ok && len([]rune(s)) > factSummaryMax {
		m["summary"] = string([]rune(s)[:factSummaryMax]) + "…"
		m["summary_truncated"] = true
	}
	return m
}

func (t *ToolSet) nodeDetail() actool.CoreTool {
	return t.readExpTool("node_detail", "Obtém o conteúdo completo de um [nó do grafo de exploração] desta tarefa ou de tarefas diretamente associadas. Nós herdados trazem source_task_id/inherited=true e são somente leitura. Restrito aos ids de nós de exploração retornados por list_facts/list_findings/graph_overview; para ativos use list_assets/asset_neighbors.",
		obj(map[string]any{"id": idp("id do nó do grafo de exploração (não é id de ativo)")}, "id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				ID json.RawMessage `json:"id"`
			}
			_ = json.Unmarshal(in, &a)
			id := pid(a.ID)
			if id <= 0 {
				return actool.Errorf("id é obrigatório"), nil
			}
			n, err := t.ts.GetNodeWithSources(id)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if n == nil {
				return actool.Errorf(fmt.Sprintf("Nó de exploração %d não encontrado. Se o que você quer consultar é um ativo, use list_assets / asset_neighbors (ativos e nós de exploração são espaços de id distintos; o id de um ativo não pode ser passado a node_detail).", id)), nil
			}
			if err := t.ts.PopulateFindingTrafficIDs([]*db.Node{n}); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(n) // full payload incl. detail / evidence, plus explicit finding IDs
		})
}

// --- planner write tools ---

// intentItem é um item de direção de exploração, em lote/único, de add_intent.
type intentItem struct {
	Summary   string            `json:"summary"`
	AssetIDs  []json.RawMessage `json:"asset_ids"`
	ParentIDs []json.RawMessage `json:"parent_ids"`
	Priority  int               `json:"priority"`
}

// addOneIntent cria um nó de intenção e conecta a linhagem upstream, retornando o id.
// Restrição: a intenção só pode ancorar em conhecimento confirmado — cada parent_id deve ser um nó
// fact/finding já existente (não pode pendurar em outra intenção/objetivo/dica). Uma direção totalmente nova de topo deixa parent_ids vazio, com fallback para conectar ao origin fact.
// Assim "toda intenção se conecta a um nó fact e é guiada por descoberta, não planejada do nada" é imposto já no caminho de criação.
func (t *ToolSet) addOneIntent(it intentItem) (int64, error) {
	if strings.TrimSpace(it.Summary) == "" {
		return 0, fmt.Errorf("summary não pode ser vazio")
	}
	// Primeiro valida as âncoras (antes de criar o nó, para que âncoras ruins não deixem intenções órfãs).
	parents := pidList(it.ParentIDs)
	for _, pidv := range parents {
		n, err := t.ts.GetNodeWithSources(pidv)
		if err != nil || n == nil {
			return 0, fmt.Errorf("parent_id %d não existe nesta tarefa nem nas tarefas diretamente associadas: parent_ids deve ser id de nó [fato (fact)/descoberta (finding)] já existente; para uma direção totalmente nova de topo, deixe parent_ids vazio", pidv)
		}
		if n.Kind != db.KindFact && n.Kind != db.KindFinding {
			return 0, fmt.Errorf("parent_id %d é um nó %q e não pode ser âncora de intenção: a intenção só pode ancorar em [fato (fact)/descoberta (finding)] já confirmados, não pode pendurar em intenção/objetivo/dica; para uma direção totalmente nova de topo, deixe parent_ids vazio", pidv, n.Kind)
		}
	}
	priority := it.Priority
	if priority == 0 {
		priority = 5
	}
	anchors := pidList(it.AssetIDs)
	// Interceptação de ativos: se os ativos vinculados à intenção atingirem regras de interceptação de ativos do sistema, é proibido despachar a intenção.
	if t.as != nil && len(anchors) > 0 {
		hits, err := t.as.CheckAssetsIntercept(t.taskID, anchors)
		if err != nil {
			return 0, fmt.Errorf("falha na validação de interceptação de ativos: %w", err)
		}
		if len(hits) > 0 {
			var b strings.Builder
			fmt.Fprintf(&b, "Os ativos vinculados à intenção \"%s\" não passaram na validação de escopo de teste; pare de testar os ativos relacionados:", it.Summary)
			for _, h := range hits {
				fmt.Fprintf(&b, "\n - %s", h.Describe())
			}
			return 0, fmt.Errorf("%s", b.String())
		}
	}
	payload := map[string]any{"summary": it.Summary}
	if len(anchors) > 0 {
		payload["asset_ids"] = anchors
	}
	id, err := t.ts.AddIntent(payload, priority, anchors, "planner")
	if err != nil {
		return 0, err
	}
	// upstream lineage: link each (validated) fact/finding parent → this intent, so
	// "multiple facts combine into one new intent" is expressible.
	for _, parent := range parents {
		_ = t.ts.Link(parent, db.RelDerivedFrom, id)
	}
	// a top-level intent (no explicit parent) connects to the origin fact, so every
	// intent still traces back to a fact node — at task start the only fact is the
	// origin, and the first intents derive from it.
	if len(parents) == 0 {
		if origin, _ := t.ts.OriginFactID(); origin > 0 {
			_ = t.ts.Link(origin, db.RelDerivedFrom, id)
		}
	}
	return id, nil
}

func (t *ToolSet) addIntent() actool.CoreTool {
	return t.writeExpTool("add_intent", "Gera uma [direção de exploração], grava no frontier e a conecta à cadeia de exploração. A intenção é uma direção de exploração aberta, não um tipo fixo — descreva livremente em uma frase, no summary, o que explorar/validar/explorar-vulnerabilidade.\n"+
		"★Prefira lote: coloque várias novas direções selecionadas numa rodada no array intents e envie de uma vez (poupa idas e vindas em relação a chamar uma a uma). Retorna o array ids, com o mesmo tamanho e ordem de intents (item com falha tem id=0, detalhes em errors). Para um único item, omita intents e dê summary no nível superior.",
		obj(map[string]any{
			"intents":    map[string]any{"type": "array", "description": "[Prefira este] Array de novas direções de exploração, processadas em ordem. Os campos de cada elemento são os mesmos dos campos de nível superior abaixo (summary/asset_ids/parent_ids/priority). O retorno ids tem o mesmo tamanho e ordem deste array.", "items": map[string]any{"type": "object"}},
			"summary":    str("[Único] Descreva em uma frase esta direção de exploração: o que fazer + por quê. Basta deixar a direção clara, sem depender de id de ativo."),
			"asset_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "[ids dos ativos-alvo] que esta direção vai testar/atacar (**passe sempre que possível**, 0/1/vários; são os ids de ativo retornados por list_assets, não ids de nós de exploração): quais ativos (site/interface/parâmetro/host etc.) esta direção de exploração visa. Sempre que a direção girar em torno de ativos concretos, passe-os obrigatoriamente — é a marcação estruturada de \"quais alvos esta exploração ataca\", usada para deduplicação de cobertura e para ligar a intenção à cadeia de ativos. Deixe vazio apenas para reconhecimento puramente global, sem ativo-alvo concreto."},
			"parent_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "ids das âncoras upstream (opcional, 0/1/vários): de quais [fatos (fact)/descobertas (finding) confirmados] esta direção foi sintetizada. **Só se pode preencher ids de nós fact/finding já existentes, não de intenção/objetivo/dica** — a intenção deve ancorar em conhecimento confirmado, guiada por descoberta e não planejada do nada. Se vários fatos juntos produzem uma nova intenção, passe vários; para uma direção de reconhecimento totalmente nova de topo, deixe vazio (será pendurada automaticamente no origin fact, o ponto de partida da tarefa)."},
			"priority":   intp("Prioridade 0-10, padrão 5"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Intents    []intentItem `json:"intents"`
				intentItem              // modo único: summary/asset_ids/parent_ids/priority de nível superior
			}
			_ = json.Unmarshal(in, &a)
			batch := len(a.Intents) > 0
			items := a.Intents
			if !batch {
				items = []intentItem{a.intentItem}
			}

			ids := make([]int64, len(items))
			errs := map[string]string{}
			createdAny := false
			for i, it := range items {
				id, err := t.addOneIntent(it)
				if err != nil {
					errs[strconv.Itoa(i)] = err.Error()
					continue
				}
				ids[i] = id
				createdAny = true
			}

			// A pessoa injeta uma intenção diretamente via agent principal → se a tarefa já está done (ramo goalless, sem objetivo open), puxa
			// de volta a running, para que o worker possa pegar esta intenção e executá-la. resumeTask só é conectado pelo Chat do agent principal
			// (SetResumeTask); o ToolSet do planner é nil, então quando o próprio planner chama add_intent este trecho é
			// no-op, sem afetar a produção normal de intenções. O nó de intenção já foi criado acima (open), e na reativação não será julgado esgotado por engano.
			if createdAny && t.resumeTask != nil {
				t.resumeTask()
			}

			if !batch { // único: mantém o retorno original
				if e, bad := errs["0"]; bad {
					return actool.Errorf(e), nil
				}
				return actool.Text(fmt.Sprintf("intent created: %d", ids[0])), nil
			}
			out := map[string]any{"ids": ids}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		})
}

func (t *ToolSet) listGoals() actool.CoreTool {
	return t.readExpTool("list_goals", "Lista os nós de objetivo desta tarefa e seu estado (open/met), usada para julgar se foram atingidos.",
		obj(map[string]any{}),
		func(context.Context, json.RawMessage) (actool.Result, error) {
			g, _ := t.ts.ListByKind(db.KindGoal, 100)
			return jsonResult(g)
		})
}

func (t *ToolSet) proveGoal() actool.CoreTool {
	return t.writeExpTool("prove_goal", "Chame quando julgar que alguma descoberta/fato comprova o atingimento de um objetivo: conecta o nó de evidência ao nó do objetivo e marca o objetivo como met.",
		obj(map[string]any{
			"goal_id":     idp("id do nó do objetivo"),
			"evidence_id": idp("id do nó de descoberta/fato que o comprova"),
			"reason":      str("Por que esta evidência satisfaz o objetivo"),
		}, "goal_id", "evidence_id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				GoalID     json.RawMessage `json:"goal_id"`
				EvidenceID json.RawMessage `json:"evidence_id"`
				Reason     string          `json:"reason"`
			}
			_ = json.Unmarshal(in, &a)
			goal, ev := pid(a.GoalID), pid(a.EvidenceID)
			if goal == 0 || ev == 0 {
				return actool.Errorf("goal_id e evidence_id são obrigatórios"), nil
			}
			goalNode, err := t.ts.GetNode(goal)
			if err != nil || goalNode == nil || goalNode.Kind != db.KindGoal {
				return actool.Errorf("goal_id deve ser um nó de objetivo desta tarefa (objetivos de tarefas associadas são somente leitura)"), nil
			}
			evidenceNode, err := t.ts.GetNodeWithSources(ev)
			if err != nil || evidenceNode == nil || (evidenceNode.Kind != db.KindFact && evidenceNode.Kind != db.KindFinding) {
				return actool.Errorf("evidence_id deve ser um nó de fato/vulnerabilidade desta tarefa ou de tarefas diretamente associadas"), nil
			}
			_ = t.ts.Link(ev, db.RelProves, goal)
			_ = t.ts.SetNodeState(goal, "met")
			// A cada objetivo marcado como met, verifica se [todos os objetivos] desta tarefa já estão met; se sim, julga automaticamente
			// a tarefa como concluída (define GoalMet), sem depender de o modelo chamar goal_met explicitamente.
			if goals, err := t.ts.ListByKind(db.KindGoal, 1000); err == nil && len(goals) > 0 {
				allMet := true
				for _, g := range goals {
					if g.State != "met" {
						allMet = false
						break
					}
				}
				if allMet {
					t.GoalMet = true
					t.Reason = fmt.Sprintf("todos os %d objetivos estão met (o último acionado pelo goal %d)", len(goals), goal)
					return actool.Text(fmt.Sprintf("goal %d marked met; todos os objetivos desta tarefa foram atingidos, a tarefa é julgada concluída automaticamente", goal)), nil
				}
			}
			return actool.Text(fmt.Sprintf("goal %d marked met", goal)), nil
		})
}

func (t *ToolSet) goalMet() actool.CoreTool {
	return writeTool("goal_met", "[Encerra imediatamente a tarefa inteira] — chame somente quando você confirmar que [todos os objetivos da tarefa foram realmente atingidos e o conjunto foi concluído] (atenção: é a conclusão [da tarefa como um todo]; ter atingido apenas um dos objetivos / uma flag / uma vulnerabilidade [não conta] — nesse caso basta marcar esse objetivo com prove_goal). ⚠️Ela não serve para \"encerrar a rodada de planejamento\": se nesta rodada não há novas intenções a despachar ou se está aguardando a produção do worker, [basta encerrar a rodada diretamente, não chame esta ferramenta] (0 intenção é perfeitamente normal). Para o julgamento normal, prefira prove_goal para comprovar os objetivos um a um; goal_met é apenas o meio de contornar a comprovação um a um e concluir diretamente pelo nível global.",
		obj(map[string]any{"reason": str("Motivo do atingimento (deve ser evidência de que o objetivo foi realmente atingido, não um motivo de encerrar a rodada como \"nenhuma direção nova nesta rodada\")")}, "reason"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct{ Reason string }
			_ = json.Unmarshal(in, &a)
			t.GoalMet = true
			t.Reason = a.Reason
			return actool.Text("acknowledged: goal marked met"), nil
		})
}

// --- worker write tools ---

func (t *ToolSet) addFinding() actool.CoreTool {
	return writeTool("report_finding", "Registra uma vulnerabilidade confirmada; use evidence para fornecer evidências verificáveis como saída de comandos e logs. No contexto de tarefa, passe o intent_id atual. O finding_id retornado é o ID do registro independente da vulnerabilidade, e finding_node_id é o ID do nó de exploração (a primeira linha mantém o número desse nó).", obj(map[string]any{
		"vulnclass": str("Classe da vulnerabilidade"), "name": str("Nome da vulnerabilidade"), "severity": str("critical|high|medium|low"), "summary": str("Resumo da descoberta"),
		"intent_id": idp("id da intenção da tarefa atual"), "asset_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "ids dos ativos afetados"},
		"evidence":         str("Texto de evidência/PoC"),
		"evidence_hint_id": idp("Opcional: ID do nó de hint desta tarefa correspondente a esta vulnerabilidade, carregando automaticamente seus traffic_refs estruturados; não pode referenciar hint herdado nem hint de outra vulnerabilidade"),
		"traffic_refs": map[string]any{"type": "array", "description": "Opcional; para vulnerabilidades HTTP/HTTPS, primeiro pesquise e confira uma a uma que a requisição/resposta realmente sustenta a conclusão da vulnerabilidade, e depois preencha os IDs reais na ordem de reprodução. Para vulnerabilidades não HTTP como TCP, tráfego não coletado ou sem registro exato, omita ou passe [], sem impedir o reporte; é possível explicar o motivo em evidence e fornecer outras evidências verificáveis. Não adivinhe IDs, não presuma associação por domínio/horário nem repita sondagens apenas para completar pacotes. Finalidade: baseline controle normal / proof comprovação da vulnerabilidade / verification validação complementar / supporting evidência auxiliar.",
			"items": obj(map[string]any{"traffic_id": str("ID de tráfego real retornado por traffic_search"), "role": map[string]any{"type": "string", "enum": []string{"baseline", "proof", "verification", "supporting"}}, "note": str("Como este tráfego sustenta a conclusão da vulnerabilidade")}, "traffic_id")},
	}, "vulnclass", "severity", "summary"), func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
		var a struct {
			VulnClass, Name, Severity, Summary, Evidence string
			IntentID                                     json.RawMessage   `json:"intent_id"`
			AssetIDs                                     []json.RawMessage `json:"asset_ids"`
			TrafficRefs                                  []db.TrafficRef   `json:"traffic_refs"`
			EvidenceHintID                               json.RawMessage   `json:"evidence_hint_id"`
		}
		if err := json.Unmarshal(in, &a); err != nil {
			return actool.Errorf(err.Error()), nil
		}
		if t.ts == nil {
			return actool.Errorf("report_finding requer contexto da tarefa; em conversas da plataforma, repasse a vulnerabilidade à tarefa correspondente via add_task_hint, levando no hint os traffic_refs já existentes, para que o Agent da tarefa a registre. Vulnerabilidades já registradas podem ser vinculadas depois com bind_finding_traffic."), nil
		}
		// Auto-binding off: ignore the evidence params instead of rejecting the call.
		// stripTrafficParameters already removes them from the advertised schema, but
		// models routinely emit fields anyway — failing here would discard a confirmed
		// finding over a stray parameter. The success path below reports evidence_status
		// "not_bound" with the "desativado, pode vincular manualmente na página" note, which is what the caller needs.
		if !findingTrafficBindingEnabled() {
			a.TrafficRefs, a.EvidenceHintID = nil, nil
		}
		if len(a.EvidenceHintID) > 0 && pid(a.EvidenceHintID) <= 0 {
			return actool.Errorf("evidence_hint_id deve ser um ID de nó de hint válido; omita se não houver hint de repasse"), nil
		}
		refs, err := t.findingRefsFromHint(pid(a.EvidenceHintID), a.TrafficRefs)
		if err != nil {
			return actool.Errorf(err.Error()), nil
		}
		input := db.RecordFindingInput{TaskID: t.taskID, ExplorationID: t.ts.ID(), IntentID: pid(a.IntentID), VulnClass: a.VulnClass, Name: a.Name, Severity: a.Severity, Summary: a.Summary, Evidence: a.Evidence, Worker: t.worker, AssetIDs: pidList(a.AssetIDs)}
		var recorded *db.RecordedFinding
		if t.findingRecorder != nil {
			recorded, err = t.findingRecorder.Record(ctx, input, refs)
		} else if len(refs) > 0 {
			return actool.Errorf("armazenamento de evidência de tráfego indisponível; vulnerabilidade não registrada"), nil
		} else {
			recorded, err = t.ts.RecordFinding(ctx, input)
		}
		if err != nil {
			return actool.Errorf(err.Error()), nil
		}
		if t.notifyFinding != nil {
			iid := input.IntentID
			if iid <= 0 {
				iid = t.ownerNode
			}
			t.notifyFinding(iid, a.Summary)
		} else if t.notify != nil {
			t.notify()
		}
		t.writes.Findings++
		// Keep the first line's node-ID contract for existing reporter triggers.
		for i := range recorded.Traffic.Bindings {
			recorded.Traffic.Bindings[i].Snapshot.ReqHead = ""
			recorded.Traffic.Bindings[i].Snapshot.RespHead = ""
		}
		result := struct {
			*db.RecordedFinding
			EvidenceStatus string `json:"evidence_status"`
			EvidenceNote   string `json:"evidence_note,omitempty"`
		}{RecordedFinding: recorded, EvidenceStatus: "bound"}
		if len(recorded.Traffic.Bindings) == 0 {
			result.EvidenceStatus = "not_bound"
			result.EvidenceNote = "Vulnerabilidade salva, sem tráfego vinculado. Casos TCP/sem pacotes podem prosseguir normalmente; se já houver tráfego HTTP verificado, use o bind_finding_traffic disponível ou a página da vulnerabilidade para vincular depois e então concluir o repasse de evidências. Não crie a vulnerabilidade de novo."
			if !findingTrafficBindingEnabled() {
				result.EvidenceNote = "Vulnerabilidade salva. O vínculo automático de tráfego pelo Agent está desativado; é possível associar o tráfego manualmente na página."
			}
		}
		raw, _ := json.Marshal(result)
		return actool.Text(fmt.Sprintf("finding recorded: %d\n%s", recorded.NodeID, raw)), nil
	})
}

// recordFact writes a general exploration RESULT/conclusion (not a vuln, not a
// new asset) into the EXPLORATION graph, chained to the intent that produced it.
// This is the home for observations and — importantly — negative results
// ("port closed", "param not injectable", "no login found"). Such conclusions
// must NOT be stuffed into the asset graph via upsert_asset.
// factItem é um item de fato, em lote/único, de record_fact.
type factItem struct {
	Summary    string            `json:"summary"`
	Detail     string            `json:"detail"`
	Evidence   string            `json:"evidence"`   // uma linha de evidência-chave (comando + linhas-chave da saída), que sustenta a conclusão e facilita a conferência posterior
	Confidence string            `json:"confidence"` // observed (visto diretamente) | inferred (inferido a partir dos fenômenos)
	IntentID   json.RawMessage   `json:"intent_id"`
	AssetIDs   []json.RawMessage `json:"asset_ids"`
}

// recordOneFact grava um nó fact e o conecta à intenção (intent→yields→fact). defaultIntent é
// a intenção padrão em lote (usada quando este item não traz intent_id).
func (t *ToolSet) recordOneFact(it factItem, defaultIntent int64) (int64, error) {
	if strings.TrimSpace(it.Summary) == "" {
		return 0, fmt.Errorf("summary não pode ser vazio")
	}
	payload := map[string]any{"summary": it.Summary}
	if it.Detail != "" {
		payload["detail"] = it.Detail
	}
	if e := strings.TrimSpace(it.Evidence); e != "" {
		payload["evidence"] = e
	}
	if c := strings.TrimSpace(it.Confidence); c != "" {
		payload["confidence"] = c
	}
	intent := pid(it.IntentID)
	if intent <= 0 {
		intent = defaultIntent
	}
	if intent > 0 {
		node, err := t.ts.GetNode(intent)
		if err != nil || node == nil || node.Kind != db.KindIntent {
			return 0, fmt.Errorf("intent_id deve ser uma intenção desta tarefa (intenções de tarefas associadas são somente leitura)")
		}
	}
	// a fact is its OWN node kind (distinct from a vuln finding).
	id, err := t.ts.AddNode(db.KindFact, payload, 5, "confirmed", t.worker, pidList(it.AssetIDs))
	if err != nil {
		return 0, err
	}
	if intent > 0 {
		_ = t.ts.Link(intent, db.RelYields, id) // chain: intent -> fact
	}
	t.writes.Facts++
	return id, nil
}

func (t *ToolSet) recordFact() actool.CoreTool {
	return t.writeExpTool("record_fact", "Grava [fatos/conclusões] de exploração no grafo de exploração, conectados à intenção que os produziu (intent_id). Usado para registrar resultados de exploração — incluindo [conclusões positivas] como fingerprints/enumeração, e [conclusões negativas] como 'porta fechada'/'parâmetro não injetável'/'nenhuma entrada de login encontrada'.\n"+
		"⚠️Várias observações de uma exploração devem ser [resumidas em um único fato], não divididas em vários; o que puder ser mesclado num só fato deve ser representado por um só fato: summary = frase de resumo desta conclusão, detail = detalhes relevantes (podem conter vários itens concretos). Exemplo: intenção de fingerprint → um fato {summary:'identificada a pilha tecnológica e as características de resposta do site X', detail:'nginx 1.25 / Vue3 / 200 / title=.. / body_len=..'}, e não um fato para cada código de status, fingerprint e título. Uma intenção normalmente produz só um fato; dividir demais faz o grafo inchar sem limite.\n"+
		"★O array facts serve para gravar de uma vez várias conclusões [distintas entre si] (cada item pode omitir intent_id, usando por padrão o intent_id de nível superior). Retorna o array ids, com o mesmo tamanho e ordem de facts.\n"+
		"⚠️Escreva só as conclusões que você [realmente viu] na saída das ferramentas, sem inventar. evidence e confidence servem para evitar que conclusões imprecisas contaminem o grafo:\n"+
		"  · evidence = [uma linha] de evidência-chave que sustenta esta conclusão (comando + uma ou duas linhas de saída que melhor comprovam), **seja conciso** — os detalhes já estão em detail, não cole mais saídas longas aqui.\n"+
		"  · confidence = observed (visto diretamente na saída) | inferred (inferido a partir dos fenômenos).\n"+
		"  · **Conclusões negativas** (não injetável/porta fechada/nenhuma entrada encontrada etc.) escreva apenas \"observação + leitura provisória\" — declare o que você realmente viu; se a direção deve ser abandonada, quem decide é o planejador, sintetizando o quadro global; sempre forneça evidence, e quando os meios não foram esgotados ou a evidência é fraca (incluindo explorado uma única vez, parece que sim), marque inferred; só marque observed quando de fato esgotado e visto diretamente.",
		obj(map[string]any{
			"facts":      map[string]any{"type": "array", "description": "[Use quando houver várias conclusões distintas] Array de fatos, com os campos de cada elemento iguais aos de nível superior abaixo (summary/detail/evidence/confidence/intent_id/asset_ids); se intent_id for omitido, usa o intent_id de nível superior. O retorno ids tem o mesmo tamanho e ordem deste array.", "items": map[string]any{"type": "object"}},
			"summary":    str("[Frase de resumo] da conclusão desta exploração (é a síntese de detail)"),
			"intent_id":  idp("id da intenção que produziu este fato (a intenção que você recebeu; em lote, serve de padrão para cada item)"),
			"detail":     str("Detalhes relevantes deste fato: escreva aqui todos os fatos observados nesta exploração"),
			"evidence":   str("[Uma linha] de evidência-chave: comando + uma ou duas linhas de saída que melhor comprovam a conclusão. Seja conciso, não cole saídas longas (os detalhes vão em detail)."),
			"confidence": str("observed (visto diretamente na saída) | inferred (inferido a partir dos fenômenos). Marque fielmente as conclusões negativas."),
			"asset_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "ids de ativos relacionados (opcional, 0/1/vários): quais ativos este fato envolve"},
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Facts    []factItem `json:"facts"`
				factItem            // modo único + intent_id padrão em lote
			}
			_ = json.Unmarshal(in, &a)
			batch := len(a.Facts) > 0
			items := a.Facts
			if !batch {
				items = []factItem{a.factItem}
			}
			defaultIntent := pid(a.factItem.IntentID) // intent_id de nível superior = padrão em lote

			ids := make([]int64, len(items))
			errs := map[string]string{}
			for i, it := range items {
				id, err := t.recordOneFact(it, defaultIntent)
				if err != nil {
					errs[strconv.Itoa(i)] = err.Error()
					continue
				}
				ids[i] = id
			}

			if !batch { // único: mantém o retorno original
				if e, bad := errs["0"]; bad {
					return actool.Errorf(e), nil
				}
				return actool.Text(fmt.Sprintf("fact recorded: %d", ids[0])), nil
			}
			out := map[string]any{"ids": ids}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		})
}

type hintItem struct {
	Text        string            `json:"text"`
	AssetIDs    []json.RawMessage `json:"asset_ids"`
	TrafficRefs []db.TrafficRef   `json:"traffic_refs"`
}

// addOneHint pendura um nó hint (active/human) no grafo de exploração, podendo ancorar ativos, e retorna o id.
func (t *ToolSet) addOneHint(it hintItem) (int64, error) {
	if len(it.TrafficRefs) > 0 && !findingTrafficBindingEnabled() {
		return 0, fmt.Errorf("O vínculo automático de tráfego pelo Agent está desativado; o hint com traffic_refs não foi salvo; ative-o nas configurações do sistema ou repasse apenas o texto")
	}
	if strings.TrimSpace(it.Text) == "" {
		return 0, fmt.Errorf("text não pode ser vazio")
	}
	var anchors []int64
	for _, raw := range it.AssetIDs {
		if tid := pid(raw); tid > 0 {
			anchors = append(anchors, tid)
		}
	}
	refs, err := db.NormalizeTrafficRefs(it.TrafficRefs)
	if err != nil {
		return 0, err
	}
	payload := map[string]any{"text": it.Text}
	if len(refs) > 0 {
		payload["traffic_refs"] = refs
	}
	// Despertar o planner não é feito aqui item a item — addHint dispara uma única vez depois de gravar o lote inteiro (levando o texto dos hints),
	// evitando que um add_hint com várias dicas inunde a linha de gatilhos do planner item a item.
	return t.ts.AddNode(db.KindHint, payload, 0, "active", "human", anchors)
}

type goalItem struct {
	Text      string `json:"text"`
	VulnClass string `json:"vulnclass"`
}

// addOneGoal pendura um nó goal (open) no grafo de exploração: conecta à raiz da tarefa (origin fact, rel spawns).
// origin usa t.worker (padrão system): o que o decompositor de objetivos grava é registrado como "goals", o agent principal em execução como
// "human". O despertar do planner é feito por setGoals depois de gravar o lote inteiro (ver abaixo); aqui só se cuida de gravar no banco.
func (t *ToolSet) addOneGoal(it goalItem) (int64, error) {
	text := strings.TrimSpace(it.Text)
	if text == "" {
		return 0, fmt.Errorf("text não pode ser vazio")
	}
	payload := map[string]any{"text": text}
	if vc := strings.TrimSpace(it.VulnClass); vc != "" {
		payload["vulnclass"] = vc
	}
	origin := t.worker
	if origin == "" {
		origin = "system"
	}
	id, err := t.ts.AddNode(db.KindGoal, payload, 0, "open", origin, nil)
	if err != nil {
		return 0, err
	}
	if of, _ := t.ts.OriginFactID(); of > 0 && id > 0 {
		_ = t.ts.Link(of, db.RelSpawns, id) // goals descend from the task root (origin fact)
	}
	return id, nil
}

// setGoals adiciona objetivos de exploração (nós goal) à [tarefa atual]. É tanto a ferramenta de envio do decompositor de objetivos quanto a do agent
// principal para complementar objetivos em execução — a mesma ferramenta gerenciada, cuja descrição/schema podem ser alterados no web e vinculados por agent.
func (t *ToolSet) setGoals() actool.CoreTool {
	return writeTool("set_goals",
		"Adiciona objetivos de exploração (goal) à [tarefa atual]. Objetivo = resultado final entregável/verificável, não passo de ataque nem ação de reconhecimento.\n"+
			"★Prefira lote: coloque vários objetivos no array goals e envie de uma vez; o retorno ids tem o mesmo tamanho e ordem (item com falha tem id=0, detalhes em errors). Para um único item, omita goals e dê text no nível superior.\n"+
			"vulnclass é opcional: a classe de vulnerabilidade correspondente (como SQLi/IDOR); deixe vazio para objetivos de lógica de negócio. Se o objetivo foi atingido é julgado e marcado como met pelo sistema; esta ferramenta só adiciona.",
		obj(map[string]any{
			"goals":     map[string]any{"type": "array", "description": "[Prefira este] Array de novos objetivos, processados em ordem. Cada elemento: text (obrigatório, um objetivo final independente e verificável) + vulnclass (opcional). O retorno ids tem o mesmo tamanho e ordem deste array.", "items": map[string]any{"type": "object"}},
			"text":      str("[Único] Um objetivo final independente e verificável"),
			"vulnclass": str("[Único] Classe de vulnerabilidade correspondente (se explícita), como SQLi/IDOR; objetivos de lógica de negócio podem ficar vazios"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.ts == nil {
				return actool.Errorf("set_goals não está habilitado: ExplorationStore não inicializado"), nil
			}
			var a struct {
				Goals    []goalItem `json:"goals"`
				goalItem            // modo único: text/vulnclass de nível superior
			}
			_ = json.Unmarshal(in, &a)
			batch := len(a.Goals) > 0
			items := a.Goals
			if !batch {
				items = []goalItem{a.goalItem}
			}

			ids := make([]int64, len(items))
			errs := map[string]string{}
			var addedTexts []string
			for i, it := range items {
				id, err := t.addOneGoal(it)
				if err != nil {
					errs[strconv.Itoa(i)] = err.Error()
					continue
				}
				ids[i] = id
				addedTexts = append(addedTexts, strings.TrimSpace(it.Text))
			}
			if len(addedTexts) > 0 {
				// Despertar o planner (uma vez por lote). Prefira notifyGoal: um set_goals registra um gatilho "a pessoa adicionou
				// N objetivos:…", sem inundar item a item; o decompositor/worker não tem este callback → volta ao notify simples (o decompositor
				// no round-0 nem sequer conectou o notify, ou seja, no-op, pois nesse momento o planner ainda não iniciou).
				switch {
				case t.notifyGoal != nil:
					t.notifyGoal(addedTexts)
				case t.notify != nil:
					t.notify()
				}
				// O agent principal adicionou objetivo em execução → puxa de volta a running a tarefa concluída/pausada para continuar (o portão de estado terminal
				// engoliria um notify comum, é preciso reviver explicitamente). Só o mainagent conectou este callback; decompositor/worker são nil.
				if t.resumeTask != nil {
					t.resumeTask()
				}
			}

			if !batch { // único: mantém o retorno original
				if e, bad := errs["0"]; bad {
					return actool.Errorf(e), nil
				}
				return actool.Text(fmt.Sprintf("goal added: %d", ids[0])), nil
			}
			out := map[string]any{"ids": ids}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		})
}

type constraintItem struct {
	Text string `json:"text"`
	Type string `json:"type"` // allow | deny
}

// addOneConstraint grava uma restrição de operação em task_constraints. origin usa t.worker (padrão system):
// o decompositor grava "goals", o agent principal grava "human".
func (t *ToolSet) addOneConstraint(it constraintItem) (int64, error) {
	text := strings.TrimSpace(it.Text)
	if text == "" {
		return 0, fmt.Errorf("text não pode ser vazio")
	}
	kind := strings.TrimSpace(strings.ToLower(it.Type))
	if kind == "" {
		kind = "deny" // por padrão trata como proibição: sem tipo informado, é mais conservador
	}
	if kind != "allow" && kind != "deny" {
		return 0, fmt.Errorf("type deve ser allow ou deny")
	}
	return t.ts.AddConstraint(kind, text, t.worker)
}

// setConstraints adiciona restrições de operação à [tarefa atual] (allow = o que é permitido fazer / deny = o que é proibido fazer). É tanto a ferramenta de envio do decompositor de
// objetivos para extrair restrições no round-0 quanto a do agent principal para complementar restrições em execução — a mesma ferramenta gerenciada, cuja descrição/schema podem ser alterados no web
// e vinculados por agent. As restrições são injetadas no prompt do sistema do planner/worker para limitar a fronteira de exploração.
func (t *ToolSet) setConstraints() actool.CoreTool {
	return writeTool("set_constraints",
		"Adiciona restrições de operação à [tarefa atual], usadas para delimitar a fronteira de exploração: type=allow (operações permitidas) ou deny (operações proibidas).\n"+
			"Restrição = determinação sobre 'quais operações podem/não podem ser feitas' (como 'testar apenas a porta atual, sem escanear outras portas', 'proibido operações de escrita no banco de produção', 'só reconhecimento passivo'), não é objetivo nem passo de ataque.\n"+
			"★Prefira lote: coloque várias no array constraints e envie de uma vez; o retorno ids tem o mesmo tamanho e ordem (item com falha tem id=0, detalhes em errors). Para um único item, omita constraints e dê text/type no nível superior.\n"+
			"Registre apenas as restrições [explicitamente escritas] no objetivo/descrição da tarefa, sem inventar; na dúvida sobre o tipo, use deny (mais conservador).",
		obj(map[string]any{
			"constraints": map[string]any{"type": "array", "description": "[Prefira este] Array de novas restrições, processadas em ordem. Cada elemento: text (obrigatório, uma restrição) + type (allow|deny). O retorno ids tem o mesmo tamanho e ordem deste array.", "items": map[string]any{"type": "object"}},
			"text":        str("[Único] Conteúdo de uma restrição de operação"),
			"type":        str("[Único] allow (permitir) ou deny (proibir); se omitido, trata como deny"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.ts == nil {
				return actool.Errorf("set_constraints não está habilitado: ExplorationStore não inicializado"), nil
			}
			var a struct {
				Constraints    []constraintItem `json:"constraints"`
				constraintItem                  // modo único: text/type de nível superior
			}
			_ = json.Unmarshal(in, &a)
			batch := len(a.Constraints) > 0
			items := a.Constraints
			if !batch {
				items = []constraintItem{a.constraintItem}
			}
			ids := make([]int64, len(items))
			errs := map[string]string{}
			for i, it := range items {
				id, err := t.addOneConstraint(it)
				if err != nil {
					errs[strconv.Itoa(i)] = err.Error()
					continue
				}
				ids[i] = id
			}
			if !batch { // único: mantém retorno simples
				if e, bad := errs["0"]; bad {
					return actool.Errorf(e), nil
				}
				return actool.Text(fmt.Sprintf("constraint added: %d", ids[0])), nil
			}
			out := map[string]any{"ids": ids}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		})
}

func (t *ToolSet) addHint() actool.CoreTool {
	return t.writeExpTool("add_hint", "Pendura no grafo de exploração uma dica estratégica de humanos/agent principal, que o planejador lerá na próxima vez que gerar intenções.\n"+
		"★Prefira lote: coloque várias dicas no array hints e envie de uma vez (poupa idas e vindas em relação a chamar uma a uma). Retorna o array ids, com o mesmo tamanho e ordem de hints (item com falha tem id=0, detalhes em errors). Para um único item, omita hints e dê text no nível superior.",
		obj(map[string]any{
			"hints":        map[string]any{"type": "array", "description": "[Prefira este] Array de novas dicas, processadas em ordem. Os campos de cada elemento são os mesmos dos campos de nível superior abaixo (text/asset_ids/traffic_refs). O retorno ids tem o mesmo tamanho e ordem deste array.", "items": obj(map[string]any{"text": str("Conteúdo da dica"), "asset_ids": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}, "traffic_refs": HintTrafficSchema()})},
			"text":         str("[Único] Conteúdo da dica, como 'focar nas interfaces pós-autenticação'"),
			"traffic_refs": HintTrafficSchema(),
			"asset_ids":    map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "ids dos ativos ancorados (opcional, 0/1/vários)"},
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Hints    []hintItem `json:"hints"`
				hintItem            // modo único: text/asset_ids de nível superior
			}
			_ = json.Unmarshal(in, &a)
			batch := len(a.Hints) > 0
			items := a.Hints
			if !batch {
				items = []hintItem{a.hintItem}
			}

			ids := make([]int64, len(items))
			errs := map[string]string{}
			var addedTexts []string
			for i, it := range items {
				id, err := t.addOneHint(it)
				if err != nil {
					errs[strconv.Itoa(i)] = err.Error()
					continue
				}
				ids[i] = id
				addedTexts = append(addedTexts, strings.TrimSpace(it.Text))
			}
			if len(addedTexts) > 0 {
				// Despertar o planner (uma vez por lote). Prefira notifyHint: um add_hint registra um gatilho "a pessoa adicionou
				// N dicas estratégicas:…", para que o planner saiba claramente que "esta rodada foi disparada por um novo hint" e veja o conteúdo da dica;
				// sem esse callback, volta ao notify simples (bare wake, o hint continua dobrado no grafo para ele ler por conta própria).
				switch {
				case t.notifyHint != nil:
					t.notifyHint(addedTexts)
				case t.notify != nil:
					t.notify()
				}
			}

			if !batch { // único: mantém o retorno original
				if e, bad := errs["0"]; bad {
					return actool.Errorf(e), nil
				}
				return actool.Text(fmt.Sprintf("hint added: %d", ids[0])), nil
			}
			out := map[string]any{"ids": ids}
			if len(errs) > 0 {
				out["errors"] = errs
			}
			return jsonResult(out)
		})
}

// killWorkTool lets the planner terminate a single running work (by intent id).
func (t *ToolSet) killWorkTool() actool.CoreTool {
	return t.writeExpTool("kill_work", "Encerra uma intenção (work) em execução. Usada para interromper explorações desviadas/sem sentido; a intenção encerrada é marcada como stopped e não é reivindicada automaticamente de novo. Primeiro use get_worker_output para ver o que ela está fazendo e só então decida.",
		obj(map[string]any{"intent_id": idp("id da intenção a encerrar (= handle do work)")}, "intent_id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.killWork == nil {
				return actool.Errorf("kill_work não está disponível no momento"), nil
			}
			var a struct {
				IntentID json.RawMessage `json:"intent_id"`
			}
			_ = json.Unmarshal(in, &a)
			id := pid(a.IntentID)
			if id <= 0 {
				return actool.Errorf("intent_id é obrigatório"), nil
			}
			node, err := t.ts.GetNode(id)
			if err != nil || node == nil || node.Kind != db.KindIntent {
				return actool.Errorf("intent_id deve ser uma intenção desta tarefa (intenções de tarefas associadas são somente leitura)"), nil
			}
			if err := t.killWork(id); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text(fmt.Sprintf("Sinal de encerramento enviado ao work da intenção %d", id)), nil
		})
}

// steerWorkTool lets the planner inject a mid-run course-correction into a running
// work WITHOUT killing it: the message reaches the worker before its next tool call,
// which re-plans its next step (already-gathered context is kept). For in-intent
// nudges ("pare de fazer X, foque em Y"); if the whole direction is wrong use kill_work + a new intent.
func (t *ToolSet) steerWorkTool() actool.CoreTool {
	return t.writeExpTool("steer_work", "Injeta em tempo real uma instrução de correção de rumo numa intenção (work) em execução, sem interrompê-la nem perder o progresso existente: o worker recebe a sua instrução antes da próxima ação e se ajusta de acordo. Usada para correções [dentro da intenção] do tipo 'não siga X, foque em Y'; se a direção estiver totalmente errada, use kill_work e depois uma nova intenção. Recomenda-se primeiro usar get_worker_output para ver o que ela está fazendo.",
		obj(map[string]any{
			"intent_id": idp("id da intenção a corrigir (= handle do work)"),
			"message":   str("Instrução de correção para o worker, deixando claro o que ele deve parar e para onde deve virar"),
		}, "intent_id", "message"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.steerWork == nil {
				return actool.Errorf("steer_work não está disponível no momento"), nil
			}
			var a struct {
				IntentID json.RawMessage `json:"intent_id"`
				Message  string          `json:"message"`
			}
			_ = json.Unmarshal(in, &a)
			id := pid(a.IntentID)
			if id <= 0 {
				return actool.Errorf("intent_id é obrigatório"), nil
			}
			node, err := t.ts.GetNode(id)
			if err != nil || node == nil || node.Kind != db.KindIntent {
				return actool.Errorf("intent_id deve ser uma intenção desta tarefa (intenções de tarefas associadas são somente leitura)"), nil
			}
			if strings.TrimSpace(a.Message) == "" {
				return actool.Errorf("message é obrigatório"), nil
			}
			if err := t.steerWork(id, a.Message); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text(fmt.Sprintf("Instrução de correção de rumo injetada no work da intenção %d (vale a partir do próximo passo)", id)), nil
		})
}

// getWorkerOutput returns a work's final (or até o momento da interrupção) conclusion text by intent id.
func (t *ToolSet) getWorkerOutput() actool.CoreTool {
	return t.readExpTool("get_worker_output", "Obtém a conclusão de saída final de uma intenção (work) desta tarefa ou de tarefas diretamente associadas. Os resultados de tarefas associadas trazem source_task_id/inherited=true e são somente leitura. Se terminou normalmente, retorna o seu resumo; para um work encerrado (stopped)/anormal, retorna a última saída até o momento da interrupção.",
		obj(map[string]any{"intent_id": idp("id da intenção (= handle do work)")}, "intent_id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				IntentID json.RawMessage `json:"intent_id"`
			}
			_ = json.Unmarshal(in, &a)
			id := pid(a.IntentID)
			if id <= 0 {
				return actool.Errorf("intent_id é obrigatório"), nil
			}
			intentNode, err := t.ts.GetNodeWithSources(id)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if intentNode == nil || intentNode.Kind != db.KindIntent {
				return actool.Errorf("intent_id não pertence a esta tarefa nem às tarefas diretamente associadas"), nil
			}
			acts, _, err := t.ts.ActivityListWithSources(id, 0, 1000)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			var chosen, fallback *db.Activity
			for i := range acts {
				switch acts[i].Kind {
				case "result":
					chosen = &acts[i]
					fallback = &acts[i]
				case "text":
					fallback = &acts[i]
				}
			}
			pick := chosen
			if pick == nil {
				pick = fallback
			}
			if pick == nil {
				if intentNode.Inherited {
					return jsonResult(inheritedMap(map[string]any{
						"intent_id": id, "final_text": "(este work ainda não tem nenhuma saída)",
					}, intentNode.SourceTaskID))
				}
				return actool.Text("(este work ainda não tem nenhuma saída)"), nil
			}
			detail, _ := t.ts.ActivityDetailWithSources(pick.ID)
			if detail == "" {
				detail = pick.Summary
			}
			result := map[string]any{
				"intent_id": id, "final_text": detail,
				"summary": pick.Summary, "is_error": pick.IsError,
			}
			if intentNode.Inherited {
				inheritedMap(result, intentNode.SourceTaskID)
			}
			return jsonResult(result)
		})
}

// traceSteps renders summary-only trace rows, re-truncating each summary to 100
// chars — the stored summary is capped at 200 for the UI transcript; the trace
// tools want it tighter since a whole work's step list is many rows.
func traceSteps(acts []db.Activity) []map[string]any {
	steps := make([]map[string]any, 0, len(acts))
	for i := range acts {
		step := map[string]any{
			"step_id": acts[i].ID, "kind": acts[i].Kind, "tool": acts[i].Tool,
			"is_error": acts[i].IsError, "summary": firstLine(acts[i].Summary, 100),
		}
		if acts[i].Inherited {
			inheritedMap(step, acts[i].SourceTaskID)
		}
		steps = append(steps, step)
	}
	return steps
}

// getWorkerTrace exposes a work's execution PROCESS (not just its final output):
// list step summaries, keyword-search within one work, or pull full detail of a
// few specific steps. Thinking steps are excluded everywhere.
func (t *ToolSet) getWorkerTrace() actool.CoreTool {
	return t.readExpTool("get_worker_trace",
		"Vê o [processo de execução] de uma intenção (work) (diferente de get_worker_output, que dá só a conclusão final). Três usos:\n"+
			"① Passe só intent_id → retorna o fluxo de resumos de cada passo do work (summary≤100 caracteres, com step_id; é apenas o contorno das ações, sem a saída completa);\n"+
			"② intent_id + q → retorna apenas os resumos dos passos que acertam a palavra-chave (pesquisa nos resumos e na saída completa; continua dando só summary, para ver o conteúdo use ③);\n"+
			"③ intent_id + step_ids → retorna o conteúdo completo (detail) desses passos; no máximo 5 por vez, o excedente retorna só os 5 primeiros e informa em notice/omitted_step_ids os não obtidos.\n"+
			"Fluxo típico: primeiro ①/② para localizar o step_id do passo suspeito, depois ③ para obter a saída completa. Não inclui passos de raciocínio (thinking). Suporta o trace histórico de tarefas diretamente associadas; seu resultado traz source_task_id/inherited=true e é somente leitura.",
		obj(map[string]any{
			"intent_id": idp("id da intenção (= handle do work)"),
			"q":         str("Palavra-chave: retorna apenas os passos cujo resumo/saída completa a acertam (opcional; exclusiva com step_ids)"),
			"step_ids":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "step_id cujo conteúdo completo se quer obter (vindos do retorno de ①/②; no máximo 5 por vez, se passar mais só os 5 primeiros são retornados e o restante é listado em omitted_step_ids)"},
			"limit":     intp("Limite de retorno do fluxo de resumos/busca (opcional)"),
		}, "intent_id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				IntentID json.RawMessage   `json:"intent_id"`
				Q        string            `json:"q"`
				StepIDs  []json.RawMessage `json:"step_ids"`
				Limit    int               `json:"limit"`
			}
			_ = json.Unmarshal(in, &a)
			id := pid(a.IntentID)
			if id <= 0 {
				return actool.Errorf("intent_id é obrigatório"), nil
			}
			intentNode, nodeErr := t.ts.GetNodeWithSources(id)
			if nodeErr != nil {
				return actool.Errorf(nodeErr.Error()), nil
			}
			if intentNode == nil || intentNode.Kind != db.KindIntent {
				return actool.Errorf("intent_id não pertence a esta tarefa nem às tarefas diretamente associadas"), nil
			}
			// ③ detail drill-down by step ids, thinking excluded by the store.
			if len(a.StepIDs) > 0 {
				// Dedup + drop invalid ids first so garbage/duplicates don't eat into
				// the per-call cap. detail is returned in full (untruncated), so the
				// cap bounds one tool result; over the cap we serve the first N and
				// tell the model exactly which ids were deferred, instead of erroring
				// and forcing it to re-plan the call.
				const maxStepIDs = 5
				var ids []int64
				seen := make(map[int64]bool)
				for _, raw := range a.StepIDs {
					if v := pid(raw); v > 0 && !seen[v] {
						seen[v] = true
						ids = append(ids, v)
					}
				}
				var omitted []int64
				if len(ids) > maxStepIDs {
					omitted = append(omitted, ids[maxStepIDs:]...)
					ids = ids[:maxStepIDs]
				}
				acts, err := t.ts.ActivityByIDsWithSources(ids)
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				steps := make([]map[string]any, 0, len(acts))
				for i := range acts {
					if acts[i].NodeID == nil || *acts[i].NodeID != id || acts[i].Inherited != intentNode.Inherited ||
						(acts[i].Inherited && acts[i].SourceTaskID != intentNode.SourceTaskID) {
						continue
					}
					step := map[string]any{
						"step_id": acts[i].ID, "kind": acts[i].Kind, "tool": acts[i].Tool,
						"is_error": acts[i].IsError, "detail": acts[i].Detail,
					}
					if acts[i].Inherited {
						inheritedMap(step, acts[i].SourceTaskID)
					}
					steps = append(steps, step)
				}
				result := map[string]any{"intent_id": id, "steps": steps, "returned_step_ids": ids}
				if len(omitted) > 0 {
					// returned_step_ids/omitted_step_ids let the model decide programmatically
					// whether another call is worth it; the notice states the same in prose.
					result["omitted_step_ids"] = omitted
					result["notice"] = fmt.Sprintf(
						"No máximo %d passos têm o conteúdo completo obtido por vez; desta vez os primeiros %d foram retornados (%v), e os %d não obtidos são %v."+
							"Se estes conteúdos já bastam para localizar, não é preciso obter os passos restantes; se de fato precisar continuar, chame de novo com estes step_id.",
						maxStepIDs, len(ids), ids, len(omitted), omitted)
				}
				if intentNode.Inherited {
					inheritedMap(result, intentNode.SourceTaskID)
				}
				return jsonResult(result)
			}
			// ①/② summary stream, optionally keyword-filtered; 100-char summaries.
			var acts []db.Activity
			var err error
			if strings.TrimSpace(a.Q) != "" {
				acts, err = t.ts.ActivityTraceSearchWithSources(id, a.Q, a.Limit)
			} else {
				acts, err = t.ts.ActivityTraceWithSources(id, a.Limit)
			}
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			result := map[string]any{"intent_id": id, "steps": traceSteps(acts)}
			if intentNode.Inherited {
				inheritedMap(result, intentNode.SourceTaskID)
			}
			return jsonResult(result)
		})
}

// searchAllWorkerTraces keyword-searches EVERY work's process in this task — for
// finding what a worker saw but never wrote back as a fact. Returns only matching
// summaries (≤100 chars), each tagged with its intent_id for follow-up drill-down.
func (t *ToolSet) searchAllWorkerTraces() actool.CoreTool {
	return t.readExpTool("search_all_worker_traces",
		"[Normalmente não recomendada, pois o sistema já deu a maior parte das informações] Busca por palavra-chave (q) no [processo de execução de outros works desta tarefa] — usada para recuperar algo que um worker viu mas não escreveu em fact (algum caminho/token/erro etc.)."+
			"Os passos da sua própria intenção já são excluídos automaticamente (eles já estão no seu contexto)."+
			"Retorna só os resumos dos passos que acertam (summary≤100 caracteres), cada um com intent_id; com isso, use get_worker_trace(intent_id, step_ids=[...]) para obter o conteúdo completo.",
		obj(map[string]any{
			"q":     str("Palavra-chave (pesquisada nos resumos + saída completa de todos os passos de work)"),
			"limit": intp("Limite de retorno, padrão 100 (opcional)"),
		}, "q"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Q     string `json:"q"`
				Limit int    `json:"limit"`
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Q) == "" {
				return actool.Errorf("q é obrigatório"), nil
			}
			// exclui os passos da própria intenção do chamador (o trace próprio do worker já está no contexto dele)
			acts, err := t.ts.ActivityTraceSearchAllWithSources(t.ownerNode, a.Q, a.Limit)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			hits := make([]map[string]any, 0, len(acts))
			for i := range acts {
				var intent int64
				if acts[i].NodeID != nil {
					intent = *acts[i].NodeID
				}
				hit := map[string]any{
					"intent_id": intent, "step_id": acts[i].ID, "worker": acts[i].Worker,
					"kind": acts[i].Kind, "tool": acts[i].Tool, "is_error": acts[i].IsError,
					"summary": firstLine(acts[i].Summary, 100),
				}
				if acts[i].Inherited {
					inheritedMap(hit, acts[i].SourceTaskID)
				}
				hits = append(hits, hit)
			}
			return jsonResult(map[string]any{"query": a.Q, "hits": hits})
		})
}

// listWorkerTraces gives a worker (which has no graph_overview and can't see the
// intent graph) a lightweight index of the works in this task — intent_id +
// one-line summary + state — so it can DISCOVER which works to inspect via
// get_worker_trace. Without this a worker only knows intent_ids that come back
// from search_all_worker_traces hits. Excludes still-open intents (not yet run →
// no process to inspect).
func (t *ToolSet) listWorkerTraces() actool.CoreTool {
	return t.readExpTool("list_worker_traces",
		"[Normalmente não recomendada, pois o sistema já deu a maior parte das informações] Lista o índice dos [works (intenções) já executados] nesta tarefa: intent_id + direção em uma frase (summary) + estado."+
			"Você (worker) não enxerga o grafo de exploração; use-a para descobrir quais works valem ser consultados — depois use get_worker_trace(intent_id) para ver seus passos e get_worker_trace(intent_id, step_ids=[...]) para obter detalhes."+
			"Lista apenas os já executados (running/done/exhausted/blocked/stopped), sem os open que ainda não rodaram. Atenção: o seu limite de tarefa continua sendo a intenção que você recebeu; olhar outros works serve apenas para reaproveitar observações/evitar trabalho repetido.",
		obj(map[string]any{
			"q":     str("Filtra por palavra-chave do summary (opcional)"),
			"limit": intp("Limite de retorno, padrão 50 (opcional)"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct {
				Q     string `json:"q"`
				Limit int    `json:"limit"`
			}
			_ = json.Unmarshal(in, &a)
			limit := a.Limit
			if limit <= 0 {
				limit = 50
			}
			all, err := t.ts.ListByKindWithSources(db.KindIntent, 500)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			q := strings.ToLower(strings.TrimSpace(a.Q))
			out := make([]map[string]any, 0, limit)
			for _, n := range all {
				if n.Inherited && n.State == "running" {
					continue
				}
				switch n.State {
				case "running", "done", "exhausted", "blocked", "stopped": // has run → has a process
				default:
					continue
				}
				var p map[string]any
				_ = json.Unmarshal(n.Payload, &p)
				summary, _ := p["summary"].(string)
				if q != "" && !strings.Contains(strings.ToLower(summary), q) {
					continue
				}
				item := map[string]any{"intent_id": n.ID, "summary": summary, "state": n.State}
				if n.Inherited {
					inheritedMap(item, n.SourceTaskID)
				}
				out = append(out, item)
				if len(out) >= limit {
					break
				}
			}
			return jsonResult(map[string]any{"works": out})
		})
}

// PlannerTools is the read + intent-generation + goal-judgement tool set.
func (t *ToolSet) PlannerTools() []actool.CoreTool {
	return []actool.CoreTool{
		t.graphOverview(), t.listFindings(), t.listFacts(), t.nodeDetail(),
		// cold-digest §6.1: restore folded cold nodes (digest body → members → detail).
		t.expandDigest(),
		t.getWorkerOutput(), t.getWorkerTrace(), t.searchAllWorkerTraces(), t.listGoals(), t.addIntent(), t.proveGoal(), t.goalMet(),
		t.killWorkTool(), t.steerWorkTool(),
		// report_finding: se, na análise da situação durante o planejamento, você mesmo já tiver uma vulnerabilidade comprovada, pode registrá-la diretamente (mesma ferramenta do worker).
		t.addFinding(),
		// list_companies: ver a lista de empresas + scope + quantidade de ativos (para obter company_id / entender o escopo de pertencimento).
		t.listCompanies(),
		// list_assets: durante o planejamento, busca em todo o banco de ativos por DSL (junto com a visão "não testado dentro do escopo" de list_untested_assets,
		// acrescenta a capacidade de "consultar no banco inteiro por condições como domínio/fingerprint/porta/código de status").
		t.listAssets(),
		// add_company_scope: durante o planejamento, pode incluir domínio/IP/CIDR/ICP/palavra-chave no escopo de ativos de uma empresa (reivindica automaticamente os ativos atingidos).
		t.addCompanyScope(),
		// add_task_scope: inclui ativamente domínio raiz inteiro/empresa inteira/algum subdomínio/IP no escopo de teste desta tarefa (denominador da cobertura).
		t.addTaskScope(),
		// list_untested_assets: consulta sob demanda os ativos não testados dentro do escopo desta tarefa (tipo + paginação), decidindo por conta própria o que testar adicionalmente.
		t.listUntestedAssets(),
	}
}
