package db

import "testing"

// O prefixo gravado mudou de "[模型]" para "[Modelo]" na tradução para pt-BR.
// Bancos em uso guardam os dois, então a leitura precisa aceitar ambos: uma
// decisão antiga do juiz não pode voltar classificada como "rule"/"unknown" nem
// exibir o prefixo cru na tela.
func TestModelReasonAcceptsBothPrefixes(t *testing.T) {
	for _, tc := range []struct {
		reason string
		model  bool
		strip  string
	}{
		{"[Modelo] Operação: ler arquivo", true, "Operação: ler arquivo"},
		{"[模型] 实际操作：读取文件", true, "实际操作：读取文件"},
		{"[Modelo]sem espaço", true, "sem espaço"},
		{ModelReasonPrefix, true, ""},
		{"Comando destrutivo recusado", false, "Comando destrutivo recusado"},
		{"", false, ""},
		// O prefixo só vale no início: no meio do texto é conteúdo comum.
		{"a regra citou [Modelo] no texto", false, "a regra citou [Modelo] no texto"},
	} {
		if got := IsModelReason(tc.reason); got != tc.model {
			t.Errorf("IsModelReason(%q) = %v, esperado %v", tc.reason, got, tc.model)
		}
		if got := StripModelReasonPrefix(tc.reason); got != tc.strip {
			t.Errorf("StripModelReasonPrefix(%q) = %q, esperado %q", tc.reason, got, tc.strip)
		}
	}
}
