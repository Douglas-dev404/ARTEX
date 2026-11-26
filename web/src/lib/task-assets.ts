import type { NewAssetType } from "@/lib/types";

const ASSET_TYPE_LABELS: Record<NewAssetType, string> = {
  app: "Aplicativo",
  endpoint: "Endpoint",
  ip: "IP",
  root_domain: "Domínio raiz",
  service: "Serviço",
  subdomain: "Subdomínio",
};

const TASK_ASSET_SOURCE_LABELS: Record<string, string> = {
  agent: "Descoberto pelo Agent",
  anchor: "Âncora do quadro (blackboard)",
  api: "API de ativos",
  company: "Associação à empresa",
  legacy: "Associação histórica",
  manual: "Adicionado manualmente",
  system: "Associação do sistema",
  task: "Inicialização da tarefa",
};

export function taskAssetTypeLabel(type: NewAssetType): string {
  return ASSET_TYPE_LABELS[type];
}

export function taskAssetSourceLabel(source: string): string {
  return TASK_ASSET_SOURCE_LABELS[source] ?? source;
}
