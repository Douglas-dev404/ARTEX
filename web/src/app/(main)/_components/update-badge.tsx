"use client";

import * as React from "react";

import Link from "next/link";

import { ArrowUpCircleIcon } from "lucide-react";

import { api } from "@/lib/api";

/**
  * Aviso de "nova versão disponível" na barra superior: consulta uma vez ao carregar a página inteira e, havendo atualização, o acende ao lado do número da versão;
  * ao clicar, vai direto ao card "Versão e atualizações" da página de configuração do sistema.
 *
  * O resultado da consulta do backend ao GitHub tem cache de 30 minutos, então consultar a cada montagem aqui é seguro
  * — a API do GitHub sem autenticação permite só 60 requisições/hora/IP; sem essa camada de cache, abrir vários separadores
  * esgotaria a cota e, depois, quando se quisesse mesmo atualizar, a consulta não funcionaria.
 *
  * Qualquer falha na consulta é silenciosa: a barra superior não é lugar para erro; o usuário que abrir a página de configuração e clicar em "Verificar atualizações" verá o motivo.
 */
export function UpdateBadge() {
  const [latest, setLatest] = React.useState("");

  React.useEffect(() => {
    let alive = true;
    api
      .checkUpdate()
      .then((r) => {
        // has_update já inclui a checagem de "versão comparável"; builds de desenvolvimento não acendem este aviso.
        if (alive && r.has_update && r.latest) setLatest(r.latest.replace(/^v(?=\d)/, ""));
      })
      .catch(() => {
        // Silencioso: sem rede / limite de taxa do GitHub não devem exibir erro na barra superior.
      });
    return () => {
      alive = false;
    };
  }, []);

  if (!latest) return null;

  return (
    <Link
      href="/system/settings"
      title={`Nova versão ${latest} encontrada, clique para ir atualizar`}
      className="inline-flex items-center gap-1.5 rounded-full bg-primary px-2.5 py-1 font-medium text-primary-foreground text-xs transition-opacity hover:opacity-90"
    >
      {/* Ponto pulsante: há muitos elementos na barra superior e texto puro passa despercebido; a animação o torna visível de imediato. */}
      <span className="relative flex size-1.5">
        <span className="absolute inline-flex size-full animate-ping rounded-full bg-primary-foreground opacity-75" />
        <span className="relative inline-flex size-1.5 rounded-full bg-primary-foreground" />
      </span>
      <ArrowUpCircleIcon className="size-3.5" />
      <span className="hidden sm:inline">Nova versão {latest}</span>
      <span className="sm:hidden">Nova versão</span>
    </Link>
  );
}
