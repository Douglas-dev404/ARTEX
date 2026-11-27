// Texto legível do payload de uma intenção.
//
// O payload chega como JSON (`{"summary": "...", ...}`) e precisa virar uma
// linha de texto em várias telas. Onde essa conversão faltava, a interface
// mostrava a string JSON crua ao usuário — era o caso dos cartões "Intenções
// em andamento" e "Intenções bloqueadas/com erro" na visão geral da tarefa.
//
// A regra segue o que a aba de sessões já fazia: usar `summary` quando houver,
// cair para o texto cru quando o payload não for JSON (nem todo produtor
// escreve JSON), e só então para um rótulo com o id.

/**
 * Resumo legível de um payload de intenção.
 *
 * @param payload  conteúdo bruto do campo `payload` do nó.
 * @param fallback texto usado quando não há payload aproveitável.
 */
export function intentSummary(payload: string | null | undefined, fallback = "Intenção sem descrição"): string {
  const raw = (payload ?? "").trim();
  if (!raw) return fallback;

  try {
    const parsed = JSON.parse(raw);
    // `summary` é o campo que o planejador preenche com a frase legível.
    if (parsed && typeof parsed === "object" && "summary" in parsed) {
      const summary = String((parsed as { summary: unknown }).summary).trim();
      if (summary) return summary;
    }
    // JSON válido sem summary: mostrar o objeto cru não ajuda ninguém.
    return fallback;
  } catch {
    // Payload que não é JSON é texto simples, e já serve como resumo.
    return raw;
  }
}
