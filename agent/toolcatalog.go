package agent

import (
	"context"
	"encoding/json"

	actool "github.com/Autumn-27/norma/tool"
)

// Este arquivo transforma as "ferramentas embutidas" de puro código em um catálogo enumerável e sobrescrevível pelo DB:
//   - BuiltinToolSeeds(): expande os conjuntos de ferramentas embutidas dos três agents de execução em registros seed (key +
//     descrição + schema de parâmetros + agents vinculados por padrão), para o servidor semear de forma idempotente na tabela tools na inicialização.
//   - Hook ToolResolve: em tempo de execução, conforme as linhas da tabela tools no DB, aplica às ferramentas já montadas "filtro por agent +
//     sobrescrita de descrição/schema + injeção de valores padrão de parâmetros". key/handler continuam na camada de código; o DB só altera "a prosa e os valores padrão".
// O handler (comportamento do Call) vem sempre do código — o DB não pode alterá-lo, só a descrição que o modelo vê e os parâmetros de entrada padrão.

// ToolSeed é um snapshot semeável de uma ferramenta embutida: key é o CoreTool.Name() (amarrado ao handler,
// somente leitura na UI), Desc/Schema vêm da definição da ferramenta no código, Agents são os agents aos quais o código a atribui por padrão.
type ToolSeed struct {
	Key    string         // = CoreTool.Name(), chave primária, imutável
	Desc   string         // descrição de nível superior (pode ser sobrescrita na UI)
	Schema map[string]any // JSON-Schema dos parâmetros (estrutura somente leitura; description/default podem ser alterados na UI)
	Agents []string       // keys dos agents vinculados por padrão (worker/planner/mainagent)
}

// builtinToolsByAgent constrói o conjunto de ferramentas de domínio de cada agent de execução usando um ToolSet "casca somente leitura" (stores nil).
// Os construtores de ferramenta só colocam closures no Spec e não desreferenciam o store na construção, então é seguro com nil —
// aqui essas ferramentas servem apenas para ler Name()/Description()/InputSchema(), e jamais para Call.
//
// Deliberadamente [não inclui] as ferramentas genéricas do SDK actool.DefaultTools() (Read/Write/Edit/MultiEdit/LS/Glob/
// Grep/Bash): todo agent as possui de forma fixa, sem a escolha de "vincular a quem", e a maior parte da explicação está em Prompt()
// (esta tabela só cobre Description(), o que causaria uma cobertura parcial enganosa). Sem seed → sem linha no DB → ToolResolve
// deixa passar como está, sem sobrescrever, e o comportamento é o mesmo de antes. Apenas as ferramentas de domínio do próprio artex entram na tabela e podem ser gerenciadas.
func builtinToolsByAgent() map[string][]actool.CoreTool {
	ts := NewToolSet(nil, "")
	return map[string][]actool.CoreTool{
		"mainagent": ts.MainAgentTools(),
		"planner":   ts.PlannerTools(),
		"worker":    ts.WorkerTools(),
		// goals (decompositor de objetivos) vincula por padrão set_goals + set_constraints: é por elas que os objetivos decompostos e
		// as restrições de operação extraídas são gravados no banco. Compartilha com o mainagent a mesma ferramenta gerenciada; no web é possível alterar descrição/schema e marcar por agent.
		"goals": {ts.setGoals(), ts.setConstraints()},
		// auto vincula por padrão ferramentas de reporte de vulnerabilidades + gestão de ativos; outras ferramentas de domínio podem ser marcadas na UI conforme necessário.
		// Em bancos novos, o seed grava daqui; em bancos antigos, seedAutoDefaultBindings migra.
		"auto": {ts.addFinding(), ts.insertAssets(), ts.addCompanyScope(), ts.listAssets(), ts.listCompanies()},
		// pentest (agent de intrusão independente) vincula por padrão: consultar ativos / inserir ativos / reportar vulnerabilidades / consultar vulnerabilidades / consultar empresas.
		// Em bancos novos, o seed grava daqui; em bancos antigos, seedPentestDefaultBindings migra.
		"pentest": {ts.listAssets(), ts.insertAssets(), ts.addFinding(), ts.listFindings(), ts.listCompanies()},
	}
}

// defaultUnbound: estas ferramentas de sistema entram normalmente no catálogo (visíveis no web, podem ser marcadas manualmente por agent), mas
// por padrão [não são vinculadas a nenhum agent] — o ToolResolve descarta, para todos os agents, as ferramentas com vínculo vazio, exigindo opt-in explícito.
// O motivo de ainda ficarem no conjunto base de algum agent (como goal_met em PlannerTools): primeiro, para que o seed consiga
// construí-la e obter desc/schema; segundo, para que, depois de o usuário revinculá-la manualmente, ela exista no base em tempo de execução e o ToolResolve consiga mantê-la.
//
// goal_met: contorna prove_goal um a um e declara globalmente [a tarefa inteira concluída]; tem peso grande e risco de julgamento errado, e ainda duplica
// "prove_goal marcando o último objetivo → encerramento automático"; por isso por padrão não é dada a nenhum agent, e só se vincula manualmente quando necessário.
var defaultUnbound = map[string]bool{"goal_met": true}

// BuiltinToolSeeds deduplica e une os conjuntos de ferramentas embutidas dos agents em uma lista de seed: ferramentas de mesmo nome (como list_assets,
// presente em vários agents) viram uma só, com Agents tomando a união; as ferramentas em defaultUnbound são forçadas a vínculo vazio.
func BuiltinToolSeeds() []ToolSeed {
	byAgent := builtinToolsByAgent()
	order := []string{"mainagent", "goals", "planner", "worker", "auto", "pentest"}

	type acc struct {
		tool   actool.CoreTool
		agents []string
	}
	m := map[string]*acc{}
	var keys []string
	for _, ak := range order {
		for _, t := range byAgent[ak] {
			a, ok := m[t.Name()]
			if !ok {
				a = &acc{tool: t}
				m[t.Name()] = a
				keys = append(keys, t.Name())
			}
			a.agents = append(a.agents, ak)
		}
	}

	out := make([]ToolSeed, 0, len(keys))
	for _, k := range keys {
		a := m[k]
		agents := a.agents
		if defaultUnbound[k] {
			agents = []string{} // entra no catálogo, pode ser vinculada manualmente, mas por padrão não é dada a nenhum agent (grava [] e não null, como as demais ferramentas)
		}
		out = append(out, ToolSeed{
			Key:    k,
			Desc:   a.tool.Description(),
			Schema: a.tool.InputSchema(),
			Agents: agents,
		})
	}
	return out
}

// ToolResolve, if set, post-processes an agent's fully-assembled tool list against
// the DB tools table: it drops tools not bound to this agent (or globally disabled)
// and wraps the rest so the model sees the DB-overridden description/schema and
// parâmetros de entrada padrão get injected. Tools with no matching DB row (MCP/skill/host tools like
// traffic) pass through untouched. nil = tools unchanged. Wired in server/assembly.go.
var ToolResolve func(ctx context.Context, agentKey string, tools []actool.CoreTool) []actool.CoreTool

// DecorateTool wraps t so Description()/InputSchema() report the DB overrides and
// Call() injects scalar parameter defaults (from schema's "default" props) whenever
// the model omitted them. Name/Prompt/permission/scheduler flags delegate to t, so
// the tool's identity and handler are unchanged. Empty desc/schema fall back to t's.
func DecorateTool(t actool.CoreTool, desc string, schema map[string]any) actool.CoreTool {
	if desc == "" {
		desc = t.Description()
	}
	if len(schema) == 0 {
		schema = t.InputSchema()
	}
	return &overriddenTool{CoreTool: t, desc: desc, schema: schema}
}

// overriddenTool is a CoreTool decorator: it embeds the original (so all behavioral
// methods — Prompt/IsReadOnly/IsConcurrencySafe/CheckPermissions/Name — delegate)
// and overrides only the model-facing description/schema plus default injection.
type overriddenTool struct {
	actool.CoreTool
	desc   string
	schema map[string]any
}

func (o *overriddenTool) Description() string         { return o.desc }
func (o *overriddenTool) InputSchema() map[string]any { return o.schema }

func (o *overriddenTool) Call(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	return o.CoreTool.Call(ctx, injectDefaults(in, o.schema), tc)
}

// injectDefaults fills scalar parameter defaults declared in the (possibly edited)
// schema into the input JSON whenever the model omitted the field or left it empty/
// null. Structure (names/types/required) is untouched — only valores padrão are merged in.
func injectDefaults(in json.RawMessage, schema map[string]any) json.RawMessage {
	defs := scalarDefaults(schema)
	if len(defs) == 0 {
		return in
	}
	m := map[string]json.RawMessage{}
	if len(in) > 0 {
		if err := json.Unmarshal(in, &m); err != nil {
			return in // non-object input: don't touch it
		}
	}
	changed := false
	for k, dv := range defs {
		if cur, ok := m[k]; !ok || isEmptyJSON(cur) {
			m[k] = dv
			changed = true
		}
	}
	if !changed {
		return in
	}
	b, err := json.Marshal(m)
	if err != nil {
		return in
	}
	return b
}

// scalarDefaults extracts properties[k]["default"] for scalar params (string/
// integer/number/boolean). Array/object defaults are skipped: merging them is
// ambiguous and not worth the surprise.
func scalarDefaults(schema map[string]any) map[string]json.RawMessage {
	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return nil
	}
	out := map[string]json.RawMessage{}
	for name, raw := range props {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		dv, ok := p["default"]
		if !ok || dv == nil {
			continue
		}
		switch p["type"] {
		case "string", "integer", "number", "boolean":
			if b, err := json.Marshal(dv); err == nil {
				out[name] = b
			}
		}
	}
	return out
}

func isEmptyJSON(raw json.RawMessage) bool {
	s := string(raw)
	return s == "null" || s == `""`
}
