"use client";

import * as React from "react";

import Link from "next/link";

import {
  ArrowUpRightIcon,
  ChevronRightIcon,
  FileTextIcon,
  FlaskConicalIcon,
  RotateCcwIcon,
  ShieldAlertIcon,
  Trash2Icon,
} from "lucide-react";

import { CopyButton } from "@/components/copy-button";
import { Markdown } from "@/components/markdown";
import { StatusBadge } from "@/components/status-badge";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { statusMeta } from "@/lib/status";
import type { ActiveFindingRetest, Finding, FindingStatus, Severity } from "@/lib/types";
import { cn } from "@/lib/utils";

export const SEVERITIES: Severity[] = ["critical", "high", "medium", "low"];

export const FINDING_STATUSES: FindingStatus[] = [
  "pending",
  "in_progress",
  "confirmed",
  "resolved",
  "fixed",
  "false_positive",
  "ignored",
  "duplicate",
  "risk_accepted",
];

export const UNASSIGNED_TASK = "__unassigned__";

// Buffer de edição na linha: nome/categoria/severidade da linha expandida atual.
export interface FindingEdit {
  name: string;
  vulnclass: string;
  severity: Severity;
}

export type FindingReport = { status: "loading" | "done" | "error"; text: string };

// Exploration node ids are only unique inside a task. Prefer the persisted
// finding id and otherwise namespace the node id by task so editing one group
// cannot update a similarly-named node in another expanded group.
export function findingRowKey(finding: Finding): string {
  if (finding.finding_id) return `finding:${finding.finding_id}`;
  return `node:${finding.task_id ?? UNASSIGNED_TASK}:${finding.id}`;
}

export function isSameFinding(left: Finding, right: Finding): boolean {
  return findingRowKey(left) === findingRowKey(right);
}

export function fmtTime(ts: string) {
  return new Date(ts).toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

const COLUMN_COUNT = 9;

interface FindingsTableProps {
  items: Finding[];
  selectedIds: Set<string>;
  onToggleSelected: (id: string, checked: boolean) => void;
  onToggleSelectedPage: (ids: string[], checked: boolean) => void;
  /** findingRowKey da linha expandida atual; null = todas recolhidas. */
  expandedKey: string | null;
  onToggleRow: (finding: Finding) => void;
  reports: Record<string, FindingReport>;
  edit: FindingEdit | null;
  onEditChange: React.Dispatch<React.SetStateAction<FindingEdit | null>>;
  saving: boolean;
  onSave: (finding: Finding) => void;
  onStatusChange: (finding: Finding, next: FindingStatus) => void;
  onRetest: (finding: Finding) => void;
  activeRetests: Record<string, ActiveFindingRetest>;
  onDeepen: (finding: Finding) => void;
  onDelete: (finding: Finding) => void;
  /** Rótulo de acessibilidade da caixa de seleção geral; a redação difere entre a visão plana e a agrupada. */
  selectAllLabel?: string;
}

// FindingsTable é o corpo da tabela da lista de achados; a visão plana e a visão agrupada por tarefa compartilham a mesma renderização de linha
// (marcar / expandir na linha / renomear e mudar status na linha / reteste / aprofundar / excluir); a diferença está só no contêiner externo e na paginação.
export function FindingsTable({
  items,
  selectedIds,
  onToggleSelected,
  onToggleSelectedPage,
  expandedKey,
  onToggleRow,
  reports,
  edit,
  onEditChange,
  saving,
  onSave,
  onStatusChange,
  onRetest,
  activeRetests,
  onDeepen,
  onDelete,
  selectAllLabel = "Selecionar todos da página atual",
}: FindingsTableProps) {
  const selectableIds = items.map((finding) => finding.finding_id).filter((id): id is string => Boolean(id));
  const selectedCount = selectableIds.filter((id) => selectedIds.has(id)).length;
  let headerChecked: boolean | "indeterminate" = false;
  if (selectableIds.length > 0 && selectedCount === selectableIds.length) {
    headerChecked = true;
  } else if (selectedCount > 0) {
    headerChecked = "indeterminate";
  }

  return (
    /* Larguras de coluna fixas garantem que o conteúdo expandido não alargue a tabela; em telas estreitas, só rola horizontalmente dentro da tabela. */
    <Table className="min-w-[60rem] table-fixed">
      <TableHeader>
        <TableRow>
          <TableHead className="w-8">
            <Checkbox
              checked={headerChecked}
              onCheckedChange={(checked) => onToggleSelectedPage(selectableIds, checked === true)}
              aria-label={selectAllLabel}
            />
          </TableHead>
          <TableHead className="w-8" />
          <TableHead className="w-20">Severidade</TableHead>
          <TableHead>Nome da vulnerabilidade</TableHead>
          <TableHead className="w-44">Ativos</TableHead>
          <TableHead className="w-28">Status</TableHead>
          <TableHead className="w-32">Tarefa de origem</TableHead>
          <TableHead className="w-24">Data</TableHead>
          <TableHead className="w-48">Ações</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {items.map((f) => {
          const rowKey = findingRowKey(f);
          const open = expandedKey === rowKey;
          const retest = f.finding_id ? activeRetests[f.finding_id] : undefined;
          return (
            <React.Fragment key={rowKey}>
              <TableRow
                className="cursor-pointer"
                role="button"
                tabIndex={0}
                aria-expanded={open}
                onClick={() => onToggleRow(f)}
                onKeyDown={(event) => {
                  if (event.target !== event.currentTarget || (event.key !== "Enter" && event.key !== " ")) return;
                  event.preventDefault();
                  onToggleRow(f);
                }}
              >
                <TableCell onClick={(e) => e.stopPropagation()}>
                  {f.finding_id && (
                    <Checkbox
                      checked={selectedIds.has(f.finding_id)}
                      onCheckedChange={(c) => onToggleSelected(f.finding_id as string, c === true)}
                      aria-label="Selecionar esta vulnerabilidade"
                    />
                  )}
                </TableCell>
                <TableCell>
                  <ChevronRightIcon
                    className={cn("size-4 text-muted-foreground transition-transform", open && "rotate-90")}
                  />
                </TableCell>
                <TableCell>
                  <StatusBadge domain="severity" value={f.severity} dot />
                </TableCell>
                <TableCell className="max-w-md">
                  <div className="flex min-w-0 flex-col gap-0.5">
                    {f.finding_id ? (
                      <Link
                        href={`/function/findings/detail?id=${f.finding_id}`}
                        onClick={(e) => e.stopPropagation()}
                        className="truncate font-medium hover:text-primary hover:underline"
                        title="Ver detalhes do achado"
                      >
                        {f.name || f.vulnclass || "Sem classificação"}
                      </Link>
                    ) : (
                      <span className="truncate font-medium">{f.name || f.vulnclass || "Sem classificação"}</span>
                    )}
                    <span className="truncate text-xs text-muted-foreground">{f.summary}</span>
                    <Badge variant="outline">Evidências de tráfego: {f.traffic_count ?? 0}</Badge>
                  </div>
                </TableCell>
                <TableCell>
                  {f.assets && f.assets.length > 0 ? (
                    <div className="flex flex-wrap gap-1">
                      {f.assets.slice(0, 3).map((a) => (
                        <code
                          key={a.id}
                          className="max-w-full truncate rounded bg-muted px-1.5 py-0.5 font-mono text-xs"
                          title={`${a.type} · ${a.label}`}
                        >
                          {a.label}
                        </code>
                      ))}
                      {f.assets.length > 3 && (
                        <span className="text-xs text-muted-foreground">+{f.assets.length - 3}</span>
                      )}
                    </div>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TableCell>
                <TableCell onClick={(e) => e.stopPropagation()}>
                  {f.finding_id ? (
                    <Select value={f.status} onValueChange={(v) => onStatusChange(f, v as FindingStatus)}>
                      <SelectTrigger size="sm" className="h-7 w-full border-none px-1 shadow-none focus-visible:ring-0">
                        <StatusBadge domain="finding" value={f.status} dot />
                      </SelectTrigger>
                      <SelectContent position="popper" align="end">
                        {FINDING_STATUSES.map((st) => (
                          <SelectItem key={st} value={st}>
                            {statusMeta("finding", st).label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  ) : (
                    <StatusBadge domain="finding" value={f.status} dot />
                  )}
                </TableCell>
                <TableCell>
                  {f.task_id ? (
                    <Link
                      href={`/function/tasks/detail?id=${f.task_id}`}
                      onClick={(e) => e.stopPropagation()}
                      className="inline-flex max-w-full items-center gap-1 text-primary hover:underline"
                      title={f.task_description}
                    >
                      <span className="truncate">{f.task_description}</span>
                      <ArrowUpRightIcon className="size-3 shrink-0" />
                    </Link>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground tabular-nums">{fmtTime(f.ts)}</TableCell>
                <TableCell onClick={(e) => e.stopPropagation()}>
                  <div className="flex items-center gap-1">
                    {retest ? (
                      <Button asChild size="sm" variant="ghost">
                        <Link href={`/chat?c=${retest.conversation_id}`} title="Ver a sessão de reteste em andamento">
                          <Spinner data-icon="inline-start" />
                          Em reteste
                        </Link>
                      </Button>
                    ) : null}
                    {!retest && f.finding_id && !f.inherited ? (
                      <Button size="sm" variant="ghost" onClick={() => onRetest(f)} title="Retestar esta vulnerabilidade em uma sessão independente">
                        <RotateCcwIcon data-icon="inline-start" />
                        Retestar
                      </Button>
                    ) : null}
                    {f.finding_id && f.task_id && (
                      <Button size="sm" variant="ghost" onClick={() => onDeepen(f)}>
                        <FlaskConicalIcon data-icon="inline-start" />
                        Aprofundar
                      </Button>
                    )}
                    {f.finding_id && (
                      <AlertDialog>
                        <AlertDialogTrigger asChild>
                          <Button
                            size="icon"
                            variant="ghost"
                            className="size-7 text-muted-foreground hover:text-destructive"
                            aria-label="Excluir vulnerabilidade"
                          >
                            <Trash2Icon className="size-4" />
                          </Button>
                        </AlertDialogTrigger>
                        <AlertDialogContent>
                          <AlertDialogHeader>
                            <AlertDialogTitle>Confirmar exclusão desta vulnerabilidade?</AlertDialogTitle>
                            <AlertDialogDescription className="break-words">
                              "
                              <span className="break-all">
                                {f.name || f.vulnclass || f.summary || `#${f.finding_id}`}
                              </span>
                              " será excluída permanentemente e também removida da lista de achados, da aba de achados da tarefa e do grafo de exploração. Esta ação não pode ser desfeita.
                            </AlertDialogDescription>
                          </AlertDialogHeader>
                          <AlertDialogFooter>
                            <AlertDialogCancel>Cancelar</AlertDialogCancel>
                            <AlertDialogAction onClick={() => onDelete(f)}>Excluir</AlertDialogAction>
                          </AlertDialogFooter>
                        </AlertDialogContent>
                      </AlertDialog>
                    )}
                  </div>
                </TableCell>
              </TableRow>
              {open && (
                <TableRow className="hover:bg-transparent">
                  {/* whitespace-normal substitui o nowrap padrão de TableCell, senão o texto da área expandida
                      seria forçado a uma única linha e transbordaria da célula. */}
                  <TableCell colSpan={COLUMN_COUNT} className="bg-muted/30 whitespace-normal">
                    <div className="flex flex-col gap-2 px-2 py-1">
                      {/* Edição na linha: nome/categoria/severidade, editáveis e salváveis (somente linhas de finding independentes). */}
                      {f.finding_id && edit && (
                        <div className="flex flex-wrap items-end gap-3 rounded-md border bg-background px-3 py-2.5">
                          <div className="flex min-w-[12rem] flex-1 flex-col gap-1">
                            <Label className="text-xs text-muted-foreground">Nome da vulnerabilidade</Label>
                            <Input
                              value={edit.name}
                              onChange={(e) => onEditChange((s) => (s ? { ...s, name: e.target.value } : s))}
                              placeholder="Título legível; se vazio, usa a categoria"
                            />
                          </div>
                          <div className="flex min-w-[10rem] flex-col gap-1">
                            <Label className="text-xs text-muted-foreground">Categoria</Label>
                            <Input
                              value={edit.vulnclass}
                              onChange={(e) => onEditChange((s) => (s ? { ...s, vulnclass: e.target.value } : s))}
                              placeholder="ex.: SQL Injection"
                            />
                          </div>
                          <div className="flex flex-col gap-1">
                            <Label className="text-xs text-muted-foreground">Severidade</Label>
                            <Select
                              value={edit.severity}
                              onValueChange={(v) => onEditChange((s) => (s ? { ...s, severity: v as Severity } : s))}
                            >
                              <SelectTrigger size="sm" className="w-28">
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                {SEVERITIES.map((sv) => (
                                  <SelectItem key={sv} value={sv}>
                                    {statusMeta("severity", sv).label}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          </div>
                          <Button size="sm" disabled={saving} onClick={() => onSave(f)}>
                            {saving ? "Salvando…" : "Salvar"}
                          </Button>
                        </div>
                      )}
                      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                        <ShieldAlertIcon className="size-3.5" />
                        Evidência
                        {f.vulnclass && (
                          <span>
                            · Tipo:
                            <code className="rounded bg-muted px-1.5 py-0.5 font-mono">{f.vulnclass}</code>
                          </span>
                        )}
                        {f.param_id && <code className="rounded bg-muted px-1.5 py-0.5 font-mono">{f.param_id}</code>}
                        {f.assets && f.assets.length > 0 && (
                          <span className="flex flex-wrap items-center gap-1">
                            · Ativo:
                            {f.assets.map((a) => (
                              <code key={a.id} className="rounded bg-muted px-1.5 py-0.5 font-mono" title={a.type}>
                                {a.label}
                              </code>
                            ))}
                          </span>
                        )}
                      </div>
                      <pre className="overflow-x-auto rounded-md bg-muted px-3 py-2 font-mono text-xs whitespace-pre-wrap">
                        {f.evidence}
                      </pre>

                      {/* Relatório detalhado (Markdown): carregado sob demanda por finding_id ao expandir, dispensando abrir a página de detalhes. */}
                      {f.finding_id && (
                        <div className="flex flex-col gap-1.5">
                          <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
                            <span className="flex items-center gap-2">
                              <FileTextIcon className="size-3.5" />
                              Relatório detalhado
                            </span>
                            {reports[rowKey]?.status === "done" && reports[rowKey]?.text.trim() && (
                              <CopyButton
                                text={reports[rowKey]?.text}
                                successMessage="Relatório detalhado copiado"
                                variant="ghost"
                                className="h-6 px-2 text-xs"
                              />
                            )}
                          </div>
                          {(() => {
                            const rep = reports[rowKey];
                            if (!rep || rep.status === "loading")
                              return <p className="text-xs text-muted-foreground">Carregando…</p>;
                            if (rep.status === "error")
                              return <p className="text-xs text-muted-foreground">Falha ao carregar o relatório.</p>;
                            if (!rep.text.trim())
                              return <p className="text-xs text-muted-foreground">Ainda não há relatório detalhado.</p>;
                            return (
                              // break-words é herdado por parágrafos/listas; o pre ganha ainda
                              // whitespace-pre-wrap para que blocos de código também quebrem linha — senão linhas longas de código / URLs longas
                              // alargariam a célula colSpan e empurrariam a tabela inteira para uma barra de rolagem horizontal.
                              <div className="min-w-0 break-words rounded-md border bg-background px-3 py-2 [&_pre]:whitespace-pre-wrap">
                                <Markdown text={rep.text} />
                              </div>
                            );
                          })()}
                        </div>
                      )}
                    </div>
                  </TableCell>
                </TableRow>
              )}
            </React.Fragment>
          );
        })}
        {items.length === 0 && (
          <TableRow>
            <TableCell colSpan={COLUMN_COUNT} className="py-12 text-center text-sm text-muted-foreground">
              Nenhum achado correspondente.
            </TableCell>
          </TableRow>
        )}
      </TableBody>
    </Table>
  );
}
