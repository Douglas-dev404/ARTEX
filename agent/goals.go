package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	acperm "github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// goalsDefaultTmpl is the built-in EDITABLE body (seção [A]) of the goals-decomposer
// prompt, seeded into agent_prompts. No template vars are used today.
const goalsDefaultTmpl = `Você é o decompositor de objetivos de teste de intrusão. Sua responsabilidade é identificar, a partir da entrada do usuário, o **resultado final a ser alcançado**, e não planejar passos de ataque.

**Primeiro passo (faça antes de decompor os objetivos): extrair as restrições de operação**
A partir do "objetivo da tarefa / descrição da tarefa", identifique as determinações explícitas do operador sobre [quais operações podem e quais não podem ser feitas] e registre-as uma a uma chamando set_constraints (se a descrição e o objetivo não envolverem restrições de operação, não é necessário extraí-las):
- type=deny: operações proibidas (como "não escanear portas", "não executar operações de escrita/exclusão em ambiente de produção", "proibido força bruta", "não tocar em certo subdomínio").
- type=allow: escopo de operações explicitamente permitido/delimitado (como "apenas reconhecimento passivo", "somente contra certo domínio").
- Restrição ≠ objetivo, e também ≠ passo de ataque: é uma determinação sobre a fronteira do comportamento operacional.
- **A restrição deve ser [autocontida, com o alvo concreto escrito explicitamente]**: substitua os **termos referenciais** como "o alvo atual / a porta atual / o IP atual / o domínio atual / este site" pelos **valores concretos** do objetivo/descrição da tarefa. A restrição será injetada isoladamente no prompt da fase de execução e, fora do contexto, o termo referencial não permite saber a quem se refere.
  Exemplo: se o alvo é https://abc.example.net → escreva "só é permitido testar abc.example.net" e não "só é permitido testar o alvo atual"; "testar apenas a porta alvo 443, sem escanear outras portas" e não "testar apenas a porta atual". Se o texto original diz apenas "o alvo atual" mas o endereço do alvo já está claro, preencha o endereço.
- **Registre apenas as restrições [explicitamente escritas ou enfatizadas] no objetivo/descrição, é estritamente proibido inventar**; na dúvida sobre o tipo, use deny (mais conservador).
- Se o objetivo/descrição realmente não contém nenhuma restrição de operação, **não** chame set_constraints.
Depois de registrar as restrições (se houver), faça a decomposição de objetivos abaixo.

**Objetivo = resultado final entregável/verificável**

**O que não é objetivo (proibido listar como subobjetivo)**:
- Coleta de informações, reconhecimento, varredura de endpoints
- Processo de análise e validação de vulnerabilidades
- Passos de ataque, meios de exploração
- Passos de verificação de resultados

**Princípios de decomposição**:
- O objetivo final descrito pelo usuário é só um → produza um
- Existem vários entregáveis finais **independentes entre si** → liste-os separadamente
- Os que correspondem a uma classe de vulnerabilidade clara recebem vulnclass; objetivos de coleta de informações/lógica de negócio ficam em branco
- É estritamente proibido inventar objetivos que o usuário não mencionou

Chame set_goals para enviar o resultado.`

// goalsScopeTail is the code-owned tail appended after the editable goals body
// WHEN an asset store + task context are available. It teaches the decomposer to
// also lift the explicit asset scope out of the goal/description and register it
// via add_task_scope. Kept in code (not the DB-editable body) so it always applies
// on released DBs and can't be edited away — same pattern as the trafficTool tail.
const goalsScopeTail = `

**Responsabilidade adicional: registrar o escopo de ativos de teste**
Além de decompor os objetivos, você também deve identificar, a partir do "objetivo da tarefa / descrição da tarefa", o **escopo de ativos de teste explicitamente informado** e registrá-lo chamando add_task_scope (a fronteira de autorização desta tarefa, e também o denominador da cobertura de teste dos ativos). **Princípio do escopo mínimo: registre apenas o único alvo que o usuário apontou explicitamente, nunca amplie por conta própria.**
- Se o alvo é uma URL ou um endereço com nome de host (como https://xxx.example.com/path, app.example.com) → pegue o **nome de host completo**, kind=subdomain, value=nome de host completo.
  Exemplo: alvo https://a1b2c3.lab.example.net/path → kind=subdomain, value=a1b2c3.lab.example.net (**não** example.net).
  **É estritamente proibido** reduzir um nome de host com subdomínio ao domínio raiz — ao ver xxx.example.com, registrar example.com inteiro amplia o escopo para além do alvo do usuário, violando o princípio do escopo mínimo.
- Somente quando o usuário informar um **domínio raiz puro, sem nenhum subdomínio** (como escrever diretamente example.com), ou disser explicitamente "o site inteiro / todos os subdomínios / o domínio inteiro" → use kind=root_domain, value=example.com.
- IP puro ou faixa de rede → kind=ip / cidr, value=IP ou CIDR.
- **Não** registre o escopo de empresa (company) — a tarefa acabou de ser criada, geralmente o sistema de ativos ainda não tem essa empresa e não é possível registrá-la; o escopo em nível de empresa fica para a fase de plan posterior.
Outras regras:
- Registre apenas o escopo **explicitamente escrito no objetivo/descrição**; é estritamente proibido inventar ou inferir domínios/IPs não mencionados.
- reason resume em qual frase está o fundamento, para facilitar a auditoria.
- Se o objetivo/descrição não contém nenhum escopo de ativos explícito, **não** chame add_task_scope.
Registre primeiro o escopo com add_task_scope (se houver) e depois chame set_goals para enviar os objetivos.`

// GoalSpec is one decomposed objective.
type GoalSpec struct {
	Text      string `json:"text"`
	VulnClass string `json:"vulnclass,omitempty"`
}

// DecomposeGoals asks the LLM to break a pentest task goal into discrete,
// independently-verifiable objectives (each becomes a goal node). Returns nil if
// no provider is configured or the call yields nothing — the caller then falls
// back to a rule-based split so goal nodes always exist.
//
// prov is supplied by the caller (rather than built here from a Config) so goal
// decomposition rides the SAME provider instance as the rest of the engine — it
// shares the rate limiter, gets recorded by llmrec, and participates in LLM
// failover instead of quietly bypassing all three.
//
// desc is the task's free-text description (contexto: escopo do alvo/quantidade de flags/regras de engajamento etc.).
// It is fed alongside the goal so the decomposer no longer splits blind — the
// prompt still forbids inventing anything the two texts don't state.
//
// emit, when non-nil, receives every LLM step (thinking/tool_use/result) with
// Worker="planner" so the round-0 goal-decomposition activity is visible in the UI.
//
// as + taskID, when non-nil/positive, wire the add_task_scope tool so the
// decomposer can register the explicit asset scope it extracts from the goal.
//
// ts is the task's exploration store: set_goals writes the decomposed goal nodes
// straight into it (the same managed tool the main agent uses to add goals at
// runtime). The returned specs are read back from the store so callers can emit
// per-goal activity and detect the "LLM produced nothing" case for their fallback.
func DecomposeGoals(ctx context.Context, prov llm.Provider, dataDir, goalText, desc string, as *db.AssetStore, ts *db.ExplorationStore, taskID int64, emit func(db.Activity)) []GoalSpec {
	if prov == nil {
		return nil
	}
	return DecomposeGoalsWithProvider(ctx, prov, dataDir, goalText, desc, as, ts, taskID, false, 0, emit)
}

// DecomposeGoalsWithProvider is the task-runtime variant used when a task has an
// ordered provider chain. It preserves the same tools and write behavior while
// letting the caller own provider selection/failover. maxTokens is the profile's
// per-reply output cap (0 = send none).
func DecomposeGoalsWithProvider(ctx context.Context, prov llm.Provider, dataDir, goalText, desc string, as *db.AssetStore, ts *db.ExplorationStore, taskID int64, nonStreaming bool, maxTokens int, emit func(db.Activity)) []GoalSpec {
	if prov == nil {
		return nil
	}
	// A decomposição de objetivos é uma chamada única: não anexa o transcript store, portanto o agentcore não anexa
	// o session id ao ctx (ele só o anexa quando há um writer, ver agentcore.Prompt). Já os gateways que fazem
	// cache de prompt/roteamento sticky pelo cabeçalho session-id (o opencode zen devolve 400 direto sem x-opencode-session,
	// MissingSessionID) leem justamente esse valor no ctx — sem o complemento seria "a conversa funciona, a decomposição dá 400".
	// Anexa-se explicitamente um id estável: as requisições de decomposição da mesma exploração o compartilham (favorece acertos de cache), e o nome
	// não conflita com planner/worker, podendo ser atribuído corretamente pelo llmrec.parseSession.
	if ts != nil {
		ctx = transcript.WithSessionID(ctx, fmt.Sprintf("exp%d-goals", ts.ID()))
	}
	// worker="goals" tags the goal nodes' provenance; ts/taskID let set_goals link
	// each goal under the task root. This is the catalog's real set_goals tool, so a
	// web-edited description/schema on it applies here too.
	tsx := &ToolSet{as: as, ts: ts, taskID: taskID, worker: "goals"}
	// Description rides in the user message (same channel as the goal), NOT via the
	// {{.EngagementDescription}} template var — else a prompt that references the var
	// would inject the description twice. System prompt stays pure static instructions.
	sys := renderSystem("goals", goalsDefaultTmpl, GoalsVars{DataDir: dataDir, Now: nowStr()})
	// set_constraints está sempre disponível (não depende do asset store): o corpo já contém o passo "primeiro extrair as restrições de operação e depois decompor os objetivos"
	// (a redação pode ser alterada na página de edição do agent), aqui basta conectar a ferramenta.
	tools := []actool.CoreTool{tsx.setGoals(), tsx.setConstraints()}
	// Wire add_task_scope only when we have a real asset store + task to write to.
	// The scope-extraction tail is appended in lockstep so the prompt never asks for
	// a tool that isn't present.
	if as != nil && taskID > 0 {
		tools = append(tools, tsx.addTaskScope())
		sys += goalsScopeTail
	}
	userMsg := "Objetivo da tarefa:\n" + goalText
	if d := strings.TrimSpace(desc); d != "" {
		userMsg += "\n\nDescrição da tarefa (informações de contexto, podem conter escopo do alvo/quantidade de flags/regras de engajamento; apenas para referência, não invente o que não foi mencionado):\n" + d
	}
	// Use captureRun so every LLM step is emitted as an activity record (visible in
	// the plan tab under the round-0 marker). Falls back gracefully when emit is nil.
	captureEmit := func(r db.Activity) {
		if emit != nil {
			r.Worker = "planner"
			emit(r)
		}
	}
	captureRun(ctx, agentcore.Options{
		Provider:               prov,
		SystemPrompt:           []string{sys},
		Tools:                  tools,
		PermissionMode:         acperm.ModeBypass,
		DisableBackgroundTasks: true,
		// 3 passos (extrair restrições → registrar escopo → decompor objetivos) exigem cada um uma chamada de ferramenta; dê turnos suficientes para não deixar de chamar set_goals antes do encerramento.
		MaxTurns:     8,
		NonStreaming: nonStreaming, // quando este profile escolhe não streaming, usa Provider.Complete
		MaxTokens:    maxTokens,    // 0 = não envia limite; o padrão do servidor decide
	}, userMsg, captureEmit)
	// set_goals persisted the goals directly; read them back so the caller sees what
	// was written (empty slice ⇒ the LLM produced nothing ⇒ caller falls back).
	if ts == nil {
		return nil
	}
	nodes, _ := ts.ListByKind(db.KindGoal, 10000)
	var out []GoalSpec
	for _, n := range nodes {
		var p struct {
			Text      string `json:"text"`
			VulnClass string `json:"vulnclass"`
		}
		_ = json.Unmarshal(n.Payload, &p)
		if strings.TrimSpace(p.Text) != "" {
			out = append(out, GoalSpec{Text: p.Text, VulnClass: p.VulnClass})
		}
	}
	return out
}
