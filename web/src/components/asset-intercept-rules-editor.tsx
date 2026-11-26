"use client";

import * as React from "react";

import { PlusIcon, Trash2Icon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import type { AssetInterceptKind, AssetInterceptRuleInput } from "@/lib/types";

// Usa NativeSelect (<select> nativo) em vez do Select do shadcn: este editor é usado dentro de um Sheet (gaveta),
// e o dropdown do shadcn Select faz portal para o body, e clicar fora dispara o "fechar ao clicar fora" da gaveta, fechando-a por engano; o dropdown nativo não tem esse problema.
export const ASSET_INTERCEPT_KIND_OPTIONS: {
  value: AssetInterceptKind;
  label: string;
  placeholder: string;
}[] = [
  { value: "exact_domain", label: "Domínio (exato)", placeholder: "example.gov.cn" },
  { value: "exact_ip", label: "IP (exato)", placeholder: "203.0.113.10" },
  { value: "exact_url", label: "URL (exata)", placeholder: "https://example.com/login" },
  { value: "fuzzy_domain", label: "Domínio (parcial)", placeholder: ".gov.cn" },
  { value: "fuzzy_ip", label: "IP (parcial)", placeholder: "203.0.113." },
  { value: "fuzzy_url", label: "URL (parcial)", placeholder: "/admin" },
  { value: "cidr", label: "Faixa CIDR", placeholder: "192.168.0.0/16" },
];

// AssetInterceptRulesEditor é a área de edição controlada, com várias linhas, das "regras de interceptação/permissão" (interceptar block/permitir allow +
// tipo + conteúdo de correspondência + observação), sem persistência própria: o componente pai decide quando enviar.
export function AssetInterceptRulesEditor({
  value,
  onChange,
}: {
  value: AssetInterceptRuleInput[];
  onChange: (v: AssetInterceptRuleInput[]) => void;
}) {
  function update(i: number, patch: Partial<AssetInterceptRuleInput>) {
    onChange(value.map((r, idx) => (idx === i ? { ...r, ...patch } : r)));
  }
  function remove(i: number) {
    onChange(value.filter((_, idx) => idx !== i));
  }
  function add() {
    onChange([...value, { action: "block", kind: "fuzzy_domain", pattern: "", note: "", enabled: true }]);
  }
  return (
    <div className="grid gap-2">
      {value.map((r, i) => {
        const ph = ASSET_INTERCEPT_KIND_OPTIONS.find((o) => o.value === r.kind)?.placeholder ?? "";
        return (
          // biome-ignore lint/suspicious/noArrayIndexKey: as linhas não têm id estável, basta controlar por índice
          <div key={i} className="flex items-center gap-2">
            <NativeSelect
              size="sm"
              className="w-[84px] shrink-0"
              value={r.action}
              onChange={(e) => update(i, { action: e.target.value as "block" | "allow" })}
            >
              <NativeSelectOption value="block">Interceptar</NativeSelectOption>
              <NativeSelectOption value="allow">Permitir</NativeSelectOption>
            </NativeSelect>
            <NativeSelect
              size="sm"
              className="w-[120px] shrink-0"
              value={r.kind}
              onChange={(e) => update(i, { kind: e.target.value as AssetInterceptKind })}
            >
              {ASSET_INTERCEPT_KIND_OPTIONS.map((o) => (
                <NativeSelectOption key={o.value} value={o.value}>
                  {o.label}
                </NativeSelectOption>
              ))}
            </NativeSelect>
            <Input
              className="flex-1"
              placeholder={ph}
              value={r.pattern}
              onChange={(e) => update(i, { pattern: e.target.value })}
            />
            <Input
              className="w-[120px] shrink-0"
              placeholder="Observação (opcional)"
              value={r.note}
              onChange={(e) => update(i, { note: e.target.value })}
            />
            <Button
              type="button"
              size="icon"
              variant="ghost"
              className="text-destructive hover:text-destructive size-8 shrink-0"
              onClick={() => remove(i)}
            >
              <Trash2Icon className="size-4" />
            </Button>
          </div>
        );
      })}
      <Button type="button" size="sm" variant="outline" className="w-fit" onClick={add}>
        <PlusIcon className="size-4" /> Adicionar uma regra
      </Button>
    </div>
  );
}
