import { type ClassValue, clsx } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

// Auxiliar para decidir o fechamento via onInteractOutside em gavetas/diálogos (Sheet/Dialog).
//
// Contexto: as camadas Radix dentro da gaveta (dropdown do Select, DropdownMenu, Popover etc.) fazem portal para fora
// da gaveta. Com a camada aberta, ao clicar no overlay/fora da gaveta para recolhê-la, esse pointerdown é tratado ao mesmo tempo pelos dois
// DismissableLayer, o do Select e o do Sheet; o Select fecha primeiro e, por ser um evento discrete, o React faz flush de forma síncrona, de modo que
// quando chega a vez do handler do Sheet, o data-state da camada já virou closed — detectar "no momento" se a camada está aberta
// é naturalmente pouco confiável (verificado na prática).
//
// Abordagem correta: o listener de pointerdown do Radix roda na fase de bubbling; nós, na fase de capture (antes dele), primeiro registramos
// "se há alguma camada aberta neste momento", e o onInteractOutside então lê esse valor registrado para decidir se libera o fechamento.
function isRadixOverlayOpenNow(): boolean {
  if (typeof document === "undefined") return false;
  return !!document.querySelector(
    [
      "[data-slot='select-trigger'][data-state='open']",
      "[data-slot='select-content'][data-state='open']",
      "[role='listbox'][data-state='open']",
      "[data-radix-popper-content-wrapper]",
      "[aria-expanded='true'][data-state='open']",
    ].join(","),
  );
}

let overlayOpenAtLastPointerDown = false;
if (typeof document !== "undefined") {
  document.addEventListener(
    "pointerdown",
    () => {
      overlayOpenAtLastPointerDown = isRadixOverlayOpenNow();
    },
    true, // capture: registra antes do handler de pointerdown da fase de bubbling do Radix
  );
}

// radixOverlayWasOpenAtPointerDown retorna "se havia camada Radix aberta no pointerdown mais recente".
// Gavetas/diálogos usam isso: com a camada aberta, clicar no overlay → só recolhe a camada, sem fechar a si mesmo.
export function radixOverlayWasOpenAtPointerDown(): boolean {
  return overlayOpenAtLastPointerDown;
}

// copyText grava o texto na área de transferência e retorna se teve sucesso.
// Contexto: navigator.clipboard só está disponível em contexto seguro (HTTPS / localhost); com acesso via IP + HTTP
// ele é undefined, e nesse caso faz fallback para execCommand("copy").
export async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard && window.isSecureContext) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // segue para o fallback
    }
  }
  try {
    const textarea = document.createElement("textarea");
    textarea.value = text;
    textarea.style.position = "fixed";
    textarea.style.left = "-9999px";
    textarea.style.top = "0";
    document.body.appendChild(textarea);
    textarea.focus();
    textarea.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(textarea);
    return ok;
  } catch {
    return false;
  }
}

export const getInitials = (str: string): string => {
  if (typeof str !== "string" || !str.trim()) return "?";

  return (
    str
      .trim()
      .split(/\s+/)
      .filter(Boolean)
      .map((word) => word[0])
      .join("")
      .toUpperCase() || "?"
  );
};

export function formatCurrency(
  amount: number,
  opts?: {
    currency?: string;
    locale?: string;
    minimumFractionDigits?: number;
    maximumFractionDigits?: number;
    noDecimals?: boolean;
  },
) {
  const { currency = "USD", locale = "en-US", minimumFractionDigits, maximumFractionDigits, noDecimals } = opts ?? {};

  const formatOptions: Intl.NumberFormatOptions = {
    style: "currency",
    currency,
    minimumFractionDigits: noDecimals ? 0 : minimumFractionDigits,
    maximumFractionDigits: noDecimals ? 0 : maximumFractionDigits,
  };

  return new Intl.NumberFormat(locale, formatOptions).format(amount);
}
