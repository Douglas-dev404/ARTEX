// Espelha db/model_reason.go. O motivo de uma interceptação decidida pelo juiz
// LLM é gravado com este prefixo; registros anteriores à tradução para pt-BR
// têm o prefixo antigo, então toda leitura aceita os dois.
const MODEL_REASON_PREFIX = "[Modelo]";
const LEGACY_MODEL_REASON_PREFIX = "[模型]";

/** Informa se o motivo foi escrito pelo juiz LLM, e não por uma regra. */
export function isModelReason(reason: string | null | undefined): boolean {
  if (!reason) return false;
  return reason.startsWith(MODEL_REASON_PREFIX) || reason.startsWith(LEGACY_MODEL_REASON_PREFIX);
}

/** Devolve o motivo sem o prefixo do modelo; um motivo de regra volta inalterado. */
export function stripModelPrefix(reason: string | null | undefined): string {
  if (!reason) return "";
  for (const prefix of [MODEL_REASON_PREFIX, LEGACY_MODEL_REASON_PREFIX]) {
    if (reason.startsWith(prefix)) return reason.slice(prefix.length).trim();
  }
  return reason;
}
