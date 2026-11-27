package db

import "strings"

// ModelReasonPrefix marca um motivo de interceptação que veio do juiz LLM, e não
// de uma regra. É gravado junto com o motivo em intercept_pending.reason e lido
// de volta para classificar a decisão como "model".
const ModelReasonPrefix = "[Modelo]"

// legacyModelReasonPrefix é o prefixo gravado antes da tradução para pt-BR.
// Registros antigos continuam no banco, então toda leitura aceita os dois.
const legacyModelReasonPrefix = "[模型]"

// IsModelReason informa se o motivo foi escrito pelo juiz LLM.
func IsModelReason(reason string) bool {
	return strings.HasPrefix(reason, ModelReasonPrefix) || strings.HasPrefix(reason, legacyModelReasonPrefix)
}

// StripModelReasonPrefix devolve o motivo sem o prefixo do modelo e sem o espaço
// que o segue. Um motivo que não veio do modelo volta inalterado.
func StripModelReasonPrefix(reason string) string {
	for _, prefix := range [...]string{ModelReasonPrefix, legacyModelReasonPrefix} {
		if rest, ok := strings.CutPrefix(reason, prefix); ok {
			return strings.TrimSpace(rest)
		}
	}
	return reason
}
