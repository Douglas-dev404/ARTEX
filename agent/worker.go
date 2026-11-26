package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/intercept"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/harness"
	"github.com/Autumn-27/norma/llm"
	"github.com/Autumn-27/norma/permission"
	actool "github.com/Autumn-27/norma/tool"
	"github.com/Autumn-27/norma/transcript"
)

// Worker is an LLM work agent (docs §4.4): it claims ONE intent, completes it
// with real tools (Bash: kali tooling through the recording proxy), writes the
// FACTS it found back into the graph, and stops. It does NOT generate new
// directions (that is the planner's job) and does NOT keep exploring toward the
// goal on its own. Multiple workers run concurrently as goroutines.
// WebSearchOpts is the web-search backend selection the server pushes into each
// agent (planner/worker/main). Enabled=false leaves the web_search tool off.
// Backend is "ddgs" (no key), "brave-free" (BraveKey required), "tavily"
// (TavilyKey required), or "deepseek" (DeepSeek* required, filled from the
// active LLM profile). It maps directly onto agentcore.Options.
// Proxy is a dedicated egress proxy for the search request (http/https/socks5),
// independent of the traffic-recording MITM proxy — set it when the search endpoint
// is only reachable via a VPN/SOCKS proxy. Empty = direct.
//
// Atenção: o backend deepseek tem natureza diferente dos outros três: o DeepSeek não tem uma interface de busca que possa ser chamada diretamente,
// a busca existe apenas dentro da sua interface messages compatível com Anthropic (web_search_20250305 server
// tool), portanto cada busca consome uma chamada ao modelo, e a requisição de busca é emitida pelo servidor do DeepSeek —
// não passa pelo Proxy local e também não entra no registro de tráfego.
type WebSearchOpts struct {
	Enabled   bool
	Backend   string
	BraveKey  string
	TavilyKey string
	Proxy     string
	// DeepSeek* vêm da configuração LLM atualmente ativa (apenas o endpoint oficial DeepSeek no formato anthropic),
	// não são configurados separadamente e mudam junto com a troca da configuração LLM.
	DeepSeekBaseURL string
	DeepSeekAPIKey  string
	DeepSeekModel   string
}

type Worker struct {
	findingRecorder FindingRecorder
	prov            llm.Provider
	model           string
	workDir         string
	proxyAddr       string
	proxyCACert     string            // recording proxy's CA cert path (for WebFetch HTTPS verify)
	webSearch       WebSearchOpts     // web_search tool backend selection (off by default)
	tx              *transcript.Store // raw LLM conversation persistence (nil = off)
	window          int               // context window in tokens (for compaction)
	windowFn        func() int        // optional dynamic task-chain minimum
	maxTurns        int               // max agent turns per run (0 = unlimited)
	// runTimeout is the wall-clock budget for the main exploration of one intent
	// (0 = unlimited). When it fires, the run is cut and a settlement round is
	// forced so already-identified facts get written back instead of being lost.
	runTimeout time.Duration
	// extraTools are host-provided tools (e.g. traffic query, oast) appended to
	// the worker's graph write-back tools.
	extraTools []actool.CoreTool
	// injectConstraints resolves whether this task's operation constraints get
	// injected into the worker system prompt. Read per run so the settings toggle
	// takes effect without rebuilding the agent. nil = inject (default).
	injectConstraints func() bool
	// nonStreamingFn resolves whether this run uses the non-streaming (Complete)
	// path. Read per run so a profile/task toggle takes effect without rebuilding
	// the agent. nil = streaming (default).
	nonStreamingFn func() bool
	// noaEnabledFn resolves whether this run uses the experimental noa context-
	// compression mechanism. Read per run, like nonStreaming. nil = off (built-in
	// compaction).
	noaEnabledFn func() bool
	// maxTokensFn resolves the per-reply output cap in tokens, on the same
	// per-run basis. nil or 0 = send no cap and let the endpoint decide.
	maxTokensFn func() int
}

// WorkerSessionID returns the stable transcript key used by a worker intent.
// Worker slots are reusable, so the intent id (rather than work#N) is the
// session identity. Keep this helper public so the Worker message API and UI
// can refer to exactly the conversation that will be resumed.
func WorkerSessionID(explorationID, intentID int64) string {
	return fmt.Sprintf("exp%d-worker-i%d", explorationID, intentID)
}

const workerChatMarkerPrefix = "<!-- ARTEX_WORKER_CHAT:"

func workerChatMarker(requestID string) string {
	return workerChatMarkerPrefix + requestID + " -->"
}

func hasWorkerChatMessage(messages []llm.Message, requestID string) bool {
	marker := workerChatMarker(requestID)
	for _, message := range messages {
		if message.Role == llm.RoleUser && strings.Contains(message.Text(), marker) {
			return true
		}
	}
	return false
}

// SetNonStreaming wires a resolver deciding whether runs use the non-streaming
// model path (true = non-streaming). nil/unset = streaming (default). Read per
// run so a profile or task-chain toggle takes effect without rebuilding.
func (w *Worker) SetNonStreaming(fn func() bool) { w.nonStreamingFn = fn }

func (w *Worker) nonStreaming() bool { return w.nonStreamingFn != nil && w.nonStreamingFn() }

// SetNoaEnabled wires a resolver deciding whether runs use the experimental noa
// context-compression mechanism. nil/unset = off (built-in compaction). Read per
// run so the settings toggle takes effect without rebuilding the agent.
func (w *Worker) SetNoaEnabled(fn func() bool) { w.noaEnabledFn = fn }

// SetMaxTokens wires a resolver for the per-reply output cap. nil/unset or 0 =
// send no cap and let the endpoint decide. Read per run, like nonStreaming.
func (w *Worker) SetMaxTokens(fn func() int) { w.maxTokensFn = fn }

func (w *Worker) maxTokens() int {
	if w.maxTokensFn == nil {
		return 0
	}
	return w.maxTokensFn()
}

// SetConstraintInject wires a resolver deciding whether this task's operation
// constraints get injected into the worker system prompt. nil = inject (default).
func (w *Worker) SetConstraintInject(fn func() bool) { w.injectConstraints = fn }

// wantConstraints reports whether constraint injection is enabled (default yes).
func (w *Worker) wantConstraints() bool { return w.injectConstraints == nil || w.injectConstraints() }

// SetRunTimeout configures the per-intent wall-clock budget for the main
// exploration (0 = unlimited). When it fires, the SDK settlement phase still runs
// so facts are never lost to a timeout. Safe to call before Execute.
func (w *Worker) SetRunTimeout(run time.Duration) {
	w.runTimeout = run
}

// settleWrapUpPrompt is injected by the SDK settlement phase when a worker hits its
// turn/time budget: stop probing, write back what was found, then end with a
// plain-text one-liner (which becomes this run's displayed result).
const settleWrapUpPrompt = "Você está prestes a ser encerrado por esgotamento de orçamento. Não execute mais nenhum comando/sondagem. Faça, em ordem: (1) grave de volta, um a um, o que você identificou acima mas ainda não gravou — novos ativos com insert_assets, conclusões/fatos de exploração com record_fact, vulnerabilidades confirmadas com report_finding; (2) **por último, em uma única frase, em texto puro**, resuma o que você fez e quais conclusões-chave obteve (esta frase será exibida como resultado desta execução, é imprescindível emiti-la)."

func NewWorker(prov llm.Provider, model, workDir string, tx *transcript.Store, window, maxTurns int, extra ...actool.CoreTool) *Worker {
	return &Worker{prov: prov, model: model, workDir: workDir, tx: tx, window: window, maxTurns: maxTurns, extraTools: extra}
}

// defaultToolsExcept returns actool.DefaultTools() minus the named tools (by
// CoreTool.Name()). Used to trim SDK default tools an agent shouldn't have.
func defaultToolsExcept(exclude ...string) []actool.CoreTool {
	drop := make(map[string]bool, len(exclude))
	for _, n := range exclude {
		drop[n] = true
	}
	all := actool.DefaultTools()
	out := make([]actool.CoreTool, 0, len(all))
	for _, t := range all {
		if !drop[t.Name()] {
			out = append(out, t)
		}
	}
	return out
}

func (w *Worker) SetCompactionWindowResolver(fn func() int) { w.windowFn = fn }

func (w *Worker) compactionWindow() int {
	if w.windowFn != nil {
		return w.windowFn()
	}
	return w.window
}

// SetProxy configures the recording proxy address that workers route target
// traffic through, plus the CA cert path WebFetch trusts to verify HTTPS through
// that MITM proxy. Empty addr disables the hint.
func (w *Worker) SetProxy(addr, caCert string) { w.proxyAddr, w.proxyCACert = addr, caCert }

// SetWebSearch selects the web_search backend for this worker (off by default).
func (w *Worker) SetWebSearch(o WebSearchOpts) { w.webSearch = o }

// proxyEnv builds the Bash-subprocess env that routes child-command HTTP through
// the egress proxy (the recording MITM when capture is on, or the global proxy
// directly when it is off) and, only when a MITM CA is present, makes the common
// toolchain trust it — so tools need no manual -x/--proxy/-k. Each ecosystem reads
// a different CA var (verified empirically): SSL_CERT_FILE→curl/urllib/Go/openssl,
// REQUESTS_CA_BUNDLE→python requests (it ignores SSL_CERT_FILE), CURL_CA_BUNDLE→curl,
// GIT_SSL_CAINFO→git, NODE_EXTRA_CA_CERTS→node; NODE_USE_ENV_PROXY makes Node 24+
// honor the proxy vars. ALL_PROXY is set too so a socks5 egress proxy (which curl
// only reads from ALL_PROXY, not HTTP(S)_PROXY) works in the capture-off path.
// Empty proxyAddr → nil (direct, unchanged env).
func proxyEnv(proxyAddr, caCert string) []string {
	if proxyAddr == "" {
		return nil
	}
	env := []string{
		"HTTP_PROXY=" + proxyAddr, "HTTPS_PROXY=" + proxyAddr,
		"http_proxy=" + proxyAddr, "https_proxy=" + proxyAddr,
		"ALL_PROXY=" + proxyAddr, "all_proxy=" + proxyAddr, // socks5 egress: curl reads only this
		"NODE_USE_ENV_PROXY=1", // Node 24+: honor HTTP(S)_PROXY in built-in fetch/http
	}
	if caCert != "" {
		env = append(env,
			"SSL_CERT_FILE="+caCert,
			"CURL_CA_BUNDLE="+caCert,
			"REQUESTS_CA_BUNDLE="+caCert,
			"GIT_SSL_CAINFO="+caCert,
			"NODE_EXTRA_CA_CERTS="+caCert,
		)
	}
	return env
}

// workerDefaultTmpl is the built-in EDITABLE body (seção [A]) of the worker system
// prompt, seeded into agent_prompts. The trafficTool block and the convenção de saída de artefatos intermediários
// are NOT here — they are code-owned and appended by workerSystem after rendering
// (seção [B]/[C]), so editing the DB body can never drop them.
const workerDefaultTmpl = `Você é o "executor" (work agent) de um sistema autorizado de teste de intrusão de uma plataforma de segurança cibernética. Você recebe [uma intenção] (uma frase com a direção de exploração), e sua única responsabilidade é: **concluir esta intenção, gravar as descobertas de volta no grafo de conhecimento e então parar e retornar.**

**Limites (linhas vermelhas)**:
1. **Faça apenas a intenção que você recebeu**. **Se, ao explorar esta intenção, você vislumbrar pistas fora dela que valem um aprofundamento** (caminhos vazados em mensagens de erro, pontos que podem se conectar a outros ativos, entradas de uma suposta outra cadeia de exploração), **cite-as numa frase no summary de um fact e repasse ao planejador**.
2. O primeiro bloqueio (payload filtrado / 404 / injeção sem eco) não significa que já foi explorado a fundo — esgote todos os meios de contorno desta intenção antes de emitir a conclusão;
3. Opere somente dentro do escopo autorizado. Se no topo do prompt do sistema houver [Restrições de operação] anexadas, elas são a linha vermelha de prioridade máxima: antes de executar cada comando/sondagem, verifique primeiro; havendo violação, não faça (mesmo que esteja dentro da intenção que você recebeu).

**Grave de volta à medida que descobre** (só vale o que foi escrito no grafo; o que está na cabeça/no texto não conta; grave imediatamente a cada resultado obtido, não acumule até o fim para perder tudo quando os passos se esgotarem). Há três tipos de gravação, não misture os grafos:
- **Novos ativos/recursos → insert_assets (grafo de ativos)**: subdomínios / service / endpoint / fingerprints / credenciais e todo ativo [em si]. **Aqui se registram apenas ativos; conclusões/julgamentos de exploração não se escrevem aqui, use record_fact.**
- **Conclusões/fatos de exploração → record_fact (grafo de exploração, passe intent_id)**: use-o sempre. **Resuma várias observações em [um único] fato** (summary com uma frase de resumo + detail com a expansão do resumo, apoiada no processo de execução real), não um fato por atributo; uma intenção normalmente tem só um; fragmentar faz o grafo inchar sem limite — **por padrão escreva um só, tudo o que puder ser incorporado ao detail deve ser incorporado**; só quando houver de fato conclusões [totalmente independentes entre si e impossíveis de unificar] use o array facts para separá-las, o que é uma exceção raríssima, não a regra. **Escreva só o incremento**: registre apenas o que foi [obtido de novo] desta vez, não reescreva com outras palavras fatos já existentes (se apenas confirma o que já existe, sem novidade, não precisa registrar). **Escreva só o que realmente viu**: forneça evidence (uma linha: comando + uma ou duas linhas de saída que melhor comprovam, conciso, os detalhes ficam em detail) e marque confidence (observed = visto diretamente / inferred = inferido a partir dos fenômenos).
- **Vulnerabilidade confirmada → report_finding (grafo de exploração, com PoC, passe intent_id)**: **use somente se você a disparou de fato nesta vez e obteve evidência reproduzível (requisição/resposta ou saída de comando)**. É proibido tratar como confirmado "versão/fingerprint casando com um CVE", "parâmetro que parece injetável", "inferência a partir de base externa de vulnerabilidades/changelog/diff de código", nem substituir o disparo real por consultar base de CVE ou comparar versão de patch. Se não consegue disparar mas há suspeita → use record_fact para registrar um fato inferred (ponto suspeito + por que não foi disparado) e repasse ao planejador, sem forçar o registro como finding.


Ao concluir esta intenção, resuma em uma frase o que você fez e quais fatos gravou.`

// workerTrafficBlock is seção [B]: the traffic-tool note, code-injected only when
// traffic capture (recording) is on — i.e. the traffic_* tools actually exist.
// Gated on recording, NOT on the egress proxy: a global proxy with capture off
// routes traffic but records nothing, so the tools would not be there. Not stored,
// not editable.
func workerTrafficBlock(recording bool) string {
	if !recording {
		return ""
	}
	return "\n\n**Ferramentas de tráfego**:\n- traffic_search / traffic_get / traffic_blob: para rever respostas e encontrar recursos já visitados, **consulte primeiro o tráfego, não repita curl na mesma URL**. traffic_search **exige host**, por padrão retorna só 3 entradas de índice ultraleves (id/method/url/status/resp_len, sem conteúdo da resposta); se precisar de mais, aumente limit explicitamente; pode usar body_contains para busca de texto completo no corpo da requisição/resposta (mínimo de 3 caracteres, suporta substring e chinês, por exemplo para achar senha/chave/erro/endereço interno); para ver o original de uma entrada use traffic_get(id), em que corpos muito grandes aparecem como @blob sha256:<hash>, e use traffic_blob(hash) para obter o texto completo em partes."
}

// artifactSpec is seção [C]: the code-owned, non-editable tail appended to every
// pentest agent's prompt — intermediate artifacts must land in the shared work
// dir, never /tmp. Guaranteed present regardless of how the DB body is edited.
func artifactSpec(dir string) string {
	return "\n\n**Convenção de saída de artefatos intermediários**: scripts, payloads, corpos de resposta capturados, dados temporários e todos os demais artefatos intermediários, **grave sempre no diretório de trabalho desta tarefa " + dir + "** (caminhos relativos são gravados aqui, e também é possível usar este caminho absoluto) — **não grave em /tmp, não use outros caminhos absolutos**."
}

// workerArtifactSpec is the worker's seção [C]: its per-intent run dir is pre-created
// by the engine (ensureRunDir), so it just writes relative paths there — no manual
// mkdir, no cross-worker name collisions.
func workerArtifactSpec(runDir string) string {
	return "\n\n**Convenção de saída de artefatos intermediários**: scripts, payloads, corpos de resposta capturados, dados temporários e todos os demais artefatos intermediários, **grave sempre no diretório de trabalho exclusivo desta intenção " + runDir + "** (já criado automaticamente; basta gravar aqui com caminho relativo, sem precisar criar o diretório manualmente) — **não grave em /tmp, não use outros caminhos absolutos**."
}

// ensureRunDir builds and creates an agent's working directory under base:
// <base>/tasks/<taskID> for planner/main; <base>/tasks/<taskID>/i<intentID> for a
// worker (intentID<=0 → task dir only). The "tasks/" segment groups per-task dirs
// symmetrically with the chat agent's "sessions/<sessionID>". Best-effort mkdir — on
// failure, writes fail the same way an unwritable CWD would.
func ensureRunDir(base string, taskID, intentID int64) string {
	dir := filepath.Join(base, "tasks", strconv.FormatInt(taskID, 10))
	if intentID > 0 {
		dir = filepath.Join(dir, "i"+strconv.FormatInt(intentID, 10))
	}
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// cmdOutDir is the SDK large-tool-output spill dir under an agent's run dir.
func cmdOutDir(dir string) string { return filepath.Join(dir, "cmd-output") }

func workerSystem(proxyAddr, caCert, dataDir, runDir string) string {
	body := renderSystem("worker", workerDefaultTmpl, WorkerVars{ProxyAddr: proxyAddr, DataDir: dataDir, Now: nowStr()})
	// caCert is present only when the recording MITM is on, which is exactly when
	// the traffic_* tools are registered — so it gates the traffic-tool note.
	// Optional finding guidance is added for every role after tool resolution.
	return body + workerTrafficBlock(caCert != "") + workerArtifactSpec(runDir)
}

// renderIntentTask formats the claimed intent for the worker's launch USER message:
// the intent is the worker's whole job. It used to live in the system prompt; it now
// rides in the first user turn (together with the situational overview) so the system
// prompt stays static/role-only — same move as the planner's situational block.
// intentAssetIDs pulls the intent's target asset ids out of its payload
// (planner's add_intent stores them as a numeric asset_ids array). nil on absence
// or malformed payload.
func intentAssetIDs(intent *db.Node) []int64 {
	if intent == nil {
		return nil
	}
	var p struct {
		AssetIDs []int64 `json:"asset_ids"`
	}
	if err := json.Unmarshal(intent.Payload, &p); err != nil {
		return nil
	}
	return p.AssetIDs
}

func renderIntentTask(intent *db.Node) string {
	return fmt.Sprintf("\n\n[Intenção que você recebeu (única tarefa desta vez: faça só esta, produza apenas fatos, termine e pare)]:\n%s\nid da intenção: %d (passe-o ao gravar com record_fact / report_finding)", string(intent.Payload), intent.ID)
}

// renderWorkerGraphOverview folds the global situational snapshot into the worker's
// launch USER message for AWARENESS ONLY. The framing is deliberately strong: the overview
// must NOT widen the worker's job — it still does only its assigned intent. Its sole
// purpose is letting the worker read context (existing facts/assets/hints)
// so it avoids redundant work and doesn't re-derive what others already found.
func renderWorkerGraphOverview(data map[string]any) string {
	// coverage é o sinal para o planejador julgar "que tipo foi menos testado / se vale ampliar o escopo", o que contradiz a fronteira de responsabilidade do worker
	// de "fazer só a intenção recebida, sem perseguir pontos não cobertos" → é removido da visão do worker. data é um map novo
	// exclusivo desta execução do worker, e remover a chave não afeta o planner.
	delete(data, "coverage")
	b, err := json.Marshal(data)
	if err != nil {
		return "" // fall back silently: the worker just won't have the global context
	}
	return "\n\n[Panorama global de exploração (somente leitura, ajuda a situar esta intenção no quadro geral)]:\n" +
		"Abaixo está o panorama atual de exploração de toda a tarefa. Serve a dois propósitos: um, saber o que os outros já descobriram, para não repetir; outro, permitir que, ao explorar esta intenção, você associe sua relação com o quadro global.\n" +
		"**Divagar é bom**: ao explorar esta intenção, pense fundo e associe à vontade. O único limite é — não execute de fato outras intenções (isso é assunto de outros workers, escalonados pelo planejador). Sempre que associar uma pista valiosa (conexão entre ativos, entrada de uma suposta outra cadeia de exploração, pontos suspeitos em nível global), **registre-a obrigatoriamente num fact para o planejador** — isto é uma produção importante sua, não algo dispensável. É melhor reportar uma a mais para o planejador julgar do que engolir sozinho.\n" +
		string(b)
}

// Execute runs one intent. hooks (the per-task Guard) gates every tool call; may
// be nil. emit, if non-nil, receives one ActivityRecord per execution step.
// notifyFinding, if non-nil, is called (intentID, summary) when this worker writes
// a finding (report_finding) so the task's planner wakes mid-flight — with context
// on which intent found what — instead of waiting for the worker to finish.
// Returns the terminal reason (so the engine can distinguish completed vs
// max_turns) and a per-kind breakdown of what was written back (so an intent that
// explored but persisted nothing isn't mistaken for done, and the engine can log
// facts/assets/findings separately instead of lumping them under "facts").
func (w *Worker) Execute(ctx context.Context, name string, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner, emit func(db.Activity), enr EnrichTrigger, notifyFinding func(int64, string)) (harness.TerminalReason, WriteCounts, error) {
	return w.execute(ctx, name, taskID, as, ts, intent, hooks, emit, enr, notifyFinding, "", "")
}

// ExecuteWithMessage runs the next turn in the same intent conversation with a
// human-authored message. The HTTP handler does not edit the transcript;
// agentcore records the message as a normal user turn when this Worker starts.
// This keeps Worker continuation identical to the regular agent chat flow.
func (w *Worker) ExecuteWithMessage(ctx context.Context, name string, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner, emit func(db.Activity), enr EnrichTrigger, notifyFinding func(int64, string), requestID, message string) (harness.TerminalReason, WriteCounts, error) {
	return w.execute(ctx, name, taskID, as, ts, intent, hooks, emit, enr, notifyFinding, strings.TrimSpace(requestID), strings.TrimSpace(message))
}

func (w *Worker) execute(ctx context.Context, name string, taskID int64, as *db.AssetStore, ts *db.ExplorationStore, intent *db.Node, hooks harness.HookRunner, emit func(db.Activity), enr EnrichTrigger, notifyFinding func(int64, string), requestID, message string) (harness.TerminalReason, WriteCounts, error) {
	tsx := NewToolSet(ts, name)
	tsx.SetFindingRecorder(w.findingRecorder)
	tsx.SetTaskID(taskID)
	coverageEnabled := as == nil || as.CoverageEnabled(taskID)
	tsx.SetCoverageEnabled(coverageEnabled)
	if as != nil {
		tsx.SetAssetStore(as, as.Companies())
	}
	tsx.SetOwnerNode(intent.ID)         // assets this worker discovers anchor to its intent → visible to the task
	tsx.SetEnrich(enr)                  // async DNS/HTTP auto-completion for assets this worker writes
	tsx.SetNotifyFinding(notifyFinding) // ao gravar report_finding no banco, desperta o planner na hora, levando "qual intenção + finding"
	// base = built-in worker tools ∪ host tools (traffic) ∪ default tools (incl. Bash);
	// then augment with the agent's visible skills/MCP. During the SDK settlement
	// phase, Bash is hidden via Settlement.DisabledTools (no local gating needed).
	base := append(tsx.WorkerTools(), w.extraTools...)
	// O worker deliberadamente não recebe MultiEdit/Glob/Grep: edição fina de arquivos usa Edit, busca vai por Bash(grep/find),
	// reduzindo a superfície de ferramentas e chamadas de baixo valor. As demais ferramentas padrão do SDK (Read/Write/Edit/LS/Bash/Sleep) seguem como de costume.
	base = append(base, defaultToolsExcept("MultiEdit", "Glob", "Grep")...)
	ctx = WithRunInfo(ctx, RunInfo{TaskID: taskID, ExplorationID: explorationID(ts), IntentID: intent.ID})
	tools, def, cleanup := AugmentTools(ctx, "worker", base)
	defer cleanup()

	// A intenção é a [responsabilidade única do worker, um invariante que atravessa toda a run] → junto com a instrução de início e os dados brutos dos ativos-alvo ancorados na intenção
	// vai para o system prompt: o system é remontado a cada run e jamais é removido pela compaction,
	// numa run longa a intenção está sempre presente, e ao continuar não depende de o transcript ter preservado aquela primeira mensagem. O custo é que o system
	// passa a conter dados voláteis por intenção, perdendo a reutilização de cache entre intenções; é um trade-off deliberado (perder a intenção é muito mais grave do que economizar tokens).
	// Divergir do planner, que "coloca o bloco de panorama no user turn", é intencional: o planner é quem produz intenções, não tem um mandate único,
	// o worker tem. Somente o [panorama global overview] fica na mensagem inicial do usuário — ele pode ser degradado, tolera stale, e removê-lo não faz mal.
	// Diretório de trabalho exclusivo desta intenção <workDir>/tasks/<taskID>/i<intentID>, criado antes pelo lado do engine.
	runDir := ensureRunDir(w.workDir, taskID, intent.ID)
	// The run-wide intent is not the current tool action. Do not forward it or
	// inherit a parent run's background into the action reviewer.
	ctx = intercept.WithReviewContext(ctx, runDir, intercept.ReviewBackground{})
	overview := renderWorkerGraphOverview(tsx.graphOverviewData())
	sysBody := workerSystem(w.proxyAddr, w.proxyCACert, w.workDir, runDir)
	if w.wantConstraints() {
		sysBody += constraintBlock(ts) // Restrições de operação (se houver) são injetadas no prompt do sistema; o worker as segue estritamente na execução
	}
	// Bloco de intenção → bloco de ativos ancorados na intenção → instrução de início, anexados em sequência ao final do system (mesmo método de anexação do constraintBlock).
	sysBody += renderIntentTask(intent)
	if as != nil {
		if ids := intentAssetIDs(intent); len(ids) > 0 {
			if assets, err := as.GetByIDs(ids); err == nil && len(assets) > 0 {
				if b, err := json.Marshal(assets); err == nil {
					sysBody += "\n\nAtivos-alvo correspondentes aos asset_ids desta intenção:\n" + string(b)
				}
				// Estes ativos a que a intenção se destina explicitamente → entram automaticamente no escopo de teste da tarefa (mesma granularidade
				// conservadora do insertAssets). O ON CONFLICT DO NOTHING do upsertTaskScope + o índice
				// único uq_task_scope garantem que não haverá duplicatas; reexecução/retentativa também é um no-op idempotente.
				// Com a função de cobertura de ativos desativada, o escopo de teste (denominador) deixa de ser acumulado.
				if coverageEnabled {
					for _, a := range assets {
						_ = as.AddAutoScope(taskID, a.Type, a.Domain, a.URL, a.IP)
					}
				}
			}
		}
	}
	sysBody += "\n\nComece a executar a intenção acima: faça só ela, produza apenas fatos, assets, finding, termine e pare."
	system, boundary := deferredSystem(sysBody, def)
	// O deadline de nível de tarefa (injetado via ctx) limita o orçamento de tempo de relógio desta run + decide o prompt de encerramento (ver taskclock.go).
	tc := taskClockFrom(ctx)
	maxDur, clamped := clampMaxDuration(tc.DeadlineUnix, w.runTimeout)
	settle := wrapupSettlement("worker", []string{"Bash"})
	if tc.DeadlineUnix > 0 {
		settle = wrapupSettlementForTask("worker", []string{"Bash"}, clamped)
	}
	opts := agentcore.Options{
		Provider:        w.prov,
		SystemPrompt:    system,
		DynamicBoundary: boundary,
		Tools:           tools,
		DeferredTools:   def.Deferred,
		UnlockSet:       def.Unlock,
		PermissionMode:  permission.ModeBypass,
		// O WebFetch passa pelo proxy de registro, e seu HTTP, como o curl, fica registrado; carrega a CA do proxy para que os certificados HTTPS reassinados pelo MITM
		// sejam [validados normalmente] (e não com a validação desligada). Proxy vazio = conexão direta.
		EnableWebFetch: true,
		WebFetchProxy:  w.proxyAddr,
		WebFetchCACert: w.proxyCACert,
		// Busca na web (opcional). ddgs não precisa de key; brave-free precisa de BraveKey; tavily precisa de TavilyKey.
		// WebSearchProxy é um proxy de saída independente (http/https/socks5), sem relação com o proxy MITM que registra o tráfego; vazio = conexão direta.
		EnableWebSearch:       w.webSearch.Enabled,
		WebSearchBackend:      w.webSearch.Backend,
		BraveSearchAPIKey:     w.webSearch.BraveKey,
		TavilySearchAPIKey:    w.webSearch.TavilyKey,
		DeepSeekSearchBaseURL: w.webSearch.DeepSeekBaseURL,
		DeepSeekSearchAPIKey:  w.webSearch.DeepSeekAPIKey,
		DeepSeekSearchModel:   w.webSearch.DeepSeekModel,
		WebSearchProxy:        w.webSearch.Proxy,
		// O HTTP dos subcomandos do Bash passa por padrão pelo proxy de registro + confia na sua CA (as ferramentas não precisam de -x/-k).
		BashEnv:    proxyEnv(w.proxyAddr, w.proxyCACert),
		WorkingDir: runDir,
		MaxTurns:   w.maxTurns, // 0 = unlimited (configurable in agent management)
		// Orçamento de tempo de relógio, avaliado na fronteira de turno, sem interromper no meio; 0 = sem limite. Havendo deadline de nível de tarefa, limita a min(orçamento próprio,
		// restante até o deadline), para que esta run entre naturalmente no encerramento quando a tarefa chega ao prazo (ver taskclock.go).
		MaxDuration: maxDur,
		// Ao atingir o orçamento (turnos OU duração) → o SDK roda uma rodada de encerramento (Bash oculto), gravando o que já foi identificado, evitando um final largado.
		// Quando clamped (limitado pelo deadline da tarefa) usa PromptByReason: por timeout = prazo da tarefa → prompt de timeout da tarefa,
		// por passos = passos esgotados primeiro na janela limitada → volta ao prompt per-run. Quando não clamped, mantém o per-run puro.
		Settlement: settle,
		// large tool output spills to cmd-output/ with a head + pointer (SDK tool.Capture);
		// full output preserved on disk. Limite de truncamento usa o padrão do SDK (30000 caracteres).
		ToolOutputDir: cmdOutDir(runDir),
		Compaction:    compactionConfig(w.compactionWindow()), // long tool-heavy runs stay within the window
		Todos:         actool.NewTodoStore(),                  // todo temporário em nível de sessão (TodoWrite), só para planejamento, descartado ao sair
		NonStreaming:  w.nonStreaming(),                       // quando este profile escolhe não streaming, usa Provider.Complete
		MaxTokens:     w.maxTokens(),                          // 0 = não envia limite; o padrão do servidor decide
	}
	if hooks != nil { // typed-nil guard: only set when concrete (avoids harness panic)
		opts.Hooks = hooks
	}
	if w.tx != nil { // persist raw LLM conversation; one file per worked intent
		opts.Transcript = w.tx
		opts.SessionID = WorkerSessionID(ts.ID(), intent.ID)
	}
	intentID := intent.ID
	emitWrap := func(r db.Activity) {
		if emit != nil {
			r.NodeID, r.Worker = &intentID, name
			emit(r)
		}
	}
	// A intenção / instrução de início / ativos ancorados na intenção já foram entregues com o system prompt (ver a montagem de sysBody acima).
	// Esta mensagem inicial do user carrega apenas o [panorama global overview] — informação de visão geral que pode ser degradada, removê-la não faz mal.
	// Quando o overview, raramente, falha no marshal e fica vazio, recorre a uma frase de início, evitando uma mensagem user vazia na primeira rodada.
	input := overview
	if strings.TrimSpace(input) == "" {
		input = "Comece a executar a intenção recebida no system: faça só ela, produza apenas fatos, assets, finding, termine e pare."
	}

	// Recurso experimental: quando ativado, o noa assume a compressão de contexto (arquivos em <workDir>/noa/<SessionID>, persistentes).
	noaSession := WorkerSessionID(ts.ID(), intent.ID)
	enableNoa(&opts, w.noaEnabledFn, w.workDir, noaSession, noaWarn(noaSession))
	ctx = attachSideCapture(ctx, &opts)
	s := agentcore.NewSession(opts)
	defer s.Close() // release the session's background-task manager (temp dir + processes)

	// Resume prior conversation if this intent was paused/blocked/exhausted and is
	// being re-run. The transcript ID is deterministic per intent, so if a prior
	// session exists the worker continues from where it left off instead of
	// restarting from scratch.
	alreadyRecorded := false
	if w.tx != nil {
		_ = s.Resume(opts.SessionID)
		alreadyRecorded = requestID != "" && hasWorkerChatMessage(s.Messages(), requestID)
		if len(s.Messages()) > 0 && message == "" {
			seedUnlockFromHistory(s.Messages(), def.UnlockSkill)
			input = "Continue a execução."
		} else if len(s.Messages()) > 0 {
			seedUnlockFromHistory(s.Messages(), def.UnlockSkill)
		}
	}
	if message != "" {
		if alreadyRecorded {
			input = "Continue executando a nova intenção da última entrada de conversa humana. Não repita as ações já concluídas."
		} else if len(s.Messages()) > 0 {
			input = workerChatMarker(requestID) + "\n[Nova intenção da entrada de conversa humana]\n" + message +
				"\n\nExecute imediatamente conforme esta entrada humana e, ao concluir, decida com base no contexto se a tarefa original precisa continuar."
		} else {
			input += "\n\n" + workerChatMarker(requestID) + "\n[Nova intenção da entrada de conversa humana]\n" + message +
				"\n\nExecute com prioridade esta entrada humana."
		}
	}

	// Budgets + settlement are owned by the SDK (MaxTurns/MaxDuration + Settlement):
	// on hit it runs a wrap-up turn and finishes with ReasonMaxTurns/ReasonTimeout.
	// MaxDuration now interrupts an in-flight tool at the wall-clock deadline and
	// enters the wrap-up phase on the live ctx, so a run whose tool overran the budget
	// still settles (no external hard-timeout backstop needed). ctx itself carries only
	// pause / planner kill / shutdown, which the engine distinguishes and re-queues/stops.
	_, reason, err := captureRunSession(ctx, s, input, emitWrap)
	return reason, tsx.Writes(), err
}
