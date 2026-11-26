"use client";

import { CheckIcon } from "lucide-react";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import type { NotificationFilter } from "@/lib/types";

// asText / inputType são auxiliares de extração de valor internos deste arquivo (muito ligados à renderização dos controles), por isso não ficam em channel-fields.
import { type FieldDef, type FieldKind, SEVERITY_OPTIONS } from "./channel-fields";

// asText renderiza qualquer valor de configuração como string utilizável em um campo de entrada.
// config vem de JSON e o valor pode ser string / number / boolean / array / null;
// aqui só importa "dá para colocar numa caixa de texto?"; a serialização em si fica a cargo do buildConfig.
function asText(v: unknown): string {
  if (typeof v === "string") return v;
  if (v === null || v === undefined) return "";
  return String(v);
}

// inputType mapeia o tipo do campo para o atributo type do input.
function inputType(kind: FieldKind): "text" | "password" | "number" {
  if (kind === "password") return "password";
  if (kind === "number") return "number";
  return "text";
}

// ConfigField renderiza o controle correspondente conforme a definição do campo.
//
// O tratamento dos campos mascarados é o único cuidado especial aqui: a caixa de entrada **não exibe** o próprio valor mascarado, só mostra uma linha
// de aviso "Salvo". Assim a interface tem uma única regra: se há texto na caixa, foi o usuário que digitou;
// caixa vazia é valor vazio. Se "__masked__:…abc123" fosse colocado na caixa, o usuário acharia que é um texto provisório que
// precisa apagar, e acabaria limpando a credencial por engano com mais facilidade.
export function ConfigField({
  def,
  value,
  isSecret,
  onChange,
}: {
  def: FieldDef;
  value: unknown;
  isSecret: boolean;
  onChange: (v: unknown) => void;
}) {
  const id = `n-cfg-${def.key}`;
  const raw = asText(value);
  // Valor mascarado devolvido pelo backend: no formato "__masked__:…abc123", com o final sendo um trecho identificável do valor original.
  const masked = isSecret && raw.startsWith("__masked__");
  const maskedTail = masked ? (raw.split("…")[1] ?? "") : "";

  if (def.kind === "switch") {
    return (
      <div className="flex items-center gap-2 text-sm">
        <Switch checked={value === true} onCheckedChange={onChange} aria-label={def.label} />
        {def.label}
        {def.help && <span className="text-muted-foreground">({def.help})</span>}
      </div>
    );
  }

  if (def.kind === "select") {
    return (
      <div className="grid gap-2">
        <Label>{def.label}</Label>
        <Select value={raw || def.options?.[0]?.value} onValueChange={onChange}>
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(def.options ?? []).map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {o.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    );
  }

  // Os controles são despachados conforme o tipo do campo. Usa-se uma cadeia de if em vez de ternários aninhados porque aqui é preciso distinguir quatro controles,
  // e com três níveis de ternário já é preciso parar para contar parênteses.
  function control() {
    if (def.kind === "textarea" || def.kind === "kv") {
      return (
        <Textarea
          id={id}
          className="font-mono"
          placeholder={def.placeholder}
          value={masked ? "" : raw}
          onChange={(e) => onChange(e.target.value)}
        />
      );
    }
    if (def.kind === "list") {
      return (
        <Input
          id={id}
          value={Array.isArray(value) ? (value as string[]).join(", ") : raw}
          onChange={(e) => onChange(e.target.value)}
          placeholder={def.placeholder}
        />
      );
    }
    return (
      <Input
        id={id}
        className={def.kind === "text" ? "font-mono" : ""}
        type={inputType(def.kind)}
        placeholder={def.placeholder}
        value={masked ? "" : raw}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }

  const hint = masked ? (
    <p className="text-muted-foreground flex items-center gap-1 text-xs">
      <CheckIcon className="size-3" />
      Salvo{maskedTail ? ` (final ${maskedTail})` : ""} · informe um novo valor para sobrescrever; deixe vazio para remover o item
    </p>
  ) : (
    def.help && <p className="text-muted-foreground text-xs">{def.help}</p>
  );

  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{def.label}</Label>
      {control()}
      {hint}
    </div>
  );
}

// FilterSummary resume as condições de filtro em uma linha, para que o card mostre o que o canal envia sem precisar ser expandido.
export function FilterSummary({ filter }: { filter: NotificationFilter }) {
  const parts: string[] = [];
  if (filter.min_severity) {
    parts.push(SEVERITY_OPTIONS.find((o) => o.value === filter.min_severity)?.label ?? filter.min_severity);
  }
  if (filter.vulnclass_include?.length) parts.push(`tipos incluem ${filter.vulnclass_include.length} termos`);
  if (filter.vulnclass_exclude?.length) parts.push(`exclui ${filter.vulnclass_exclude.length} termos`);
  if (filter.task_ids?.length) parts.push(`${filter.task_ids.length} tarefas`);
  if (filter.asset_ids?.length) parts.push(`${filter.asset_ids.length} ativos`);
  if (filter.on_status_change) parts.push("inclui mudanças de status");
  if (parts.length === 0) {
    return <p className="text-muted-foreground text-sm">Todas as vulnerabilidades</p>;
  }
  return <p className="text-muted-foreground text-sm">{parts.join(" · ")}</p>;
}
