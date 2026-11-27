// Centralised status → color/label semantics, reused across the whole app.
// Spec §8.3: intenção / cobertura / tarefa / severidade each have a consistent color set.

export type Tone = "neutral" | "blue" | "green" | "amber" | "red" | "rose" | "violet" | "slate";

// As cores vêm dos tokens do tema (globals.css), não de paletas soltas do
// Tailwind: assim a mesma severidade tem exatamente a mesma cor no selo, na
// barra lateral da linha e no gráfico, e muda junto em qualquer preset de tema.
export const toneClasses: Record<Tone, string> = {
  neutral: "bg-muted text-muted-foreground border-transparent",
  blue: "bg-sev-low-bg text-sev-low border-sev-low/25",
  green: "bg-state-done/15 text-state-done border-state-done/25",
  amber: "bg-sev-medium-bg text-sev-medium border-sev-medium/25",
  red: "bg-sev-high-bg text-sev-high border-sev-high/30",
  // rose = "crítico": preenchimento sólido de alta ênfase, um degrau visual
  // acima do contorno suave de "alto", para saltar à vista numa lista longa.
  rose: "bg-sev-critical text-sev-critical-on border-sev-critical font-semibold",
  violet: "bg-chart-5/15 text-chart-5 border-chart-5/25",
  slate: "bg-sev-info-bg text-sev-info border-sev-info/25",
};

export const toneDot: Record<Tone, string> = {
  neutral: "bg-muted-foreground",
  blue: "bg-sev-low",
  green: "bg-state-done",
  amber: "bg-sev-medium",
  red: "bg-sev-high",
  rose: "bg-sev-critical-on",
  violet: "bg-chart-5",
  slate: "bg-sev-info",
};

// Cor sólida do tom, para a barra lateral de uma linha, um traço de gráfico ou
// qualquer superfície que precise do matiz cheio em vez do selo.
export const toneSolid: Record<Tone, string> = {
  neutral: "bg-border",
  blue: "bg-sev-low",
  green: "bg-state-done",
  amber: "bg-sev-medium",
  red: "bg-sev-high",
  rose: "bg-sev-critical",
  violet: "bg-chart-5",
  slate: "bg-sev-info",
};

/** Ordem de gravidade, da mais grave para a menos. Útil para ordenar listas. */
export const severityOrder = ["critical", "high", "medium", "low"] as const;

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

// Escala de severidade: quatro degraus visuais distintos, do sólido (crítico)
// ao azul calmo (baixo). "Baixo" usa blue, e não slate, para não se confundir
// com os estados neutros de "ignorado"/"descartado" — é um achado real.
const severity: Record<string, StatusMeta> = {
  critical: { label: "Crítico", tone: "rose" },
  high: { label: "Alto", tone: "red" },
  medium: { label: "Médio", tone: "amber" },
  low: { label: "Baixo", tone: "blue" },
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
