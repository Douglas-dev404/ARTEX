import { type StatusDomain, statusMeta, type Tone, toneClasses, toneDot, toneSolid } from "@/lib/status";
import { cn } from "@/lib/utils";

export function StatusBadge({
  domain,
  value,
  dot = false,
  className,
}: {
  domain: StatusDomain;
  value: string;
  dot?: boolean;
  className?: string;
}) {
  const meta = statusMeta(domain, value);
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-xs font-medium whitespace-nowrap",
        toneClasses[meta.tone],
        className,
      )}
    >
      {dot && <span className={cn("size-1.5 rounded-full", toneDot[meta.tone])} />}
      {meta.label}
    </span>
  );
}

export function ToneDot({ tone }: { tone: Tone }) {
  return <span className={cn("size-2 rounded-full", toneDot[tone])} />;
}

/**
 * Faixa vertical de severidade para a borda de uma linha de lista ou cartão.
 * Ler a gravidade pela cor da barra é mais rápido do que pelo selo quando a
 * lista é longa, e a barra sobrevive a telas estreitas, onde o selo é o
 * primeiro elemento a ser escondido.
 *
 * Decorativa: o selo ao lado já carrega o rótulo, então fica oculta para
 * leitores de tela em vez de repetir a mesma informação.
 */
export function SeverityBar({ value, className }: { value: string; className?: string }) {
  const meta = statusMeta("severity", value);
  return <span aria-hidden className={cn("w-1 shrink-0 self-stretch rounded-full", toneSolid[meta.tone], className)} />;
}
