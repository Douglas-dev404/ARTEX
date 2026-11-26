// ARTEX domain model — types used across the UI.
// Derived from the functional spec (section 7: formatos de dados principais).

export type TaskStatus = "created" | "queued" | "running" | "paused" | "done" | "failed" | "timeout";
export type EngineMode = "exploring" | "paused" | "stalled" | "idle";

export interface Task {
  id: string;
  name?: string; // nome opcional da tarefa; vazio/omitido = sem nome, e na exibição usa a descrição como fallback
  category_id?: number;
  category_name?: string;
  pinned?: boolean;
  pinned_at?: string | null;
  description: string;
  goal: string;
  status: TaskStatus;
  created_at: string;
  created_unix?: number; // created_at as unix seconds (run-duration calc)
  completed_at?: string; // RFC3339 finish time (done/failed); "" if unfinished
  completed_unix?: number; // completed_at as unix seconds (0/undef if unfinished)
  last_activity_unix?: number; // unix seconds of the last activity (0/undef if none)
  paused?: boolean;
  queued?: boolean;
  active?: boolean;
  in_flight?: number;
  findings?: { critical: number; high: number; medium: number; low: number }; // número de vulnerabilidades registradas (por faixa de severidade)
  last_activity?: string;
  stalled?: boolean;
  goals_total?: number;
  goals_met?: number;
  engine_mode?: EngineMode;
  tokens?: TokenTotal; // whole-task token consumption
  llm_profile_id?: number; // LLM profile used; absent = default profile
  llm_profile_ids?: number[]; // ordered task-level failover chain
  active_llm_profile_id?: number; // profile used by the next LLM call
  llm_failover_state?: "default" | "ready" | "chain_exhausted" | string;
  llm_failover_reason?: string;
  source_task_ids?: string[]; // directly related tasks inherited as read-only context
  archive_blocked_by_task_id?: string; // live direct dependent that must be archived first
  company_ids?: number[]; // associated company scopes; current company assets join the task at creation
  coverage_enabled?: boolean; // chave do recurso de cobertura de ativos (definida na criação, padrão ativado); false = não calcula/não exibe a cobertura
}

export interface TaskCategory {
  id: number;
  name: string;
  task_count: number;
  created_at: string;
  updated_at: string;
}

export interface TaskTemplate {
  id: number;
  name: string;
  description: string;
  goal: string;
  category_id?: number | null; // categoria predefinida; null/omitido = nenhuma
  intercept_rules?: AssetInterceptRuleInput[]; // regras de interceptação/permissão no nível da tarefa predefinidas
  created_at: string;
  updated_at: string;
}

export interface DeleteTaskOptions {
  delete_assets: boolean;
  delete_traffic: boolean;
  delete_files: boolean;
  delete_findings: boolean;
  delete_llm_records: boolean;
}

export interface DeleteTaskResult {
  deleted: string;
  assets_deleted: number;
  assets_detached: number;
  traffic_deleted: number;
  files_deleted: boolean;
  findings_deleted: number;
  llm_records_deleted: number;
  cleanup_warning?: string;
}

export type TaskArchiveState =
  | "archive_queued"
  | "archiving"
  | "archive_failed"
  | "ready"
  | "restore_queued"
  | "restoring"
  | "restore_failed"
  | "delete_queued"
  | "deleting"
  | "delete_failed";

export interface TaskArchiveTokenStats {
  calls?: number;
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
}

export interface TaskArchive {
  id: number;
  task_id: number;
  state: TaskArchiveState;
  phase: string;
  progress: number;
  error?: string;
  warnings?: string[];
  format_version: number;
  sha256?: string;
  original_size: number;
  compressed_size: number;
  task_name: string;
  task_description: string;
  task_goal: string;
  original_status: TaskStatus;
  category_id?: number;
  category_name?: string;
  source_task_ids: number[];
  remaining_timeout_seconds: number;
  data_counts: Record<string, number>;
  aggregate_stats: {
    tokens?: TaskArchiveTokenStats;
    skills?: Record<string, number>;
    tools?: Record<string, number>;
    findings?: Record<string, number>;
  };
  archived_at?: string;
  requested_at: string;
  created_at: string;
  updated_at: string;
}

export interface TaskArchivePage {
  items: TaskArchive[];
  total: number;
  page: number;
  size: number;
}

export interface ArchiveBatchItem {
  id: string;
  archive_id?: number;
  ok: boolean;
  queued: boolean;
  error?: string;
}

// ---- Asset graph (global, shared across tasks) ----
export type AssetType =
  | "company"
  | "domain"
  | "ip"
  | "port"
  | "service"
  | "site"
  | "endpoint"
  | "parameter"
  | "tech"
  | "credential"
  | "data";

export type NodeState = "observed" | "confirmed" | "tombstoned";

export interface AssetNode {
  id: string;
  type: AssetType;
  name: string;
  key: string; // nkey
  value?: string;
  company_id?: string; // id do ativo da empresa proprietária; vazio = sem atribuição
  state: NodeState;
  confidence: number; // 0..1
  attrs?: Record<string, unknown>;
  first_seen: string;
  last_seen: string;
}

export type AssetRel =
  | "owns"
  | "resolves"
  | "exposes"
  | "runs"
  | "serves"
  | "has_endpoint"
  | "has_param"
  | "fingerprinted"
  | "authenticates_as"
  | "reachable"
  | "has_subdomain";

export interface Edge {
  src: string;
  dst: string;
  rel: AssetRel | ExploreRel;
}

// Task asset view — server-side enriched, paginated.
export interface TaskAssetRef {
  id: string;
  name?: string;
  key: string;
  attrs?: Record<string, unknown>;
}

export interface TaskAssetItem extends AssetNode {
  techs?: TaskAssetRef[];
  auth?: TaskAssetRef[];
  params?: TaskAssetRef[];
}

export interface TaskAssetView {
  counts: Record<string, number>;
  total: number;
  items: TaskAssetItem[];
}

// ---- New unified asset model (new backend) ----
export type NewAssetType = "root_domain" | "ip" | "subdomain" | "app" | "service" | "endpoint";

export interface Asset {
  id: number;
  type: NewAssetType;
  company_id?: number;
  task_ids: number[];
  domain?: string;
  root_domain?: string;
  ip?: string;
  c_segment?: string;
  port?: number;
  icp?: string;
  bound_domains?: string[];
  open_ports?: { port: number; service?: string }[];
  record_type?: string;
  record_value?: string[] | string;
  bundle_id?: string;
  app_name?: string;
  category?: string;
  app_description?: string;
  app_icp?: string;
  url?: string;
  service_type?: string;
  service_name?: string;
  favicon_mmh3?: string;
  status_code?: number;
  content_length?: number;
  page_title?: string;
  technologies?: string[];
  auth?: Record<string, unknown>[];
  method?: string;
  params?: Record<string, unknown>[];
  extra?: Record<string, unknown>;
  last_seen: string;
  task_source?: string;
  task_source_summary?: string;
  task_source_node_id?: number;
}

export interface IntentAsset {
  intent_id: number | string;
  asset_id: number;
  type: NewAssetType;
  label: string;
  source: string;
  source_summary: string;
  source_node_id?: number;
  source_task_id: number;
  inherited: boolean;
}

export interface TaskAssetMutation {
  requested: number;
  attached: number;
  existing: number;
}

export interface TaskAssetScopeMutation {
  requested: number;
  assets_linked: number;
  assets_existing: number;
  scopes_added: number;
  scopes_existing: number;
}

// ---- Asset coverage graph (per task) ----
// Um nó do "grafo de cobertura de ativos" dirigido por forças. key é único: ativo="a:<id>", empresa="c:<id>",
// domínio raiz sem linha de ativo="r:<domain>". in_scope=false são nós de contexto cinza usados apenas para conexão.
export interface CoverageGraphNode {
  key: string;
  kind: "company" | "root_domain" | "subdomain" | "ip" | "service" | "app" | "endpoint";
  label: string;
  tested: boolean;
  in_scope: boolean;
  asset_id?: number;
  company_id?: number;
  domain?: string;
  root_domain?: string;
  ip?: string;
  url?: string;
  port?: number;
  service_type?: string;
  app_name?: string;
  page_title?: string;
  status_code?: number;
}

export interface CoverageGraphEdge {
  src: string;
  dst: string;
}

export interface CoverageGraphData {
  nodes: CoverageGraphNode[];
  edges: CoverageGraphEdge[];
}

// Intenções/fatos/achados associados a um ativo no grafo de exploração desta tarefa (usado na gaveta de nó do grafo de cobertura).
export interface CoverageAssetRef {
  id: number;
  kind: string;
  state: string;
  summary: string;
  source_task_id?: string;
  inherited?: boolean;
}
export interface CoverageAssetRefs {
  intents: CoverageAssetRef[];
  facts: CoverageAssetRef[];
  findings: CoverageAssetRef[];
}

// ---- Workspace file manager (workDir) ----
export interface WorkspaceEntry {
  name: string;
  path: string; // workspace-relative, forward slashes
  dir: boolean;
  size: number;
  mtime: number; // unix millis
}
export interface WorkspaceListing {
  path: string;
  entries: WorkspaceEntry[];
}
export interface WorkspaceFile {
  path: string;
  size: number;
  binary: boolean;
  too_large?: boolean;
  content?: string;
}

// Um item do escopo de teste da tarefa (denominador da cobertura + limite de autorização).
export interface TaskScopeRow {
  id: number;
  task_id: number;
  kind: "company" | "root_domain" | "subdomain" | "ip" | "cidr" | "icp" | "keyword";
  company_id?: number;
  company_name?: string; // resolvido pelo backend via JOIN em companies; só tem valor quando kind=company
  domain?: string;
  net?: string;
  value?: string;
  source: "auto" | "agent" | "manual";
  reason?: string;
}

export type CompanyScopeKind = "domain" | "ip" | "cidr" | "icp" | "keyword";

// Regras estruturadas de escopo de ativos enviadas ao adicionar uma empresa.
export interface CompanyScopeRule {
  kind: CompanyScopeKind;
  value: string;
}

// Resultado da gravação do escopo de ativos. errors são as linhas inválidas desta submissão; warnings são problemas em dados já existentes, sem relação com esta submissão,
// mas que fazem o resultado de atribuição divergir do esperado (por exemplo, ativos com nome de host gravado no campo ip).
export interface CompanyScopeMutation {
  added: number;
  skipped: number;
  invalid: number;
  errors?: string[];
  warnings?: string[];
}

// Um item das regras de escopo de ativos da empresa (única fonte de verdade da atribuição).
export interface ScopeRow {
  id: number;
  company_id: number;
  kind: CompanyScopeKind;
  domain?: string; // tem valor quando kind=domain
  net?: string; // tem valor quando kind=ip|cidr
  value?: string; // quando kind=icp|keyword, pode ser retornado diretamente pelo backend
  raw: string; // entrada original do usuário, usada para exibir e preencher de volta
  reason?: string;
}

// Empresa: nó de ativo com type=company + ícone + contagem de ativos + regras de escopo de ativos.
export interface Company {
  id: number;
  name: string;
  logo?: string; // URL do ícone remoto; se vazio, o frontend usa a inicial do nome
  asset_count: number;
  scope?: ScopeRow[];
}

// ---- Exploration graph (per task) ----
export type ExploreKind = "task" | "begin" | "goal" | "intent" | "fact" | "finding" | "hint" | "digest";
export type GoalState = "open" | "met" | "abandoned";
export type IntentState = "open" | "running" | "paused" | "done" | "blocked" | "exhausted" | "stopped";
export type FindingState = "confirmed" | "dismissed";
export type HintState = "active" | "consumed";
export type ExploreRel = "spawns" | "derived_from" | "yields" | "proves" | "covers";

export interface TaskNode {
  id: string;
  type: ExploreKind;
  payload?: string;
  priority: number; // 0..10
  state: string; // GoalState | IntentState | FindingState | HintState
  origin: string;
  ts: string;
  source_task_id?: string;
  inherited?: boolean;
  delete_reason?: string; // motivo da exclusão quando a intenção sofre exclusão lógica (state='deleted')
}

// Uma página do quadro de transmissão: nós paginados por ordem de criação + as arestas envolvidas nesta página + os nós da outra ponta das arestas (refs, indexados por id),
// assim cada transmissão consegue dizer "de onde veio e o que produziu", sem precisar baixar o grafo inteiro.
export interface ExplorationNodePage {
  items: TaskNode[];
  total: number;
  page: number;
  size: number;
  edges: Edge[];
  refs: Record<string, TaskNode>;
  // id do nó → ativo ancorado por esse nó (exibido junto ao expandir o quadro de transmissão, incluindo os nós desta página e seus vizinhos).
  assets: Record<string, FindingAsset[]>;
}

export interface ExplorationNodeQuery {
  page?: number;
  size?: number;
  kinds?: ExploreKind[];
  states?: string[];
  q?: string;
  order?: "asc" | "desc";
}

// Objetivo usado pelo cartão de gerenciamento de objetivos (o backend já separou o payload em text/vulnclass).
export interface TaskGoal {
  id: string;
  text: string;
  vulnclass?: string;
  state: string; // GoalState
  origin?: string;
  ts: string;
}

// Restrição de operação usada pelo cartão de gerenciamento de restrições (allow = permitir / deny = proibir).
export type ConstraintKind = "allow" | "deny";
export interface TaskConstraint {
  id: string;
  kind: ConstraintKind;
  text: string;
  origin?: string;
  ts?: string;
}

// ---- Findings ----
export type Severity = "critical" | "high" | "medium" | "low";

// Estado de tratamento da vulnerabilidade: pendente / em tratamento / confirmado / tratado / corrigido / falso positivo / ignorado / duplicado / risco aceito.
export type FindingStatus =
  | "pending"
  | "in_progress"
  | "confirmed"
  | "resolved"
  | "fixed"
  | "false_positive"
  | "ignored"
  | "duplicate"
  | "risk_accepted";

// FindingAsset é um ativo vinculado a uma vulnerabilidade (label já pré-renderizado no backend).
export interface FindingAsset {
  id: string;
  type: string;
  label: string;
}

export interface Finding {
  traffic_count?: number;
  evidence_version?: number;
  report_evidence_version?: number;
  report_stale?: boolean;
  id: string;
  finding_id?: string; // id da linha na tabela findings independente, handle para atualização de estado (nós antigos dentro da tarefa podem não ter)
  vulnclass: string;
  name?: string; // nome da vulnerabilidade; quando vazio, a exibição usa vulnclass como fallback
  severity: Severity;
  status: FindingStatus;
  summary: string;
  evidence: string;
  report?: string; // relatório detalhado (Markdown); retornado apenas pela API de detalhes, vazio na lista
  intent_id?: string;
  param_id?: string;
  task_id?: string;
  task_description?: string;
  source_task_id?: string;
  inherited?: boolean;
  assets?: FindingAsset[];
  ts: string;
}

// FindingsPage é a resposta paginada pelo servidor da lista de achados.
export interface FindingsPage {
  items: Finding[];
  total: number;
  page: number;
  page_size: number;
}

export interface FindingGroup {
  task_id: string | number | null;
  task_name?: string; // nome opcional da tarefa; vazio/omitido = sem nome
  task_description: string;
  task_status: string;
  count: number;
  critical: number;
  high: number;
  medium: number;
  low: number;
  last_found_at: string;
}

export interface FindingGroupsPage {
  items: FindingGroup[];
  total: number;
  finding_total: number;
  page: number;
  page_size: number;
}

export interface FindingDeepenResponse {
  task_id: string;
  intent_id: string;
  state: IntentState;
  queued: boolean;
}

// FindingStats é a agregação da tabela inteira de achados (cards de estatística + dropdown de classe de vulnerabilidade), calculada no servidor, não afetada pela paginação.
export interface FindingStats {
  total: number;
  pending: number;
  critical: number;
  high: number;
  medium: number;
  low: number;
  vulnclasses: string[];
  tasks: FindingTaskOption[];
}

// FindingTaskOption é um item do dropdown de filtro "por tarefa" da página de achados: tarefas com vulnerabilidades (descrição vazia indica que a tarefa foi excluída,
// e o frontend exibe o id como fallback) e a quantidade de vulnerabilidades.
export interface FindingTaskOption {
  id: string | number;
  name?: string; // nome opcional da tarefa; vazio/omitido = sem nome
  description: string;
  count: number;
}

// FindingQuery são os parâmetros de paginação/filtro/ordenação da lista de achados.
export interface FindingQuery {
  page: number;
  pageSize: number;
  severity?: "all" | Severity;
  status?: "all" | FindingStatus;
  vulnclass?: string;
  task?: string; // id da tarefa; "all"/vazio = não filtra por tarefa
  query?: string;
  sort?: "severity" | "time";
  // key do nó da árvore de ativos; selecionar um nó = selecionar toda a sua subárvore. Vazio = não filtra por ativo.
  assetScope?: string;
}

// ---- Findings by asset (visão por ativo) ----
export type FindingAssetKind = "company" | "root_domain" | "subdomain" | "ip" | "service" | "app" | "endpoint" | "none";

// FindingAssetNode é um nó da árvore de ativos. key tem a forma a:<id> (ativo), c:<id> (empresa),
// r:<domain> (domínio raiz sem linha de ativo no banco), __none__ (sem ativo associado).
export interface FindingAssetNode {
  key: string;
  parent?: string;
  kind: FindingAssetKind;
  label: string;
  asset_id?: number;
  company_id?: number;
  self: number; // número de achados pendurados diretamente nesse ativo
  total: number; // inclui descendentes, deduplicado por achado
  critical: number;
  high: number;
  medium: number;
  low: number;
  last_found_at: string;
}

export interface FindingAssetTree {
  nodes: FindingAssetNode[];
  finding_total: number;
  truncated: boolean;
  dropped_kinds?: string[];
}

// FINDING_UNASSIGNED_ASSET corresponde a db.FindingUnassignedAsset do backend.
export const FINDING_UNASSIGNED_ASSET = "__none__";

// ---- Activity / sessions ----
export type ActivityKind =
  | "tool_use"
  | "tool_result"
  | "text"
  | "thinking"
  | "result"
  | "user"
  | "intent" // LLM-generated exploration objective leading a worker session (UI-synthesized)
  | "round" // planner round boundary marker (engine-emitted)
  | "usage" // live cumulative token usage (per model turn); not rendered
  | "llm_switch" // automatic/manual task-level LLM switch
  | "llm_failover" // task-level provider switch / chain exhaustion audit event
  | "intercept_request"; // user-approval request from the intercept layer

// ChatAttachment é um arquivo enviado: path relativo ao diretório de trabalho da sessão/tarefa (ou seja, o CWD do agent).
export interface ChatAttachment {
  name: string;
  path: string;
  size: number;
  abs?: string; // caminho absoluto (retornado em uploads temporários com scope=staging; escreva-o na descrição antes de criar a tarefa)
}

export interface Activity {
  seq: number;
  intent_id?: string;
  worker: string; // session owner: planner | mainagent | work#1 ...
  ts: string;
  kind: ActivityKind;
  tool?: string;
  tool_use_id?: string;
  is_error?: boolean;
  summary: string;
  detail?: string;
  metadata?: {
    llm_transition?: LLMTransition;
  };
  source_task_id?: string;
  inherited?: boolean;
  main_seg?: number; // main-agent conversation segment (present only on worker="mainagent" rows)
  // token usage (present only on kind='result')
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
}

export interface LLMAuditProfile {
  id: number;
  name: string;
  format: string;
  model: string;
}

export interface LLMTransition {
  mode: "automatic" | "manual" | "exhausted";
  reason: string;
  previous?: LLMAuditProfile;
  next?: LLMAuditProfile;
}

export interface TaskLLMResolution {
  profile_id?: number;
  name: string;
  format: string;
  model: string;
  source: "task_chain" | "agent_binding" | "global_profile" | "environment" | "global";
  available: boolean;
  reason?: string;
}

export interface TaskLLMResolutions {
  mainagent: TaskLLMResolution;
  planner: TaskLLMResolution;
  worker: TaskLLMResolution;
}

// ---- Agent triggers (agendamento P3, apenas agents personalizados) ----
export interface AgentTrigger {
  id: number;
  agent_key: string;
  enabled: boolean;
  interval_sec: number; // agendado: a cada N segundos (0 = sem agendamento)
  on_finding: boolean; // dispara quando qualquer tarefa encontrar um finding
  on_goal_met: boolean; // dispara quando qualquer tarefa atingir o objetivo
  on_task_timeout: boolean; // dispara quando qualquer tarefa expirar por timeout
  on_tool_call: boolean; // dispara quando a ferramenta selecionada for chamada (execução concluída)
  on_task_create: boolean; // dispara quando qualquer tarefa for criada
  interval_message: string; // mensagem de usuário independente de cada condição de gatilho
  finding_message: string;
  goal_message: string;
  task_timeout_message: string;
  tool_call_message: string;
  task_create_message: string;
  tool_names: string[]; // key das ferramentas selecionadas em on_tool_call (ao menos uma)
  last_fire?: string;
}

// ---- Conversations (chat page) ----
export interface ActiveFindingRetest {
  id: number;
  finding_id: string;
  conversation_id: number;
  status: "pending" | "running";
}

export interface FindingRetest {
  id: number;
  finding_id: number;
  conversation_id: number | null;
  status: "pending" | "running" | "completed" | "failed" | "stopped";
  verdict: "" | "reproduced" | "fixed" | "inconclusive";
  notes: string;
  summary: string;
  evidence: string;
  error: string;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
}

export interface Conversation {
  id: number;
  running?: boolean; // live server state, returned with the conversation list
  agent_key: string;
  title: string;
  llm_profile_id?: number;
  pinned?: boolean;
  pinned_at?: string | null;
  created_at: string;
  updated_at: string;
}

// ---- Backend logs (/logs page) ----
export interface LogLine {
  seq: number;
  db_id?: number; // server_logs.id; present for DB-persisted lines
  ts: string;
  level: "info" | "warn" | "error";
  tag: string;
  text: string;
}

export type SessionRole = "mainagent" | "planner" | "worker" | "system";
export type SessionStatus = "running" | "paused" | "done" | "blocked" | "exhausted" | "pending" | "stopped" | "deleted";

// Daily token aggregate bucket (GET /api/tokens/daily).
export interface DailyTokenBucket {
  date: string; // "YYYY-MM-DD"
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// Per-worker token usage (GET /api/exploration/tokens).
export interface TokenUsage {
  worker: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

export interface SessionTokenUsage {
  session: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

export interface BatchControlItem {
  id: string;
  ok: boolean;
  status?: string;
  queued?: boolean;
  error?: string;
}

// Resultado por tarefa da alteração de categoria em lote. A falha só pode ser porque a tarefa já foi excluída; a gravação da categoria em si é atômica.
export interface BatchCategoryItem {
  id: string;
  ok: boolean;
  error?: string;
}

// Whole-task (all agents) token aggregate.
export interface TokenTotal {
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// Global per-profile token spend from the llm_usage ledger (GET /api/tokens/usage).
export interface ProfileUsage {
  profile_name: string;
  calls: number;
  tasks: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// One (profile, UTC day) token bucket for the dashboard's daily chart (new source).
export interface ProfileDayUsage {
  profile_name: string;
  date: string; // YYYY-MM-DD
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
}

// Response of GET /api/tokens/usage — the dashboard's "new" (llm_usage) token view.
export interface UsageStats {
  by_profile: ProfileUsage[];
  daily: ProfileDayUsage[];
}

// Per-model token usage for one task (GET /api/llm/records/by-model), from the
// always-on llm_usage metering ledger. calls = number of LLM calls on this model.
export interface ModelTokenStat {
  model: string;
  calls: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

export interface Session {
  id: string;
  role: SessionRole;
  title: string;
  status: SessionStatus;
  live: boolean;
  last_activity: string;
  intent_id?: string;
  source_task_id?: string;
  inherited?: boolean;
  seg?: number; // main-agent session: which conversation segment (0 = original)
}

// ---- Security ----
export interface AuditEntry {
  ts: string;
  tool: string;
  action: "allow" | "block";
  reason?: string;
  command?: string;
}

export interface Audit {
  entries?: AuditEntry[];
  attributions?: Record<string, number>;
}

// ---- Traffic ----
export interface TrafficExchange {
  id: string;
  ts: string;
  host: string;
  method: string;
  url: string;
  status: number;
  content_type: string;
  resp_len: number;
}

export interface TrafficResp {
  enabled: boolean;
  proxy?: string;
  count?: number; // global total (unfiltered)
  total?: number; // rows matching the current filter (for pagination)
  page?: number;
  size?: number;
  exchanges?: TrafficExchange[];
}

// Full raw request/response of one exchange (lazy-loaded on row select).
export interface TrafficDetail {
  req: string;
  resp: string;
}

// One distinct recorded host with its exchange count (target picker).
export interface TrafficHost {
  host: string;
  count: number;
}

// ---- App settings (runtime toggles) ----
export interface Settings {
  traffic_capture: boolean;
  agent_traffic_binding: boolean; // o Agent vincula evidência de tráfego automaticamente, padrão desativado; não afeta o vínculo manual
  llm_record: boolean; // chave de gravação de LLM (padrão desativada); quando desativada, nenhuma chamada de LLM é registrada
  // Web search. brave_key_set / tavily_key_set reflect whether a key is stored
  // (the values are never returned). On PUT, send the corresponding field to set/clear.
  web_search_enabled: boolean;
  web_search_backend: string; // "ddgs" | "brave-free" | "tavily" | "deepseek"
  brave_key_set: boolean;
  tavily_key_set: boolean;
  // write-only: only sent on PUT to store/clear the key.
  brave_search_api_key?: string;
  tavily_search_api_key?: string;
  // Proxy de saída independente (http/https/socks5), usado para acessar endpoints de busca; sem relação com o proxy MITM que registra o tráfego. Vazio = conexão direta.
  web_search_proxy?: string;
  // Proxy de saída global (http/https/socks5, pode incluir user:pass); todo o tráfego dos alvos passa por ele. Com a captura de tráfego ativada, serve de
  // upstream do MITM; com a captura desativada, é injetado direto no bash/WebFetch do agent. Vazio = conexão direta.
  global_proxy?: string;
  python_interpreter?: string; // caminho do interpretador python das ferramentas de script personalizadas (vazio = detectado em tempo de execução)
  workers?: number; // número de agents worker concorrentes (padrão 3); vale para tarefas iniciadas depois
  // Limite de concorrência de tarefas: máximo de tarefas "em execução" ao mesmo tempo. Desativado = sem limite; ativado, novas tarefas acima do limite entram na fila e iniciam sozinhas quando houver vaga.
  task_concurrency_enabled?: boolean; // padrão false
  task_concurrency_limit?: number; // padrão 5 quando ativado
  // Rodízio de LLM (failover). Padrão desativado; ativado, agents "sem modelo especificado" trocam automaticamente para a próxima configuração quando a atual fica indisponível
  // (saldo insuficiente/key inválida/limite de taxa/falha do serviço).
  llm_pool_enabled?: boolean; // padrão false
  // Se agents/tarefas vinculados a uma configuração específica também recuam para a cadeia de rodízio em caso de falha. Padrão false = vínculo é exclusivo.
  llm_pool_bind_fallback?: boolean;
  // Escopo de injeção das restrições de operação (todos ativos por padrão): inclui as restrições allow/deny da tarefa no system prompt do agent correspondente.
  constraints_inject_planner?: boolean;
  constraints_inject_worker?: boolean;
  // Recurso experimental: compressão de contexto dirigida pelo modelo noa (padrão desativado). Ativado, os quatro tipos de agent integrados à plataforma (planner/
  // worker/agent principal/conversa) têm a compressão de contexto assumida pelo noa, substituindo a compaction embutida; lido uma vez por run, vale para
  // runs iniciados depois.
  noa_compaction?: boolean;
  // ---- Push de vulnerabilidades por IM (o canal em si é um recurso independente, ver /api/notify/*; aqui há apenas três configurações globais) ----
  notify_enabled?: boolean; // chave geral de push, padrão ativada; usada para estancar tudo de uma vez durante manutenção
  notify_public_base_url?: string; // endereço de acesso externo do link de volta aos detalhes da vulnerabilidade; vazio = a mensagem não leva link de volta
  notify_digest_interval_min?: number; // período do modo resumo (minutos), padrão 30
}

// ---- Push de vulnerabilidades por IM ----

// NotificationFilter são as condições de filtro do canal; todos os campos são opcionais e, se omitidos, não há filtro.
// O backend não valida nenhum campo: com configuração malformada, trata como "correspondeu" (melhor empurrar a mais do que deixar de empurrar).
export interface NotificationFilter {
  min_severity?: string; // "" | low | medium | high | critical
  task_ids?: number[]; // vazio = sem limite; não vazio exige interseção com a tarefa a que a vulnerabilidade pertence
  asset_ids?: number[]; // vazio = sem limite; não vazio exige interseção com os ativos ancorados pela vulnerabilidade
  vulnclass_include?: string[]; // vazio = aceita tudo; não vazio exige que a classe da vulnerabilidade corresponda a qualquer palavra-chave (substring, sem distinção de maiúsculas)
  vulnclass_exclude?: string[]; // corresponder a qualquer palavra-chave exclui (exclusão tem prioridade sobre inclusão)
  on_status_change?: boolean; // se também recebe eventos de mudança de estado de tratamento da vulnerabilidade
}

// NotificationChannel é uma instância de canal. Os campos de config variam conforme kind,
// e os campos de credencial, ao serem lidos, são substituídos por valores mascarados começando com "__masked__"; devolvê-los como estão significa "não alterar".
export interface NotificationChannel {
  id: number;
  name: string;
  kind: string;
  enabled: boolean;
  mode: "realtime" | "digest";
  config: Record<string, unknown>;
  filter: NotificationFilter;
  rate_per_min: number;
  created_at: string;
  updated_at: string;
  // secret_keys é fornecido pelo backend conforme o tipo de canal, e o frontend renderiza com base nele a caixa de senha e a dica "deixe em branco para não alterar",
  // sem codificar nenhum conhecimento de canal.
  secret_keys: string[];
}

// NotificationKind são os metadados do tipo de canal retornados por /api/notify/meta.
export interface NotificationKind {
  kind: string;
  default_rate_per_min: number;
  secret_keys: string[];
}

export interface NotificationMeta {
  kinds: NotificationKind[];
  enabled: boolean;
  public_base_url: string;
  digest_interval_min: string;
  defaults: { digest_interval_min: number };
  stats: {
    channels: number;
    channels_on: number;
    pending: number;
    failed: number;
    sent_today: number;
    backlog_age_ms: number;
  };
}

// NotificationDelivery é um registro de entrega, usado no histórico de entregas e no reenvio de falhas.
export interface NotificationDelivery {
  id: number;
  finding_id: string;
  event_kind: string; // finding_created | finding_status_changed
  channel_id: number;
  channel_name: string;
  channel_kind: string;
  state: "pending" | "sending" | "sent" | "failed" | "skipped";
  attempts: number;
  last_error: string;
  batch_id?: number;
  created_at: string;
  sent_at?: string;
  next_attempt_at: string;
  title: string;
  severity: string;
}

// ---- LLM config ----
export interface LLMProfile {
  id: string;
  name: string;
  format: "openai" | "anthropic" | "openai-responses";
  base_url?: string;
  proxy?: string;
  model: string;
  api_key_hint?: string;
  rate_per_second: number;
  rate_per_minute: number;
  context_window_k?: number;
  // Chave de thinking (thinking.type): ""=não envia (padrão) | "disabled"=desativa | "enabled"=ativa
  thinking_type?: string;
  // Intensidade de thinking: ""=não envia (padrão) | "low"/"medium"/"high"/"xhigh"/"max"
  reasoning_effort?: string;
  is_default: boolean;
  // Ordem no rodízio: quanto maior, mais cedo é selecionado. A configuração ativa é sempre a primeira da cadeia, independentemente deste valor.
  priority?: number;
  // true = não serve como alvo de failover (ainda pode ser vinculada explicitamente por agent/tarefa).
  pool_exclude?: boolean;
  // true (padrão) = streaming (SSE) | false = não-streaming de verdade (stream:false, retorno de uma só vez).
  streaming?: boolean;
  // Limite de saída por resposta (token). 0 = não envia esse campo, e o padrão do servidor decide.
  // Atenção para distinguir de context_window_k: este é a capacidade total do modelo, usada só localmente para o limiar de compressão.
  max_tokens?: number;
  // Qual nome de campo da requisição é usado para o limite; só faz sentido com format="openai":
  // ""=max_tokens (padrão) | "max_completion_tokens" (os modelos de raciocínio da OpenAI só aceitam este)
  max_tokens_field?: string;
  // Nome do cabeçalho de sessão personalizado: se não vazio, cada requisição leva esse cabeçalho HTTP, com valor igual ao session id da sessão/intenção atual.
  // ""=não envia. Usado por gateways que fazem cache de prompt/roteamento sticky pelo cabeçalho de session-id.
  session_header_key?: string;
  // Sobrescrita desta configuração para retry (conexão/resposta vazia/janela segura do mesmo provider). Vazio/tudo 0 = segue a política global.
  retry?: LLMRetryOverride;
}

// ---- Política de retry de LLM ----
// Os dois botões de uma camada de retry. Ambos usam "0 = não configurado":
//   attempts    0=usa o número padrão de tentativas | -1=desativa o retry desta camada | >0=número de tentativas
//   interval_ms 0=usa o backoff exponencial padrão | >0=usa este intervalo fixo em milissegundos
export interface LLMRetryRule {
  attempts: number;
  interval_ms: number;
}

// As três camadas que uma configuração de LLM individual pode sobrescrever (todos são retries "que acompanham o endpoint").
export interface LLMRetryOverride {
  connect: LLMRetryRule; // retry de conexão: reset de conexão/timeout/429/5xx, antes do início do stream
  empty: LLMRetryRule; // retry de resposta vazia: concluída mas sem nenhum conteúdo (apenas formato openai)
  stream: LLMRetryRule; // retry na janela segura do mesmo provider: replay de stream cortado antes de entregar saída
}

// Política global = valores padrão das três camadas acima + duas camadas exclusivamente globais:
//   breaker  circuit breaker do rodízio (attempts = quantas falhas transitórias consecutivas acionam o breaker, interval_ms = duração fixa do cooldown)
//   intent   reexecução de intenção (quando o worker termina com model_error, a intenção inteira é reexecutada)
export interface LLMRetryPolicy extends LLMRetryOverride {
  breaker: LLMRetryRule;
  intent: LLMRetryRule;
}

// ---- Rodízio de LLM (failover) ----
// Posição e estado de saúde de uma configuração na cadeia de rodízio. state:
//   ok       normal
//   degraded há falhas consecutivas, mas sem atingir o limiar do breaker
//   tripped  breaker acionado, ignorada durante o cooldown (cooldown_secs é o número de segundos restantes)
export interface LLMPoolMember {
  profile_id: string;
  name: string;
  model: string;
  format: string;
  priority: number;
  active: boolean; // se é a configuração ativa atual (sempre a primeira da cadeia)
  excluded: boolean; // pool_exclude: não participa do rodízio
  state: "ok" | "degraded" | "tripped";
  fails: number;
  trips: number;
  cooldown_secs: number;
  last_error?: string;
  last_at?: string;
}

export interface LLMPoolStatus {
  enabled: boolean;
  bind_fallback: boolean;
  chain: LLMPoolMember[];
}

// ---- Agents ----
export interface Agent {
  id: string;
  key: string; // os embutidos são goals/planner/mainagent/worker; os personalizados têm key definida pelo usuário
  name: string;
  description?: string;
  role: string;
  builtin: boolean;
  enabled: boolean;
  llm_profile_id?: number | null; // configuração de LLM vinculada; null/absent = segue tarefa/sessão/global
  max_turns?: number; // 0 = sem limite
  run_seconds?: number; // limite de tempo (wall-clock) de uma execução do worker (segundos); 0 = sem limite
  web_search?: boolean; // se a busca na web está ativada (controlada pela chave global do sistema)
  interactive_shell?: boolean; // se o shell interativo está ativado (família de ferramentas de sessão PTY persistente)
  // Política de tratamento pós-gatilho P3 (só faz sentido para agents personalizados)
  trigger_run_mode?: "serial" | "parallel"; // serial na fila / cada disparo em uma sessão concorrente própria
  trigger_merge_mode?: "by_task" | "all" | "none"; // apenas serial: mesclar na mesma tarefa / mesclar tudo / não mesclar
  trigger_max_parallel?: number; // apenas parallel: limite de concorrência por agent; 0 = sem limite
  // Contagens de vínculos (retornadas apenas pela API de lista): MCP visíveis / Skills visíveis / ferramentas vinculadas
  mcp_count?: number;
  skill_count?: number;
  tool_count?: number;
}

export interface PromptVar {
  name: string;
  description: string;
  example: string;
  source: "exploration" | "runtime" | "distilled";
}

export interface PromptVersion {
  version: number;
  ts: string;
  note: string;
  template_text: string;
}

export interface AgentDetail {
  agent: Agent;
  prompt: string;
  variables: PromptVar[];
  versions: PromptVersion[];
  visibility: { mcp: number[]; skill: string[] };
  // Candidatos de configuração de LLM vinculáveis (para o dropdown de "modelo padrão"); o vínculo atual está em agent.llm_profile_id
  llm_profiles?: { id: number; name: string; model: string; is_default: boolean }[];
  wrapup_prompt?: string; // prompt de encerramento salvo (vazio = usa o padrão embutido)
  wrapup_default?: string; // prompt de encerramento padrão embutido (placeholder/restaurar padrão)
  wrapup_max_turns?: number; // número de rodadas de encerramento salvo (0 = usa o padrão embutido)
  wrapup_max_turns_default?: number; // número de rodadas de encerramento padrão embutido (para a dica "0 = padrão N")
  // Prompt de encerramento por timeout no nível da tarefa (apenas worker/planner; essa seção só é exibida quando task_timeout_wrapup_supported=true)
  task_timeout_wrapup_supported?: boolean;
  task_timeout_wrapup_prompt?: string;
  task_timeout_wrapup_default?: string;
  task_timeout_wrapup_max_turns?: number;
  task_timeout_wrapup_max_turns_default?: number;
}

// ---- MCP ----
export interface MCPServer {
  id: number;
  name: string;
  transport: "stdio" | "http" | "sse";
  command?: string;
  args: string[];
  env: Record<string, string>;
  url?: string;
  enabled: boolean;
  insecure?: boolean; // http: skip TLS cert verification (self-signed servers)
  tools?: string[]; // mcp_tools_cache (names only, for the count)
}

export interface MCPTool {
  name: string;
  description: string;
}

// ---- Skills ----
// Fields align with the agentskills.io open specification.
// description covers both "what the skill does" and "when to use it".
export interface SkillItem {
  name: string; // unique key = directory name
  description?: string; // required per spec; covers what + when to use
  license?: string; // optional: SPDX identifier or free text
  compatibility?: string; // optional: environment requirements
  mcps?: string[]; // MCP server names this skill unlocks on load
  files: string[]; // files in the skill directory
  // Estatísticas de chamadas (livro-razão skill_usage). Skill nunca chamada: calls=0, last_used omitido.
  calls: number;
  tasks: number; // número de tarefas que a carregaram (sessões de chat não contam)
  usage_agents: string[]; // key dos agents que a carregaram
  last_used?: string;
}

// SkillCall é uma chamada de Skill() (lista das chamadas recentes de uma única skill).
export interface SkillCall {
  ts: string;
  agent_key: string;
  task_id: number; // 0 = cenário que não é de tarefa (sessão de conversa)
  session_id: string;
  args_len: number;
}

// MissingSkill é uma skill citada que não existe — a lacuna de "queria usar mas não tem".
export interface MissingSkill {
  skill: string;
  calls: number;
  agents: string[];
  last_used?: string;
}

// ---- Tools (catálogo de ferramentas embutidas) ----
// key + handler live in Go; only these fields are page-editable. system tools lock
// the key and the parameter *structure* (name/type/required) — the per-param
// description/default and the agent binding are what move.
export interface Tool {
  key: string;
  system: boolean;
  description: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  schema: Record<string, any>; // full JSON-Schema (object with properties)
  agents: string[]; // bound agent keys
  enabled: boolean;
  kind?: "builtin" | "shell" | "command" | "script" | "http"; // tipo da ferramenta personalizada
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  exec?: Record<string, any>; // especificação de execução da ferramenta personalizada (kind!=builtin)
  deferred?: boolean; // schema diferido (SearchExtraTools/ExecuteExtraTool)
  calls?: number; // persistent runtime invocation count (older APIs may omit it)
}

// ---- Stats ----
export interface Stats {
  assets: number;
  engine_mode: EngineMode;
  llm_configured: boolean;
  roe_enabled: boolean;
  findings_confirmed: number;
  active_task?: Partial<Task>;
}

// ---- Intercept Rules ----
export type InterceptAction = "allow" | "deny" | "ask";
export type InterceptMatchTarget = "tool_name" | "tool_input";
export type InterceptMatchType = "string" | "regex";

export interface InterceptRule {
  id: number;
  name: string;
  enabled: boolean;
  priority: number;
  match_target: InterceptMatchTarget;
  match_type: InterceptMatchType;
  pattern: string;
  action: InterceptAction;
  message: string;
  timeout_enabled: boolean;
  timeout_seconds: number;
  timeout_action: "deny" | "allow";
  created_at: string;
  updated_at: string;
}

// ---- Asset Intercept Rules (interceptação de ativos: blacklist global) ----
export type AssetInterceptKind =
  | "exact_domain"
  | "exact_ip"
  | "exact_url"
  | "fuzzy_domain"
  | "fuzzy_ip"
  | "fuzzy_url"
  | "cidr";

// action é usado apenas em regras no nível da tarefa: block = interceptar (proibido testar) allow = permitir (whitelist).
export type AssetInterceptAction = "block" | "allow";

// Item de entrada de regra de interceptação/permissão de ativos no nível da tarefa (usado na criação da tarefa e na edição dos detalhes da tarefa).
export interface AssetInterceptRuleInput {
  action: AssetInterceptAction;
  kind: AssetInterceptKind;
  pattern: string;
  note: string;
  enabled: boolean;
}

export interface AssetInterceptRule {
  id: number;
  enabled: boolean;
  action?: AssetInterceptAction; // regras globais não trazem este campo (sempre interceptam); regras no nível da tarefa distinguem block/allow
  kind: AssetInterceptKind;
  pattern: string;
  note: string;
  builtin: boolean;
  created_at: string;
  updated_at: string;
}

export interface InterceptPending {
  decision_source?: "rule" | "model" | "unknown" | "";
  id: number;
  rule_id?: number;
  conversation_id?: number;
  task_id?: string;
  agent_name: string;
  tool_name: string;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  tool_input: Record<string, any>;
  status: "pending" | "allowed" | "denied" | "timeout";
  reason: string; // message da regra ou motivo da decisão do modelo (a decisão do modelo leva o prefixo [模型])
  decided_at?: string;
  created_at: string;
}

// JudgeConfig: configuração global da aprovação de fallback por modelo (o modelo só decide quando nenhuma regra de interceptação corresponde).
export interface JudgeConfig {
  enabled: boolean;
  profile_id: number; // 0 = segue a configuração ativa/padrão
  prompt: string; // prompt de decisão; se não definido, o GET faz o backend preencher com o texto completo do template embutido
  timeout_seconds: number; // timeout da chamada ao modelo
  fail_action: "allow" | "ask" | "deny"; // fallback quando o modelo dá erro/timeout/saída não interpretável
  ask_timeout_seconds: number; // timeout de espera da aprovação depois que o modelo decide ask e encaminha para humano
  ask_timeout_action: "allow" | "deny"; // ação padrão após o timeout da aprovação
}

// JudgeUsage: uso acumulado de tokens da aprovação de fallback por modelo (canal judge) + série diária dos últimos N dias.
export interface JudgeDayUsage {
  date: string; // YYYY-MM-DD (UTC)
  calls: number;
  input_tokens: number;
  output_tokens: number;
}
export interface JudgeUsage {
  calls: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  daily: JudgeDayUsage[];
}

export interface InterceptApprovalFilter {
  status?: InterceptPending["status"];
  decision_source?: "rule" | "model" | "unknown";
}

// InterceptApprovalRow enriches InterceptPending with conversation/task and rule context.
export interface InterceptApprovalRow extends InterceptPending {
  conv_title: string; // "" if no linked conversation
  conv_agent_key: string; // "" if no linked conversation
  rule_name: string; // "" if rule was deleted
}

// ── Sincronização de ativos (fonte de dados ScopeSentry) ──────────────────────────────────────────────
export interface SSProject {
  id: string; // MongoDB ObjectID — used as filter.project
  name: string;
  logo?: string;
  AssetCount?: number;
  tag?: string;
}

export interface SSTask {
  id: string;
  name: string; // used as filter.task
  status?: number;
  progress?: number;
  creatTime?: string;
  endTime?: string;
}

// ConvTokenSummary — one conversation's token total (+ profile/date) for merging
// chat usage into the dashboard token stats. GET /api/tokens/conversations.
export interface ConvTokenSummary {
  llm_profile_id: number | null;
  created_at: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

// ---- Command recording (Bash execution history) ----
export interface CommandRecord {
  id: number;
  exploration_id: number;
  worker: string;
  tool: string;
  command: string; // raw tool input (JSON)
  output: string;
  is_error: boolean;
  created_at: string;
}

// Estatísticas de chamadas de uma única ferramenta (/commands/stats); errors é o número de falhas entre elas.
export interface ToolStat {
  tool: string;
  total: number;
  errors: number;
}

// ---- LLM recording ----
export interface LLMRecordItem {
  id: number;
  ts: string;
  model: string;
  profile_name: string;
  session_id: string;
  task_id: string;
  worker: string;
  latency_ms: number;
  input_tokens: number;
  output_tokens: number;
  cache_read: number;
  cache_write: number;
  status: string;
  error?: string;
}

export interface LLMRecordDetail extends LLMRecordItem {
  request_body: string;
  response_body: string;
  // HTTP bruto realmente trocado com o provider: a requisição é o body completo enviado por buildBody() (incluindo o schema das ferramentas),
  // a resposta são os frames SSE originais. Os request_body/response_body acima são a visão normalizada,
  // que descarta o schema das ferramentas e os blocos tool_use. Registros antigos ficam vazios.
  raw_request?: string;
  raw_response?: string;
}

// One distinct task with its LLM-record count (task picker on the records page).
export interface LLMTask {
  task_id: string;
  count: number;
}

// The exact JSON sent to the review model, retained for all model verdicts.
export interface InterceptReviewInput {
  version: number;
  background?: {
    // worker_summary is retained only for immutable v2/v3 snapshots.
    source: "user_message" | "worker_summary";
    text: string;
    truncated?: boolean;
  };
  // Version 1 snapshots are immutable and remain readable in historical audits.
  task?: {
    task_id: number;
    description: string;
    goal: string;
    constraints: { id: number; kind: string; text: string; origin: string; created_at: number }[];
    truncated?: boolean;
  };
  working_directory?: string;
  worker_intent?: string;
  turn_input?: string;
  background_truncated?: boolean;
  // Legacy v1/v2 snapshots only; v3 never sends execution history.
  history?: {
    tool_use_id: string;
    tool: string;
    arguments_preview: string;
    result: string;
    status: "succeeded" | "failed";
    truncated?: boolean;
  }[];
  history_truncated?: boolean;
  correlation?: "exact" | "ambiguous" | "unavailable";
  tool_name: string;
  arguments: Record<string, unknown>;
}

// Immutable review snapshot plus separately recorded execution outcome.
export interface InterceptAudit {
  model_input?: InterceptReviewInput;
  model_input_digest?: string;
  run_id?: string;
  tool_use_id?: string;
  correlation: "exact" | "ambiguous" | "unavailable";
  input_digest: string;
  user_message: string;
  user_truncated?: boolean;
  context:
    | { kind: string; tool?: string; tool_use_id?: string; text: string; is_error?: boolean; truncated?: boolean }[]
    | null;
  context_truncated?: boolean;
  captured_at: string;
  model_fallback?: boolean;
  initial_action: "allow" | "ask" | "deny";
  initial_reason: string;
  effective_action?: "allow" | "deny";
  decision_reason?: string;
  rule_name?: string;
  config_digest?: string;
  profile_id?: number;
  execution_status: "not_started" | "not_executed" | "awaiting_result" | "succeeded" | "failed" | "unknown";
  output?: string;
  output_truncated?: boolean;
  execution_ended_at?: string;
}
export interface InterceptDetail extends InterceptApprovalRow {
  audit: InterceptAudit | null;
}

export type TrafficEvidenceRole = "baseline" | "proof" | "verification" | "supporting";
export interface TrafficEvidenceRef {
  traffic_id: string;
  role?: TrafficEvidenceRole;
  note?: string;
}
export interface TrafficEvidenceSnapshot {
  id: string;
  source_traffic_id: string;
  captured_at: number;
  url: string;
  method: string;
  status: number;
  content_type: string;
  req_head?: string;
  resp_head?: string;
  req_hash: string;
  resp_hash: string;
  req_len: number;
  resp_len: number;
}
export interface FindingTrafficBinding {
  id: string;
  finding_id: string;
  snapshot_id: string;
  role: TrafficEvidenceRole;
  note: string;
  position: number;
  created_at: string;
  snapshot: TrafficEvidenceSnapshot;
}
export interface FindingTraffic {
  finding_id: string;
  version: number;
  report_version: number;
  bindings: FindingTrafficBinding[];
}
export interface EvidenceBodyPreview {
  content: string;
  offset: number;
  total: number;
  next_offset: number;
  truncated: boolean;
  binary: boolean;
}
export interface FindingTrafficDetail {
  binding: FindingTrafficBinding;
  request: EvidenceBodyPreview;
  response: EvidenceBodyPreview;
}

/** GET /api/update/check — resultado da comparação entre a versão atual e a última versão estável do GitHub. */
export interface UpdateCheck {
  /** Versão em execução; em builds de desenvolvimento é "dev" ou a forma com sufixo do git describe. */
  current: string;
  /** Forma de execução. No docker, a troca de binário só afeta a camada gravável do contêiner; recriar o contêiner volta à versão da imagem. */
  mode: "docker" | "binary";
  os: string;
  arch: string;
  repo: string;
  /** Se existe uma versão anterior para rollback (artex.old). */
  has_backup: boolean;
  /** Resultado do auto-update bootstrap desta inicialização (falha na troca / rollback já feito etc.); vazio quando nada aconteceu. */
  boot_notice?: string;
  rolled_back?: boolean;
  /** Motivo, quando a consulta ao GitHub falha; nesse caso os campos abaixo não existirão. */
  error?: string;
  latest?: string;
  notes?: string;
  html_url?: string;
  published_at?: string;
  /** Nome do pacote de release da plataforma atual e se o Release realmente o contém. */
  asset?: string;
  asset_available?: boolean;
  size?: number;
  has_update?: boolean;
  /** Se os números de versão dos dois lados são comparáveis; em builds de desenvolvimento é false, e o update com um clique fica desativado. */
  comparable?: boolean;
  /** Explicação quando comparable é false. */
  reason?: string;
}

/** Uma atualização de progresso enviada por /api/update/stream. */
export interface UpdateProgress {
  phase: "idle" | "downloading" | "verifying" | "extracting" | "staged" | "failed";
  /** Significativo apenas na fase de download (0-100); nas demais fases é -1. */
  percent: number;
  message: string;
  version?: string;
  error?: string;
}

// Original execution selected from an approval, never submitted to the reviewer.
export interface InterceptExecution {
  conversation_id: number | null;
  task_id: string | null;
  session: string;
  seq: number;
  items: Activity[];
}
