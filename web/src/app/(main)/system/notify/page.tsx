"use client";

import * as React from "react";

import { BellIcon, PlusIcon, SendIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { NotificationChannel, NotificationFilter, NotificationMeta } from "@/lib/types";

import {
  CHANNEL_FIELDS,
  type ChannelForm,
  emptyForm,
  KIND_LABEL,
  parseIDs,
  parseKeywords,
  parseKV,
  SEVERITY_OPTIONS,
} from "./_components/channel-fields";
import { ConfigField, FilterSummary } from "./_components/channel-form";
import { DeliveryList } from "./_components/delivery-list";
import { formatBacklog, StatTile } from "./_components/stat-tile";

// Esta página cuida apenas da orquestração: carregar dados, manter o estado do formulário e chamar as APIs.
// A definição e a interpretação dos campos estão em _components/channel-fields.ts, os controles e o resumo de filtros em
// _components/channel-form.tsx, e os registros de entrega em _components/delivery-list.tsx —
// foram separados porque cada um pode ser lido isoladamente, enquanto num único arquivo esta página chegava perto de 1100 linhas.
export default function NotifyPage() {
  const [meta, setMeta] = React.useState<NotificationMeta | null>(null);
  const [channels, setChannels] = React.useState<NotificationChannel[]>([]);
  const [tab, setTab] = React.useState<"channels" | "deliveries">("channels");

  const [open, setOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<NotificationChannel | null>(null);
  const [form, setForm] = React.useState<ChannelForm>(emptyForm("dingtalk"));
  const [saving, setSaving] = React.useState(false);
  const [testing, setTesting] = React.useState(false);

  const [globalSaving, setGlobalSaving] = React.useState(false);
  const [baseURL, setBaseURL] = React.useState("");
  const [digestMin, setDigestMin] = React.useState("");

  const load = React.useCallback(() => {
    api
      .notifyMeta()
      .then((m) => {
        setMeta(m);
        setBaseURL(m.public_base_url);
        setDigestMin(m.digest_interval_min);
      })
      .catch((e) => toast.error("Falha ao ler a configuração de notificações: " + (e as Error).message));
    // A falha ao carregar a lista de canais precisa ser exibida: uma falha silenciosa apareceria como "nenhum canal",
    // e o usuário acharia que a configuração se perdeu, o que assusta mais do que um erro direto.
    api
      .notifyChannels()
      .then(setChannels)
      .catch((e) => toast.error("Falha ao ler a lista de canais: " + (e as Error).message));
  }, []);
  React.useEffect(() => {
    load();
  }, [load]);

  function setF(patch: Partial<ChannelForm>) {
    setForm((f) => ({ ...f, ...patch }));
  }
  function setCfg(key: string, value: unknown) {
    setForm((f) => ({ ...f, config: { ...f.config, [key]: value } }));
  }

  function openAdd() {
    setEditing(null);
    setForm(emptyForm(meta?.kinds[0]?.kind ?? "dingtalk"));
    setOpen(true);
  }

  function openEdit(ch: NotificationChannel) {
    setEditing(ch);
    // No backend o filter é uma struct Go, sempre serializada como objeto (nunca null), então não é preciso fallback.
    const f = ch.filter;
    setForm({
      name: ch.name,
      kind: ch.kind,
      mode: ch.mode,
      enabled: ch.enabled,
      ratePerMin: String(ch.rate_per_min),
      // No config devolvido pelo backend as credenciais vêm como valores mascarados; são colocados no formulário como estão e, no envio,
      // devolvidos como estão, e o backend com isso mantém o valor original guardado no banco.
      config: { ...ch.config },
      minSeverity: f.min_severity ?? "",
      includeText: (f.vulnclass_include ?? []).join("\n"),
      excludeText: (f.vulnclass_exclude ?? []).join("\n"),
      taskIDsText: (f.task_ids ?? []).join(","),
      assetIDsText: (f.asset_ids ?? []).join(","),
      onStatusChange: f.on_status_change ?? false,
    });
    setOpen(true);
  }

  // buildConfig converte o estado do formulário em config do canal.
  //
  // Uma única regra, dois tipos de valor:
  //   - valor mascarado ("__masked__...") devolvido como está → o backend interpreta como "este campo não mudou, mantenha o valor do banco"
  //   - todo o resto é enviado como o usuário digitou; string vazia significa "limpar este campo"
  //
  // Não se dá tratamento especial aos campos de credencial (por exemplo "credencial vazia = ignorar"), porque isso impediria o usuário de **limpar**
  // uma chave configurada errada — a interface não teria nenhuma ação capaz de expressar "quero apagar isto". Com a regra atual,
  // esvaziar a caixa equivale a limpar o campo: semântica única e controlada pelo usuário.
  // O valor mascarado nunca aparece na caixa de entrada (ver ConfigField), então "há texto na caixa" sempre equivale a
  // "o usuário preencheu por conta própria".
  function buildConfig(): Record<string, unknown> {
    const defs = CHANNEL_FIELDS[form.kind] ?? [];
    const out: Record<string, unknown> = {};
    for (const d of defs) {
      const raw = form.config[d.key];
      if (d.kind === "switch") {
        out[d.key] = raw === true;
        continue;
      }
      if (typeof raw === "string" && raw.startsWith("__masked__")) {
        out[d.key] = raw;
        continue;
      }
      if (d.kind === "number") {
        const n = Number(raw);
        out[d.key] = Number.isFinite(n) && n > 0 ? n : 0;
        continue;
      }
      if (d.kind === "kv") {
        out[d.key] = parseKV(String(raw ?? ""));
        continue;
      }
      if (d.kind === "list") {
        out[d.key] = String(raw ?? "")
          .split(/[\s,，]+/)
          .map((s) => s.trim())
          .filter(Boolean);
        continue;
      }
      out[d.key] = String(raw ?? "").trim();
    }
    return out;
  }

  function buildFilter(): NotificationFilter {
    return {
      min_severity: form.minSeverity || undefined,
      vulnclass_include: parseKeywords(form.includeText),
      vulnclass_exclude: parseKeywords(form.excludeText),
      task_ids: parseIDs(form.taskIDsText),
      asset_ids: parseIDs(form.assetIDsText),
      on_status_change: form.onStatusChange,
    };
  }

  async function saveForm() {
    if (!form.name.trim()) {
      toast.error("Preencha o nome do canal");
      return;
    }
    setSaving(true);
    try {
      const payload = {
        name: form.name.trim(),
        kind: form.kind,
        mode: form.mode,
        enabled: form.enabled,
        config: buildConfig(),
        filter: buildFilter(),
        rate_per_min: form.ratePerMin.trim() === "" ? undefined : Number(form.ratePerMin),
      };
      if (editing) {
        await api.notifyUpdateChannel(editing.id, payload);
        toast.success("Salvo");
        setOpen(false);
      } else {
        await api.notifyCreateChannel(payload);
        toast.success("Canal adicionado");
        setOpen(false);
      }
      load();
    } catch (e) {
      toast.error("Falha ao salvar: " + (e as Error).message);
    } finally {
      setSaving(false);
    }
  }

  async function testChannel() {
    if (!editing) return;
    setTesting(true);
    try {
      const r = await api.notifyTestChannel(editing.id);
      toast.success(`Mensagem de teste enviada (${r.latency_ms} ms), confirme no grupo`);
    } catch (e) {
      // O backend devolve fielmente o erro original retornado pelo canal; é a única pista para diagnosticar a configuração, então é exibido como veio.
      toast.error("Falha no teste: " + (e as Error).message, { duration: 12000 });
    } finally {
      setTesting(false);
    }
  }

  async function removeChannel(ch: NotificationChannel) {
    try {
      await api.notifyDeleteChannel(ch.id);
      toast.success(`Excluído: ${ch.name}`);
      setOpen(false);
      load();
    } catch (e) {
      toast.error("Falha ao excluir: " + (e as Error).message);
    }
  }

  async function toggleEnabled(ch: NotificationChannel) {
    try {
      await api.notifyUpdateChannel(ch.id, { enabled: !ch.enabled });
      load();
    } catch (e) {
      toast.error("Falha na operação: " + (e as Error).message);
    }
  }

  async function toggleGlobal(on: boolean) {
    setGlobalSaving(true);
    try {
      await api.setSettings({ notify_enabled: on });
      setMeta((m) => (m ? { ...m, enabled: on } : m));
      toast.success(on ? "Notificações ativadas" : "Notificações pausadas");
    } catch (e) {
      toast.error("Falha na operação: " + (e as Error).message);
    } finally {
      setGlobalSaving(false);
    }
  }

  async function saveGlobal() {
    setGlobalSaving(true);
    try {
      const patch: Record<string, unknown> = { notify_public_base_url: baseURL.trim() };
      const n = Number(digestMin);
      if (Number.isFinite(n) && n > 0) patch.notify_digest_interval_min = n;
      await api.setSettings(patch);
      toast.success("Salvo");
      load();
    } catch (e) {
      toast.error("Falha ao salvar: " + (e as Error).message);
    } finally {
      setGlobalSaving(false);
    }
  }

  const fields = CHANNEL_FIELDS[form.kind] ?? [];
  const secretKeys = new Set(meta?.kinds.find((k) => k.kind === form.kind)?.secret_keys ?? []);
  const defaultRate = meta?.kinds.find((k) => k.kind === form.kind)?.default_rate_per_min ?? 0;

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">Notificações push</h1>
          <p className="text-muted-foreground text-sm">
            Ao encontrar vulnerabilidades, envia para canais como DingTalk / Feishu / WeCom · cada canal pode ter seu próprio momento de envio e regras de filtro
          </p>
        </div>
        {meta && (
          // Usa-se div em vez de label: o Switch já tem aria-label, e envolvê-lo em outro label
          // não se associaria a nenhum controle nativo e ainda faria parecer que clicar no texto deveria alternar.
          <div className="flex shrink-0 items-center gap-2 text-sm">
            <span className="text-muted-foreground">Chave geral</span>
            <Switch
              checked={meta.enabled}
              disabled={globalSaving}
              onCheckedChange={toggleGlobal}
              aria-label="Chave geral de notificações"
            />
          </div>
        )}
      </div>

      {meta && (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-5">
          <StatTile label="Canais" value={`${meta.stats.channels_on} / ${meta.stats.channels}`} hint="ativos / total" />
          <StatTile label="Entregues hoje" value={String(meta.stats.sent_today)} />
          <StatTile label="Pendentes" value={String(meta.stats.pending)} />
          <StatTile label="Falhas" value={String(meta.stats.failed)} tone={meta.stats.failed > 0 ? "red" : undefined} />
          <StatTile
            label="Maior backlog"
            value={formatBacklog(meta.stats.backlog_age_ms)}
            // A idade do backlog é muito mais útil que a quantidade: 3 itens acumulados podem significar de 3 segundos a 3 horas.
            hint={meta.stats.backlog_age_ms > 5 * 60_000 ? "O envio pode estar travado" : undefined}
            tone={meta.stats.backlog_age_ms > 5 * 60_000 ? "red" : undefined}
          />
        </div>
      )}

      <Card className="gap-3">
        <CardHeader>
          <CardTitle className="text-base">Configurações globais</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-4 sm:grid-cols-2">
          <div className="grid gap-2">
            <Label htmlFor="n-base">Endereço de link de retorno</Label>
            <Input
              id="n-base"
              placeholder="https://artex.example.com"
              value={baseURL}
              onChange={(e) => setBaseURL(e.target.value)}
            />
            <p className="text-muted-foreground text-xs">Endereço para o qual aponta o botão &quot;Ver detalhes&quot; na mensagem. Deixe vazio para não incluir o botão.</p>
          </div>
          <div className="grid gap-2">
            <Label htmlFor="n-digest">Período de resumo (minutos)</Label>
            <Input
              id="n-digest"
              type="number"
              min={1}
              max={1440}
              placeholder="30"
              value={digestMin}
              onChange={(e) => setDigestMin(e.target.value)}
            />
            <p className="text-muted-foreground text-xs">Vale só para canais no modo &quot;Resumo&quot;.</p>
          </div>
          <div className="sm:col-span-2">
            <Button onClick={saveGlobal} disabled={globalSaving}>
              Salvar configurações globais
            </Button>
          </div>
        </CardContent>
      </Card>

      <Tabs value={tab} onValueChange={(v) => setTab(v as "channels" | "deliveries")} className="flex flex-col gap-4">
        <TabsList>
          <TabsTrigger value="channels">Canais</TabsTrigger>
          <TabsTrigger value="deliveries">Registros de entrega</TabsTrigger>
        </TabsList>

        <TabsContent value="channels">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3">
            <button
              type="button"
              onClick={openAdd}
              className="text-foreground/70 border-foreground/70 hover:bg-muted/60 hover:shadow-sm flex min-h-[130px] flex-col items-center justify-center gap-2 rounded-xl border border-dashed transition"
            >
              <PlusIcon className="size-6" />
              <span className="text-sm">Adicionar canal</span>
            </button>

            {channels.map((ch) => (
              <Card
                key={ch.id}
                onClick={() => openEdit(ch)}
                className="hover:border-primary/60 cursor-pointer gap-3 transition hover:shadow-sm"
              >
                <CardHeader>
                  <div className="flex items-center gap-2">
                    <BellIcon className="text-muted-foreground size-4 shrink-0" />
                    <CardTitle className="truncate text-base">{ch.name}</CardTitle>
                    {/* O card inteiro é clicável (abre a edição), então estes dois controles precisam engolir o evento de propagação,
                            senão o switch/excluir também acionariam a edição. O stopPropagation fica nos próprios controles,
                            e não em uma div envolvente: a div criaria um elemento estático que "parece interativo mas não tem
                            papel", o que dispara alerta de a11y e também não faz sentido semântico. */}
                    <div className="ml-auto flex items-center gap-2">
                      <Switch
                        checked={ch.enabled}
                        onCheckedChange={() => toggleEnabled(ch)}
                        onClick={(e) => e.stopPropagation()}
                        aria-label="Ativar"
                      />
                      <Button
                        size="icon"
                        variant="outline"
                        aria-label="Excluir"
                        onClick={(e) => {
                          e.stopPropagation();
                          // void descarta explicitamente a Promise: removeChannel faz o próprio catch e toast,
                          // aqui não é preciso await (onClick não é async).
                          void removeChannel(ch);
                        }}
                      >
                        <Trash2Icon className="text-destructive" />
                      </Button>
                    </div>
                  </div>
                </CardHeader>
                <CardContent className="grid gap-3">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="outline">{KIND_LABEL[ch.kind] ?? ch.kind}</Badge>
                    <Badge variant="outline">{ch.mode === "digest" ? "Resumo" : "Tempo real"}</Badge>
                    {!ch.enabled && <Badge variant="outline">Desativado</Badge>}
                  </div>
                  <FilterSummary filter={ch.filter} />
                </CardContent>
              </Card>
            ))}
          </div>
        </TabsContent>

        <TabsContent value="deliveries">
          <DeliveryList channels={channels} />
        </TabsContent>
      </Tabs>

      <Sheet open={open} onOpenChange={setOpen}>
        <SheetContent side="right" className="w-full data-[side=right]:sm:max-w-lg">
          <SheetHeader>
            <SheetTitle>{editing ? editing.name : "Adicionar canal de notificação"}</SheetTitle>
            <SheetDescription>
              {KIND_LABEL[form.kind] ?? form.kind}
              {defaultRate > 0 ? ` · limite padrão ${defaultRate} msgs/minuto` : " · sem limite"}
            </SheetDescription>
          </SheetHeader>

          <div className="flex min-h-0 flex-1 flex-col overflow-y-auto px-4">
            <div className="grid gap-4 py-4">
              <div className="grid gap-2">
                <Label>Tipo de canal</Label>
                <Select
                  value={form.kind}
                  onValueChange={(v) => {
                    // Trocar o tipo equivale a trocar todo o conjunto de campos de credencial; não se pode mesclar a configuração antiga.
                    setF({ kind: v, config: {} });
                  }}
                  disabled={!!editing}
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {(meta?.kinds ?? []).map((k) => (
                      <SelectItem key={k.kind} value={k.kind}>
                        {KIND_LABEL[k.kind] ?? k.kind}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                {editing && (
                  <p className="text-muted-foreground text-xs">
                    O tipo de canal não pode ser alterado — mudar o tipo equivale a trocar de credenciais, crie um novo canal.
                  </p>
                )}
              </div>

              <div className="grid gap-2">
                <Label htmlFor="n-name">Nome do canal</Label>
                <Input
                  id="n-name"
                  placeholder="Grupo de resposta a incidentes / Grupo de avisos diários"
                  value={form.name}
                  onChange={(e) => setF({ name: e.target.value })}
                />
              </div>

              {fields.length === 0 ? (
                <p className="text-muted-foreground text-sm">
                  O formulário deste canal ainda não foi definido (falta a entrada em CHANNEL_FIELDS no frontend); complete-a e tente de novo.
                </p>
              ) : (
                fields.map((d) => (
                  <ConfigField
                    key={d.key}
                    def={d}
                    value={form.config[d.key]}
                    isSecret={secretKeys.has(d.key)}
                    onChange={(v) => setCfg(d.key, v)}
                  />
                ))
              )}

              <div className="grid gap-2">
                <Label>Momento do envio</Label>
                <Select value={form.mode} onValueChange={(v) => setF({ mode: v as "realtime" | "digest" })}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="realtime">Tempo real · cada vulnerabilidade gera uma mensagem própria</SelectItem>
                    <SelectItem value="digest">Resumo · agrupa em uma mensagem por período</SelectItem>
                  </SelectContent>
                </Select>
                <p className="text-muted-foreground text-xs">
                  Para ter "alta em tempo real, o resto em resumo", crie dois canais: um em tempo real com limiar alto e outro em resumo sem limite de nível.
                </p>
              </div>

              <div className="grid gap-2">
                <Label htmlFor="n-rate">Limite de envio (msgs/minuto)</Label>
                <Input
                  id="n-rate"
                  type="number"
                  min={0}
                  placeholder={defaultRate > 0 ? String(defaultRate) : "0 = sem limite"}
                  value={form.ratePerMin}
                  onChange={(e) => setF({ ratePerMin: e.target.value })}
                />
                <p className="text-muted-foreground text-xs">
                  Deixe vazio para usar o padrão do canal; 0 significa sem limite. Exceder o limite não perde mensagens, apenas adia o envio.
                </p>
              </div>

              <div className="border-t pt-4">
                <p className="mb-3 text-sm font-medium">Regras de filtro (vazio = sem filtro)</p>
                <div className="grid gap-4">
                  <div className="grid gap-2">
                    <Label>Nível mínimo</Label>
                    <Select
                      value={form.minSeverity || "all"}
                      onValueChange={(v) => setF({ minSeverity: v === "all" ? "" : v })}
                    >
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {SEVERITY_OPTIONS.map((o) => (
                          <SelectItem key={o.value || "all"} value={o.value || "all"}>
                            {o.label}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                  <div className="grid gap-2">
                    <Label htmlFor="n-inc">Enviar só estes tipos de vulnerabilidade</Label>
                    <Textarea
                      id="n-inc"
                      placeholder={"Injeção de SQL\nExecução de comandos"}
                      value={form.includeText}
                      onChange={(e) => setF({ includeText: e.target.value })}
                    />
                    <p className="text-muted-foreground text-xs">
                      Uma palavra-chave por linha, correspondência de substring sem distinção de maiúsculas/minúsculas. Vazio = todos os tipos.
                    </p>
                  </div>
                  <div className="grid gap-2">
                    <Label htmlFor="n-exc">Excluir estes tipos de vulnerabilidade</Label>
                    <Textarea
                      id="n-exc"
                      placeholder={"Vazamento de informações"}
                      value={form.excludeText}
                      onChange={(e) => setF({ excludeText: e.target.value })}
                    />
                    <p className="text-muted-foreground text-xs">A exclusão tem prioridade sobre a inclusão: se ambas corresponderem, será excluído.</p>
                  </div>
                  <div className="grid gap-2">
                    <Label htmlFor="n-tasks">Limitar a IDs de tarefa</Label>
                    <Input
                      id="n-tasks"
                      placeholder="1, 2, 3"
                      value={form.taskIDsText}
                      onChange={(e) => setF({ taskIDsText: e.target.value })}
                    />
                  </div>
                  <div className="grid gap-2">
                    <Label htmlFor="n-assets">Limitar a IDs de ativo</Label>
                    <Input
                      id="n-assets"
                      placeholder="10, 11"
                      value={form.assetIDsText}
                      onChange={(e) => setF({ assetIDsText: e.target.value })}
                    />
                    <p className="text-muted-foreground text-xs">Tarefas/ativos vazios = sem limite; ao preencher, exige-se interseção com a vulnerabilidade.</p>
                  </div>
                  <div className="flex items-center gap-2 text-sm">
                    <Switch
                      checked={form.onStatusChange}
                      onCheckedChange={(v) => setF({ onStatusChange: v })}
                      aria-label="Receber mudanças de status"
                    />
                    Enviar também quando o status de tratamento da vulnerabilidade mudar (apenas modo tempo real)
                  </div>
                </div>
              </div>

              <div className="flex items-center gap-2 text-sm">
                <Switch checked={form.enabled} onCheckedChange={(v) => setF({ enabled: v })} aria-label="Ativar" />
                Ativar este canal
              </div>
            </div>

            <div className="flex gap-2 pt-2 pb-6">
              <Button onClick={saveForm} disabled={saving}>
                {editing ? "Salvar" : "Adicionar"}
              </Button>
              {editing && (
                <Button variant="outline" onClick={testChannel} disabled={testing}>
                  <SendIcon /> Enviar mensagem de teste
                </Button>
              )}
            </div>
          </div>
        </SheetContent>
      </Sheet>
    </div>
  );
}
