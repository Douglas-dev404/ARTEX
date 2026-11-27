// Cabeçalho padrão de tela. Antes desta peça, as 24 páginas repetiam a mesma
// estrutura à mão e já tinham divergido: o título aparecia como text-xl,
// text-lg e text-sm, às vezes com tracking-tight, às vezes sem. Centralizar
// aqui é o que garante que o produto pareça um só, e não uma coleção de telas.

import type * as React from "react";

import { cn } from "@/lib/utils";

interface PageHeaderProps {
  /** Título da tela. Fica em text-xl: é o primeiro nível de hierarquia. */
  title: React.ReactNode;
  /** Linha de contexto sob o título — o que esta tela mostra, em uma frase. */
  description?: React.ReactNode;
  /** Ações e indicadores alinhados à direita (botões, selos, contadores). */
  actions?: React.ReactNode;
  /** Conteúdo colado sob o cabeçalho, dentro do mesmo bloco (abas, filtros). */
  children?: React.ReactNode;
  className?: string;
}

export function PageHeader({ title, description, actions, children, className }: PageHeaderProps) {
  return (
    <div className={cn("flex flex-col gap-4", className)}>
      {/* flex-wrap, e não um flex rígido: em telas estreitas as ações descem
          para a linha seguinte em vez de comprimirem o título até truncar. */}
      <div className="flex flex-wrap items-start justify-between gap-x-4 gap-y-2">
        <div className="min-w-0">
          <h1 className="truncate text-xl font-semibold tracking-tight">{title}</h1>
          {description && <p className="mt-0.5 text-sm text-muted-foreground">{description}</p>}
        </div>
        {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
      </div>
      {children}
    </div>
  );
}
