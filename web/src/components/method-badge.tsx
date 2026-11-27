// Selo de método HTTP.
//
// A tabela de cores estava duplicada em `function/assets/page.tsx` e na aba de
// ativos do detalhe da tarefa, com os mesmos tons -100/-700: fundo quase
// branco com texto escuro, que no tema escuro — o padrão do produto — virava
// um bloco claro berrante.
//
// A paleta é **categórica**, não escala de severidade: a cor serve para
// distinguir os métodos entre si, então não vem dos tokens --sev-*. O que
// mudou foi a técnica: cor com alfa sobre o fundo da superfície, que se
// adapta aos dois temas, mais um par de tons de texto por tema.
//
// DELETE puxa o vermelho de propósito: é o método destrutivo, e num console de
// pentest convém que ele salte à vista numa lista longa de requisições.

import { cn } from "@/lib/utils";

const METHOD_COLOR: Record<string, string> = {
  GET: "bg-emerald-500/15 text-emerald-700 dark:text-emerald-300",
  POST: "bg-blue-500/15 text-blue-700 dark:text-blue-300",
  PUT: "bg-amber-500/15 text-amber-700 dark:text-amber-300",
  PATCH: "bg-orange-500/15 text-orange-700 dark:text-orange-300",
  DELETE: "bg-red-500/15 text-red-700 dark:text-red-300",
  HEAD: "bg-purple-500/15 text-purple-700 dark:text-purple-300",
  OPTIONS: "bg-muted text-muted-foreground",
};

export function MethodBadge({ method, className }: { method: string; className?: string }) {
  const m = (method ?? "").toUpperCase();
  return (
    <span
      className={cn(
        "inline-block rounded px-1.5 py-0.5 font-mono text-[10px] font-semibold leading-none",
        METHOD_COLOR[m] ?? "bg-muted text-muted-foreground",
        className,
      )}
    >
      {m || "—"}
    </span>
  );
}
