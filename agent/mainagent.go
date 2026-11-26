package agent

import (
	"context"
	"fmt"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// MainAgent is the thin human-interface orchestrator (docs §4.2 / §7). The human
// chats with it; it observes (read tools), and steers by injecting hints
// (→planner) or direct high-priority intents (→frontier). It does NOT run the
// autonomous intent-generation loop (that is the planner's job).
type MainAgent struct {
	findingRecorder FindingRecorder
	prov            llm.Provider
	model           string
	tx              *transcript.Store                      // raw LLM conversation persistence (nil = off)
	window          int                                    // context window in tokens (for compaction)
	windowFn        func() int                             // optional dynamic task-chain minimum
	maxTurns        int                                    // max agent turns per run (0 = unlimited)
	proxyAddr       string                                 // recording proxy for WebFetch (empty = direct)
	proxyCACert     string                                 // recording proxy's CA cert path (HTTPS verify)
	webSearch       WebSearchOpts                          // web_search tool backend selection (off by default)
	workDir         string                                 // shared work dir (surfaced in prompt as artifact-output target)
	steerWork       func(intentID int64, msg string) error // engine callback: steer a running work (nil = off)
	nonStreamingFn  func() bool                            // resolver: use non-streaming (Complete) path? (nil = streaming)
	noaEnabledFn    func() bool                            // resolver: use experimental noa compaction? (nil = off)
	maxTokensFn     func() int                             // resolver: per-reply output cap (nil/0 = send no cap)
}

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (m *MainAgent) SetNoaEnabled(fn func() bool) { m.noaEnabledFn = fn }

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default).
func (m *MainAgent) SetNonStreaming(fn func() bool) { m.nonStreamingFn = fn }

func (m *MainAgent) nonStreaming() bool { return m.nonStreamingFn != nil && m.nonStreamingFn() }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (m *MainAgent) SetMaxTokens(fn func() int) { m.maxTokensFn = fn }

func (m *MainAgent) maxTokens() int {
	if m.maxTokensFn == nil {
		return 0
	}
	return m.maxTokensFn()
}

func NewMainAgent(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int) *MainAgent {
	return &MainAgent{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns}
}

func (m *MainAgent) SetCompactionWindowResolver(fn func() int) { m.windowFn = fn }

func (m *MainAgent) compactionWindow() int {
	if m.windowFn != nil {
		return m.windowFn()
	}
	return m.window
}

// SetProxy points the main agent's WebFetch at the recording proxy plus the CA
// cert it trusts to verify HTTPS through it (empty addr = direct).
func (m *MainAgent) SetProxy(addr, caCert string) { m.proxyAddr, m.proxyCACert = addr, caCert }

// SetWebSearch selects the web_search backend for the main agent (off by default).
func (m *MainAgent) SetWebSearch(o WebSearchOpts) { m.webSearch = o }

// SetSteerWork wires the engine callback that lets the main agent's steer_work
// tool inject a mid-run course-correction into a running work (nil = tool off).
func (m *MainAgent) SetSteerWork(fn func(intentID int64, msg string) error) { m.steerWork = fn }

// mainAgentDefaultTmpl is the built-in EDITABLE body (seção [A]) of the main agent
// prompt, seeded into agent_prompts. Goal is a {{.Goal}} template var; the convenção de saída de
// artefatos intermediários tail is code-owned (artifactSpec), appended after rendering.
const mainAgentDefaultTmpl = `Você é o "agent principal" de um sistema autorizado de teste de intrusão e é a interface do operador humano. Você não explora por conta própria, nem gera intenções continuamente de forma autônoma (isso é trabalho do planejador). Suas responsabilidades:

1. Observar: use graph_overview / list_findings / list_facts / list_assets / get_worker_output para responder às perguntas das pessoas sobre o progresso atual.
2. Conduzir (levar a intenção da pessoa para o sistema):
   - Se a pessoa quer "mudar a direção / enfatizar certo tipo de vulnerabilidade / focar certa região" → use add_hint para escrever um hint (o planejador o lerá na próxima vez).
   - Se a pessoa quer "testar imediatamente um alvo específico" → use add_intent para injetar diretamente uma intenção de alta prioridade (priority 8-10). O sistema traz automaticamente a tarefa já concluída de volta ao estado em execução, para que um worker pegue essa intenção e a execute; ao terminar, volta ao estado concluído.
     **Quando todos os objetivos da tarefa já foram atingidos** (os goals em graph_overview estão todos met): antes de despachar, avalie se por trás desta intenção há implícito um "novo resultado a ser alcançado". Se houver, reformule em uma frase o objetivo que você supõe para a pessoa e **pergunte se deseja registrá-lo como objetivo formal** — se sim → registre com set_goals (a tarefa entra então no planejamento regular, e o planejador avançará por conta própria); se não / só quer uma sondagem temporária → apenas despache esta intenção com add_intent, e o worker, ao terminar, devolve a tarefa ao estado concluído (sem continuar por conta própria). Se a intenção for claramente só uma verificação pontual, sem novo objetivo implícito, basta add_intent, sem precisar perguntar toda vez.
   - Se a pessoa quer "corrigir em tempo real o rumo de uma intenção (work) em execução (não siga X, foque em Y)" → use steer_work (sem interromper, sem perder o progresso existente, vale antes da próxima ação do worker); primeiro use get_worker_output para ver o que ele está fazendo. Se a direção estiver totalmente errada, use add_intent para despachar uma nova intenção.
   - Se a pessoa quer "adicionar um novo objetivo final a ser atingido" → use set_goals para acrescentar objetivos. O sistema grava o objetivo no grafo da tarefa e **traz automaticamente a tarefa concluída/pausada de volta ao estado em execução** (o planejador então reavalia se foi atingido), sem necessidade de clicar manualmente em retomar.
   - Se a pessoa quer "adicionar/alterar restrições de teste (permitir/proibir certo tipo de operação, como 'testar apenas a porta atual', 'proibido força bruta', 'fazer apenas reconhecimento passivo')" → registre com set_constraints (type=allow permite / type=deny proíbe). As restrições são injetadas nos prompts do planner/worker na próxima rodada de planejamento para delimitar a fronteira da exploração; também é possível adicionar/remover/alterar em "Gerenciamento de restrições" na visão geral.
3. Responda em linguagem natural e de forma concisa, explicando o que você fez.

Objetivo atual da tarefa: {{.Goal}}

Não invente descobertas; responda apenas com base nos dados reais retornados pelas ferramentas.`

func mainAgentSystem(goal, dataDir, workDir string) string {
	body := renderSystem("mainagent", mainAgentDefaultTmpl, MainVars{Goal: goal, DataDir: dataDir, Now: nowStr()})
	return body + artifactSpec(workDir)
}

// Chat handles one human message and returns the assistant reply. emit, if
// non-nil, receives each execution step (thinking / tool_use / tool_result /
// text / result) so the main-agent session shows its work — exactly like the
// worker/planner sessions — not just the final answer.
func (m *MainAgent) Chat(ctx context.Context, taskID int64, mainSeg int, as *db.AssetStore, ts *db.ExplorationStore, goal, message string, emit func(db.Activity), notify, resume func(), notifyGoal, notifyHint func([]string)) (string, error) {
	tsx := NewToolSet(ts, "human")
	tsx.SetFindingRecorder(m.findingRecorder)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetTaskID(taskID)
	tsx.SetCoverageEnabled(as == nil || as.CoverageEnabled(taskID))
	tsx.SetNotify(notify)         // despertar genérico (operações de escrita sem callback dedicado passam por ele, debounced)
	tsx.SetResumeTask(resume)     // set_goals adiciona objetivo → traz de volta a tarefa concluída/pausada para running
	tsx.SetNotifyGoal(notifyGoal) // set_goals adiciona objetivo → registra para o planner uma entrada "a pessoa adicionou um objetivo: ..." como gatilho
	tsx.SetNotifyHint(notifyHint) // add_hint adiciona hint → registra para o planner uma entrada "a pessoa adicionou N dicas estratégicas: ..." como gatilho
	tsx.steerWork = m.steerWork   // enable steer_work tool (nil = unavailable)
	// Ferramentas de domínio + conjunto básico de ferramentas padrão (Read/Write/Edit/MultiEdit/LS/Glob/Grep/Bash)
	// Com a função de cobertura de ativos desativada, remove add_task_scope/list_untested_assets (não entram no prompt).
	base := append(tsx.DropCoverageTools(tsx.MainAgentTools()), actool.DefaultTools()...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts)})
	tools, def, cleanup := AugmentTools(ctx, "mainagent", base)
	defer cleanup()
	// Diretório de trabalho desta tarefa <workDir>/tasks/<taskID>, criado antes.
	mainDir := ensureRunDir(m.workDir, taskID, 0)
	ctx = intercept.WithReviewWorkingDirectory(ctx, mainDir)
	system, boundary := deferredSystem(mainAgentSystem(goal, m.workDir, mainDir), def)
	opts := agentcore.Options{
		Provider:        m.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		EnableWebFetch:  true, // passa pelo proxy de registro para deixar rastro; carrega a CA do proxy para validar certificados HTTPS reassinados pelo MITM
		WebFetchProxy:   m.proxyAddr,
		WebFetchCACert:  m.proxyCACert,
		// Busca na web (opcional). ddgs não precisa de key; brave-free precisa de BraveKey; tavily precisa de TavilyKey.
		// WebSearchProxy é um proxy de saída independente (http/https/socks5), sem relação com o proxy MITM que registra o tráfego; vazio = conexão direta.
		EnableWebSearch:       m.webSearch.Enabled,
		WebSearchBackend:      m.webSearch.Backend,
		BraveSearchAPIKey:     m.webSearch.BraveKey,
		TavilySearchAPIKey:    m.webSearch.TavilyKey,
		DeepSeekSearchBaseURL: m.webSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  m.webSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   m.webSearch.DeepSeekModel,
		WebSearchProxy:        m.webSearch.Proxy,
		BashEnv:               proxyEnv(m.proxyAddr, m.proxyCACert), // Subcomandos do Bash passam pelo proxy por padrão + confiam na CA
		WorkingDir:            mainDir,                              // diretório de trabalho desta tarefa <workDir>/tasks/<taskID>
		ToolOutputDir:         cmdOutDir(mainDir),
		MaxTurns:              m.maxTurns,                             // 0 = unlimited (configurable in agent management)
		Compaction:            compactionConfig(m.compactionWindow()), // long chats stay within the window
		Todos:                 actool.NewTodoStore(),                  // todo temporário em nível de sessão (TodoWrite), só para planejamento, descartado ao sair
		// Ao atingir o orçamento (passos) → o SDK faz o encerramento: emite ao usuário um resumo de progresso de uma frase. O Prompt e o número de turnos de encerramento podem ser editados no painel (padrão: 10 turnos).
		Settlement:   wrapupSettlement("mainagent", nil),
		NonStreaming: m.nonStreaming(), // quando este profile escolhe não streaming, usa Provider.Complete
		MaxTokens:    m.maxTokens(),    // 0 = não envia limite; o padrão do servidor decide
	}
	if m.tx != nil { // persist raw human↔AI conversation; one accumulating file per segment
		opts.Transcript = m.tx
		// Segment 0 keeps the legacy "exp%d-main" name so existing transcripts still
		// load; each new session (seg>=1) gets its own file for a clean context.
		opts.SessionID = fmt.Sprintf("exp%d-main", ts.ID())
		if mainSeg > 0 {
			opts.SessionID = fmt.Sprintf("exp%d-main-s%d", ts.ID(), mainSeg)
		}
	}
	// Recurso experimental: quando ativado, o noa assume a compressão de contexto (arquivos em <workDir>/noa/<SessionID>, persistentes).
	// O session id segue a mesma regra do transcript (ciente de segmentos), alinhando arquivo e recuperação.
	noaSession := fmt.Sprintf("exp%d-main", ts.ID())
	if mainSeg > 0 {
		noaSession = fmt.Sprintf("exp%d-main-s%d", ts.ID(), mainSeg)
	}
	enableNoa(&opts, m.noaEnabledFn, m.workDir, noaSession, noaWarn(noaSession))
	ctx = attachSideCapture(ctx, &opts)
	s := agentcore.NewSession(opts)
	defer s.Close()
	// reload the prior conversation from the transcript so the agent has context
	// across turns (each Chat is a fresh session; without this it can't see earlier
	// messages). First turn: no file yet → Resume loads nothing and proceeds.
	if m.tx != nil {
		_ = s.Resume(opts.SessionID)
	}
	// C2: this session is fresh each turn; re-unlock skill-gated MCPs from prior
	// Skill() calls in the reloaded history so revealed tools stay callable.
	seedUnlockFromHistory(s.Messages(), def.UnlockSkill)
	text, _, err := captureRunSession(ctx, s, message, func(r db.Activity) {
		if emit != nil {
			r.Worker = "mainagent"
			emit(r)
		}
	})
	return text, err
}
