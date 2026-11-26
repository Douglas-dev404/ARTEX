// Centralised status → color/label semantics, reused across the whole app.
// Spec §8.3: intenção / cobertura / tarefa / severidade each have a consistent color set.

export type Tone = "neutral" | "blue" | "green" | "amber" | "red" | "rose" | "violet" | "slate";

export const toneClasses: Record<Tone, string> = {
  neutral: "bg-muted text-muted-foreground border-transparent",
  blue: "bg-blue-500/15 text-blue-600 dark:text-blue-400 border-blue-500/20",
  green: "bg-emerald-500/15 text-emerald-600 dark:text-emerald-400 border-emerald-500/20",
  amber: "bg-amber-500/15 text-amber-600 dark:text-amber-400 border-amber-500/20",
  red: "bg-red-500/15 text-red-600 dark:text-red-400 border-red-500/20",
  // rose é usado para "crítico": preenchimento sólido de alta ênfase, visualmente bem acima do contorno vermelho suave de "alto".
  rose: "bg-rose-600 text-white border-rose-600 dark:bg-rose-600 dark:text-white",
  violet: "bg-violet-500/15 text-violet-600 dark:text-violet-400 border-violet-500/20",
  slate: "bg-slate-500/15 text-slate-600 dark:text-slate-400 border-slate-500/20",
};

export const toneDot: Record<Tone, string> = {
  neutral: "bg-muted-foreground",
  blue: "bg-blue-500",
  green: "bg-emerald-500",
  amber: "bg-amber-500",
  red: "bg-red-500",
  rose: "bg-white",
  violet: "bg-violet-500",
  slate: "bg-slate-500",
};

interface StatusMeta {
  label: string;
  tone: Tone;
}

const intent: Record<string, StatusMeta> = {
  open: { label: "Pendente de atribuição", tone: "slate" },
  running: { label: "Em execução", tone: "blue" },
  paused: { label: "Pausado", tone: "amber" },
  done: { label: "Concluído", tone: "green" },
  // blocked = tentativas esgotadas por falha de modelo/API/rede; esta intenção praticamente não chegou a ser explorada (não é bloqueio por interceptação do alvo).
  blocked: { label: "Erro na execução", tone: "red" },
  // exhausted = interrompida no meio ao atingir o orçamento de passos/tempo, gravando apenas resultados parciais (não significa que a direção foi totalmente explorada).
  exhausted: { label: "Orçamento esgotado", tone: "violet" },
  // stopped = estado histórico de exclusão lógica (mantido, dado histórico).
  stopped: { label: "Parado", tone: "slate" },
  // deleted = o usuário fez exclusão lógica desta intenção (nó e linhagem são mantidos; o motivo da exclusão está no campo delete_reason).
  deleted: { label: "Excluído", tone: "slate" },
};

const task: Record<string, StatusMeta> = {
  created: { label: "Criado", tone: "slate" },
  queued: { label: "Na fila", tone: "amber" },
  running: { label: "Em execução", tone: "blue" },
  paused: { label: "Pausado", tone: "amber" },
  done: { label: "Concluído", tone: "green" },
  failed: { label: "Falhou", tone: "red" },
  timeout: { label: "Expirado", tone: "amber" },
};

const severity: Record<string, StatusMeta> = {
  critical: { label: "Crítico", tone: "rose" },
  high: { label: "Alto", tone: "red" },
  medium: { label: "Médio", tone: "amber" },
  low: { label: "Baixo", tone: "slate" },
};

const finding: Record<string, StatusMeta> = {
  pending: { label: "Pendente", tone: "amber" },
  in_progress: { label: "Em tratamento", tone: "blue" },
  confirmed: { label: "Confirmado", tone: "red" },
  resolved: { label: "Tratado", tone: "green" },
  fixed: { label: "Corrigido", tone: "green" },
  false_positive: { label: "Falso positivo", tone: "slate" },
  ignored: { label: "Ignorado", tone: "neutral" },
  duplicate: { label: "Duplicado", tone: "neutral" },
  risk_accepted: { label: "Risco aceito", tone: "violet" },
};

const engine: Record<string, StatusMeta> = {
  exploring: { label: "Explorando", tone: "blue" },
  paused: { label: "Pausado", tone: "amber" },
  stalled: { label: "Estagnado", tone: "red" },
  idle: { label: "Ocioso", tone: "neutral" },
};

const goal: Record<string, StatusMeta> = {
  open: { label: "Em andamento", tone: "blue" },
  met: { label: "Atingido", tone: "green" },
  abandoned: { label: "Abandonado", tone: "slate" },
};

const audit: Record<string, StatusMeta> = {
  allow: { label: "Permitir", tone: "green" },
  block: { label: "Interceptar", tone: "red" },
};

const node: Record<string, StatusMeta> = {
  observed: { label: "Observado", tone: "slate" },
  confirmed: { label: "Confirmado", tone: "green" },
  tombstoned: { label: "Descartado", tone: "neutral" },
};

// Status de entrega de push. sending usa blue e não amber: não é um "problema",
// e sim "já foi retirado da fila e está sendo enviado", e precisa ser distinguível da semântica de espera de pending.
const delivery: Record<string, StatusMeta> = {
  pending: { label: "Aguardando envio", tone: "amber" },
  sending: { label: "Enviando", tone: "blue" },
  sent: { label: "Entregue", tone: "green" },
  failed: { label: "Falhou", tone: "red" },
  skipped: { label: "Ignorado", tone: "neutral" },
};

const maps = {
  intent,
  task,
  severity,
  finding,
  engine,
  goal,
  audit,
  node,
  delivery,
} as const;

export type StatusDomain = keyof typeof maps;

export function statusMeta(domain: StatusDomain, key: string): StatusMeta {
  return maps[domain][key] ?? { label: key, tone: "neutral" };
}
