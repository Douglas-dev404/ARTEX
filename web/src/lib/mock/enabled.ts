// Chave do mock. Variável pública injetada em tempo de build (só o prefixo NEXT_PUBLIC_ é legível no navegador).
// Na Vercel, definir NEXT_PUBLIC_MOCK=1 faz o site inteiro usar mock, sem precisar de backend.
export const MOCK = process.env.NEXT_PUBLIC_MOCK === "1";
