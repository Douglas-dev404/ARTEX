// Cor de código de status HTTP, a partir dos tokens de tema.
//
// Estava espalhada em cada tela como `text-red-500` / `text-emerald-500` fixos
// do Tailwind, o que trazia dois problemas: a mesma faixa de status ganhava
// matizes diferentes em telas diferentes, e as cores fixas não acompanhavam o
// tema (ficavam escuras demais no modo escuro, onde o produto vive).
//
// O mapeamento reaproveita a escala de severidade de propósito: 5xx é falha do
// alvo, e deve pesar visualmente como "alto"; 4xx é desvio esperado durante um
// teste e pesa como "médio".

import { type Tone, toneDot } from "@/lib/status";

/** Tom semântico da faixa de status. Útil quando se quer o selo inteiro. */
export function httpStatusTone(status: number): Tone {
  if (status >= 500) return "red";
  if (status >= 400) return "amber";
  if (status >= 300) return "blue";
  if (status >= 200) return "green";
  // 1xx e qualquer coisa fora da faixa (0 = sem resposta, erro de transporte).
  return "neutral";
}

/** Classe de cor de texto para o número do status. */
export function httpStatusTextClass(status: number): string {
  switch (httpStatusTone(status)) {
    case "red":
      return "text-sev-high";
    case "amber":
      return "text-sev-medium";
    case "blue":
      return "text-state-running";
    case "green":
      return "text-state-done";
    default:
      return "text-muted-foreground";
  }
}

/** Classe de fundo sólido, para um ponto ou barra indicando a faixa. */
export function httpStatusDotClass(status: number): string {
  return toneDot[httpStatusTone(status)];
}

/** Alias de `httpStatusDotClass`, para quando o uso é uma barra e não um ponto. */
export const httpStatusBgClass = httpStatusDotClass;
