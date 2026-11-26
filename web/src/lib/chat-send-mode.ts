"use client";

import * as React from "react";

import { getLocalStorageValue, setLocalStorageValue } from "@/lib/local-storage.client";

// Atalhos de envio/quebra de linha da caixa de entrada da conversa. Preferência puramente de frontend: só vai para o localStorage, não é salva no banco nem sincronizada com a conta,
// então é preciso reconfigurar ao trocar de navegador. Ver issue #39: a versão 0.3.2 trocou o envio por Ctrl+Enter pelo envio por Enter,
// e aqui o atalho antigo volta como opção.
export type ChatSendMode = "enter" | "ctrl-enter";

export const CHAT_SEND_MODE_KEY = "artex_chat_send_mode";
export const DEFAULT_CHAT_SEND_MODE: ChatSendMode = "enter";

export const CHAT_SEND_MODE_OPTIONS: { value: ChatSendMode; label: string }[] = [
  { value: "enter", label: "Enter envia, Shift+Enter quebra a linha" },
  { value: "ctrl-enter", label: "Ctrl+Enter envia, Enter quebra a linha" },
];

function parseMode(raw: string | null): ChatSendMode {
  return raw === "ctrl-enter" || raw === "enter" ? raw : DEFAULT_CHAT_SEND_MODE;
}

// Conjunto de assinantes dentro da mesma aba. O evento storage do localStorage só dispara nas "outras" abas,
// então, depois de alterar nas configurações, esta página precisa notificar via emit as caixas de entrada da mesma página; do contrário só teria efeito após recarregar.
const listeners = new Set<() => void>();

function subscribe(listener: () => void) {
  listeners.add(listener);
  window.addEventListener("storage", listener);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("storage", listener);
  };
}

// O retorno é um literal de string, e Object.is compara por valor, então não faz o useSyncExternalStore entrar em loop.
function getSnapshot(): ChatSendMode {
  return parseMode(getLocalStorageValue(CHAT_SEND_MODE_KEY));
}

// O servidor não tem localStorage, então renderiza o valor padrão primeiro e, após o hydrate, getSnapshot corrige.
function getServerSnapshot(): ChatSendMode {
  return DEFAULT_CHAT_SEND_MODE;
}

export function useChatSendMode(): ChatSendMode {
  return React.useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}

export function setChatSendMode(mode: ChatSendMode) {
  setLocalStorageValue(CHAT_SEND_MODE_KEY, mode);
  for (const listener of listeners) listener();
}

// shouldSubmitOnKey decide se um pressionamento de tecla deve enviar.
// isComposing / keyCode 229 indicam que um IME (como o de chinês) está selecionando caracteres e deve ser deixado passar; do contrário, o Enter ao escolher a palavra enviaria por engano.
// O modo enter exclui apenas o Shift, mantendo exatamente o comportamento da 0.3.2: quem não altera a configuração não sente diferença.
// O modo ctrl-enter aceita Ctrl e Cmd (macOS).
export function shouldSubmitOnKey(e: React.KeyboardEvent, mode: ChatSendMode): boolean {
  if (e.key !== "Enter") return false;
  if (e.nativeEvent.isComposing || e.nativeEvent.keyCode === 229) return false;
  if (mode === "ctrl-enter") return e.ctrlKey || e.metaKey;
  return !e.shiftKey;
}
