"use client";

import * as React from "react";

import {
  Loader2Icon,
  PlugZapIcon,
  PlusIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  SaveIcon,
  StarIcon,
  Trash2Icon,
  ZapIcon,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import type { LLMPoolMember, LLMPoolStatus, LLMProfile, LLMRetryOverride } from "@/lib/types";
import { cn } from "@/lib/utils";

import { ProfileRetryFields, RetryPolicyPanel, ZERO_OVERRIDE } from "./_components/retry";

// O interruptor de raciocínio (thinking.type) e a intensidade de raciocínio (reasoning_effort) são dois campos [independentes entre si],
// configurados cada um por conta própria — algumas APIs não têm o campo thinking e ativam o raciocínio só pelo parâmetro de intensidade, por isso é preciso desacoplá-los.
// String vazia no banco = o campo [não é enviado]; o Select do Radix não aceita value vazio, então a UI usa a sentinela "none"
// para "não enviar", convertida de/para "" ao salvar e ler (NONE / fromStore / toStore).
const NONE = "none";
const fromStore = (v?: string) => (v ? v : NONE);
const toStore = (v: string) => (v === NONE ? "" : v);
const THINKING_TYPES: { value: string; label: string }[] = [
  { value: NONE, label: "Não enviar (padrão)" },
  { value: "disabled", label: "Desligado" },
  { value: "enabled", label: "Ligado" },
];
// Qual nome de campo da requisição é usado para o limite de saída (só faz sentido no formato openai). NONE ↔ "" usa a mesma conversão por sentinela.
const MAX_TOKENS_FIELDS: { value: string; label: string }[] = [
  { value: NONE, label: "max_tokens (padrão)" },
  { value: "max_completion_tokens", label: "max_completion_tokens" },
];
// Os outros dois formatos já têm o nome do campo fixado, e a opção não tem sentido para eles; o texto explicativo já deixa isso claro.
const MAX_TOKENS_FIELD_HINTS: Record<string, string> = {
  openai:
    "Qual chave carrega o limite. max_tokens é o padrão, e a grande maioria dos gateways compatíveis só reconhece esta; os modelos de raciocínio oficiais da OpenAI (série o / GPT-5), ao contrário, só reconhecem max_completion_tokens, e ao receber max_tokens dão diretamente unsupported_parameter.",
  anthropic: "Selecionável apenas no formato openai. O nome do campo da Anthropic é fixo: max_tokens.",
  "openai-responses": "Selecionável apenas no formato openai. O nome do campo da Responses API é fixo: max_output_tokens.",
};
const EFFORT_LEVELS: { value: string; label: string }[] = [
  { value: NONE, label: "Não enviar (padrão)" },
  { value: "low", label: "low" },
  { value: "medium", label: "medium" },
  { value: "high", label: "high" },
  { value: "xhigh", label: "xhigh" },
  { value: "max", label: "max" },
];

function cooldownText(secs: number) {
  if (secs <= 0) return "";
  if (secs < 60) return `${secs}s`;
  return `${Math.ceil(secs / 60)}min`;
}

// O "está tudo bem?" de uma configuração exibido no card. Uma configuração sem Key não consegue enviar requisição nenhuma, e isso deve ser dito antes do circuit breaker;
// os demais estados vêm do registro de circuit breaker do round-robin (com o round-robin desligado não são gerados novos registros; aí "Normal" = nenhuma falha conhecida).
type Health = { label: string; cls: string; hint?: string };
function healthOf(p: LLMProfile, m?: LLMPoolMember): Health {
  if (!p.api_key_hint) {
    return {
      label: "Key não configurada",
      cls: "border-muted-foreground/40 text-muted-foreground",
      hint: "API Key não preenchida, não é possível chamar",
    };
  }
  if (m?.state === "tripped") {
    return {
      label: m.cooldown_secs > 0 ? `Breaker acionado · ${cooldownText(m.cooldown_secs)}` : "Breaker acionado",
      cls: "border-destructive/50 text-destructive",
      hint: m.last_error,
    };
  }
  if (m?.state === "degraded") {
    return {
      label: `Anormal · ${m.fails} falhas`,
      cls: "border-amber-500/50 text-amber-600 dark:text-amber-400",
      hint: m.last_error,
    };
  }
  return { label: "Normal", cls: "border-emerald-500/50 text-emerald-600 dark:text-emerald-400" };
}

// ─────────────────────────────────────────────────────────────────────────────
// Gaveta de configuração do round-robin
// ─────────────────────────────────────────────────────────────────────────────

function PoolSheet({
  open,
  onOpenChange,
  pool,
  onReload,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  pool: LLMPoolStatus | null;
  onReload: () => Promise<void>;
}) {
  const [busy, setBusy] = React.useState(false);

  // A contagem regressiva do resfriamento é o número de segundos restantes calculado pelo backend — só se faz polling quando a gaveta está aberta e há configuração anormal, para fazê-la andar.
  React.useEffect(() => {
    if (!open || !pool?.enabled || !pool.chain.some((m) => m.state !== "ok")) return;
    const t = setInterval(() => void onReload(), 10_000);
    return () => clearInterval(t);
  }, [open, pool, onReload]);

  async function toggle(patch: { llm_pool_enabled?: boolean; llm_pool_bind_fallback?: boolean }) {
    if (busy) return;
    setBusy(true);
    try {
      await api.setSettings(patch);
      await onReload();
      if (patch.llm_pool_enabled !== undefined) {
        toast.success(patch.llm_pool_enabled ? "Round-robin de LLM ativado" : "Round-robin de LLM desativado");
      } else {
        toast.success("Configurações de fallback atualizadas");
      }
    } catch (e) {
      toast.error(`Falha ao configurar: ${(e as Error).message}`);
    } finally {
      setBusy(false);
    }
  }

  async function recover(id?: string) {
    try {
      await api.resetLLMPool(id);
      await onReload();
      toast.success(id ? "Esta configuração foi restaurada" : "Todas as configurações foram restauradas");
    } catch (e) {
      toast.error(`Falha ao restaurar: ${(e as Error).message}`);
    }
  }

  const enabled = pool?.enabled ?? false;
  const chain = pool?.chain ?? [];
  // Membros que participam do round-robin (exclui os marcados como "não participa do round-robin"); a ordem é a ordem real de tentativa do backend.
  const inChain = chain.filter((m) => m.active || !m.excluded);
  const tripped = chain.filter((m) => m.state === "tripped");

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="flex flex-col gap-0 p-0 data-[side=right]:sm:max-w-lg">
        <SheetHeader className="px-4">
          <SheetTitle className="flex items-center gap-2">
            <ZapIcon className="size-4" /> Round-robin de LLM · failover
          </SheetTitle>
          <SheetDescription>
            Quando ativado, os Agents <b>sem modelo especificado</b> trocam automaticamente para a próxima configuração quando a atual fica indisponível (saldo insuficiente / Key inválida / limite de taxa /
            falha do serviço).
          </SheetDescription>
        </SheetHeader>

        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-6">
          <div className="flex items-center justify-between gap-4 rounded-lg border p-3">
            <div className="grid gap-0.5">
              <Label className="text-sm">Ativar round-robin</Label>
              <p className="text-muted-foreground text-xs">Desativado por padrão. Desativado, só a configuração ativa é usada e falha é falha.</p>
            </div>
            <Switch
              checked={enabled}
              disabled={busy}
              onCheckedChange={(v) => void toggle({ llm_pool_enabled: v })}
              aria-label="Chave do round-robin de LLM"
            />
          </div>

          {enabled && (
            <>
              <div className="flex items-center justify-between gap-4 rounded-lg border p-3">
                <div className="grid gap-0.5">
                  <Label className="text-sm">Fazer fallback também quando o modelo especificado falhar</Label>
                  <p className="text-muted-foreground text-xs">
                    Desativado por padrão: se o Agent ou a tarefa especificou uma configuração, só ela é usada e falha é falha (não troca em silêncio por outro modelo).
                    Quando ativado, se a configuração especificada falhar, também recai na cadeia de round-robin abaixo.
                  </p>
                </div>
                <Switch
                  checked={pool?.bind_fallback ?? false}
                  disabled={busy}
                  onCheckedChange={(v) => void toggle({ llm_pool_bind_fallback: v })}
                  aria-label="Chave de fallback para configuração vinculada com falha"
                />
              </div>

              <Separator />

              <div className="grid gap-2">
                <div className="flex items-center justify-between">
                  <Label className="text-sm">Ordem do round-robin</Label>
                  {tripped.length > 0 && (
                    <Button size="sm" variant="ghost" onClick={() => void recover()}>
                      <RotateCcwIcon /> Restaurar todas
                    </Button>
                  )}
                </div>
                {inChain.length < 2 && (
                  <p className="text-muted-foreground text-xs">
                    No momento só há {inChain.length} configurações utilizáveis, então o round-robin não terá efeito — são necessárias pelo menos 2 configurações com API Key preenchida e participando do round-robin.
                  </p>
                )}
                {chain.map((m) => {
                  const excluded = m.excluded && !m.active;
                  const order = excluded ? null : inChain.findIndex((x) => x.profile_id === m.profile_id) + 1;
                  return (
                    <div
                      key={m.profile_id}
                      className={cn(
                        "grid gap-1 rounded-lg border p-2.5 text-sm",
                        excluded && "opacity-55",
                        m.state === "tripped" && "border-destructive/40",
                      )}
                    >
                      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                        <span className="w-5 shrink-0 text-center font-mono text-muted-foreground text-xs">
                          {order ?? "—"}
                        </span>
                        <span className="font-medium">{m.name}</span>
                        {m.active && (
                          <Badge variant="outline" className="border-amber-400/50 text-amber-500">
                            Ativa
                          </Badge>
                        )}
                        {excluded && <Badge variant="outline">Fora do round-robin</Badge>}
                        <div className="ml-auto flex items-center gap-2">
                          {m.state === "tripped" && m.cooldown_secs > 0 && (
                            <span className="text-muted-foreground text-xs">Resfriamento {cooldownText(m.cooldown_secs)}</span>
                          )}
                          {m.state === "degraded" && (
                            <span className="text-muted-foreground text-xs">Falhas consecutivas {m.fails}</span>
                          )}
                          {m.state !== "ok" && (
                            <Button
                              size="icon"
                              variant="ghost"
                              className="size-7"
                              aria-label="Restaurar agora"
                              title="Restaurar agora: limpa o circuit breaker, a próxima chamada tenta esta configuração de novo"
                              onClick={() => void recover(m.profile_id)}
                            >
                              <RotateCcwIcon className="size-3.5" />
                            </Button>
                          )}
                        </div>
                      </div>
                      <div className="flex flex-wrap items-center gap-x-3 pl-7 text-muted-foreground text-xs">
                        <code className="truncate font-mono">{m.model}</code>
                        {!m.active && <span>Prioridade {m.priority}</span>}
                      </div>
                      {m.last_error && (
                        <p className="truncate pl-7 font-mono text-muted-foreground text-xs" title={m.last_error}>
                          {m.last_error}
                        </p>
                      )}
                    </div>
                  );
                })}
                {chain.length === 0 && (
                  <div className="rounded-lg border border-dashed p-4 text-center text-muted-foreground text-sm">
                    Nenhuma configuração
                  </div>
                )}
              </div>

              <div className="rounded-lg border border-dashed p-3 text-muted-foreground text-xs leading-relaxed">
                A configuração ativa é sempre a 1ª na ordem, as demais por prioridade da maior para a menor (definida em cada configuração). Depois que uma configuração falha, entra em resfriamento (60s → 5min →
                30min), é pulada durante o resfriamento e volta automaticamente depois de se recuperar. Configurações cuja janela de contexto não comporta a requisição atual são puladas. Agents com modelo especificado
                e as tarefas por padrão não participam do round-robin.
              </div>
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Gaveta de configuração de modelo (criar / editar usam o mesmo formulário)
// ─────────────────────────────────────────────────────────────────────────────

function ProfileSheet({
  profile,
  open,
  onOpenChange,
  onSaved,
}: {
  profile: LLMProfile | null; // null = nova
  open: boolean;
  onOpenChange: (o: boolean) => void;
  onSaved: (id: string) => void;
}) {
  const isNew = !profile;
  const [name, setName] = React.useState("");
  const [format, setFormat] = React.useState<"anthropic" | "openai" | "openai-responses">("anthropic");
  const [model, setModel] = React.useState("");
  const [baseUrl, setBaseUrl] = React.useState("");
  const [proxy, setProxy] = React.useState("");
  const [apiKey, setApiKey] = React.useState("");
  const [keyHint, setKeyHint] = React.useState("");
  const [rps, setRps] = React.useState("0");
  const [rpm, setRpm] = React.useState("0");
  const [cw, setCw] = React.useState("0"); // janela de contexto (K tokens); 0 = padrão 200K
  const [thinkingType, setThinkingType] = React.useState(NONE);
  const [effort, setEffort] = React.useState(NONE);
  const [priority, setPriority] = React.useState("0"); // ordem no round-robin; quanto maior, mais cedo
  const [poolExclude, setPoolExclude] = React.useState(false);
  const [streaming, setStreaming] = React.useState(true); // true = streaming (padrão); false = sem streaming
  const [maxTokens, setMaxTokens] = React.useState("0"); // limite de saída por resposta; 0 = não enviar
  const [maxTokensField, setMaxTokensField] = React.useState(NONE); // qual nome de campo usar para o limite; NONE = max_tokens
  const [sessionHeaderKey, setSessionHeaderKey] = React.useState(""); // nome do header de sessão personalizado; vazio = não enviar
  const [retry, setRetry] = React.useState<LLMRetryOverride>(ZERO_OVERRIDE); // sobrescrita de retry desta configuração; tudo 0 = segue o global
  const [testing, setTesting] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [models, setModels] = React.useState<string[]>([]);
  const [loadingModels, setLoadingModels] = React.useState(false);
  const [modelsOpen, setModelsOpen] = React.useState(false);

  // A cada abertura, preenche o formulário a partir do profile recebido (em nova configuração, reseta para os valores padrão). Fechar e reabrir a gaveta
  // é um começo limpo, sem deixar vestígios da configuração anterior.
  React.useEffect(() => {
    if (!open) return;
    setName(profile?.name ?? "");
    setFormat(profile?.format === "openai" || profile?.format === "openai-responses" ? profile.format : "anthropic");
    setModel(profile?.model ?? "");
    setBaseUrl(profile?.base_url ?? "");
    setProxy(profile?.proxy ?? "");
    setRps(String(profile?.rate_per_second ?? 0));
    setRpm(String(profile?.rate_per_minute ?? 0));
    setCw(String(profile?.context_window_k ?? 0));
    setThinkingType(fromStore(profile?.thinking_type));
    setEffort(fromStore(profile?.reasoning_effort));
    setPriority(String(profile?.priority ?? 0));
    setPoolExclude(profile?.pool_exclude ?? false);
    setStreaming(profile?.streaming ?? true);
    setMaxTokens(String(profile?.max_tokens ?? 0));
    setMaxTokensField(fromStore(profile?.max_tokens_field));
    setSessionHeaderKey(profile?.session_header_key ?? "");
    setRetry(profile?.retry ?? ZERO_OVERRIDE);
    setApiKey("");
    setKeyHint(profile?.api_key_hint ?? "");
    setModels([]);
    setModelsOpen(false);
  }, [open, profile]);

  const profileId = profile ? Number(profile.id) : undefined;

  async function loadModels() {
    if (loadingModels) return;
    setLoadingModels(true);
    setModels([]);
    try {
      const r = await api.fetchLLMModels(format, baseUrl, apiKey, proxy, profileId);
      if (r.ok && r.models && r.models.length > 0) {
        setModels(r.models);
        setModelsOpen(true);
        toast.success(`${r.models.length} modelos carregados`);
      } else {
        toast.error(`Falha ao carregar modelos: ${r.error ?? "nenhum modelo obtido"}`);
      }
    } catch (e) {
      toast.error(`Erro ao carregar modelos: ${(e as Error).message}`);
    } finally {
      setLoadingModels(false);
    }
  }

  async function testConnection() {
    if (testing) return;
    setTesting(true);
    try {
      // Testa com os parâmetros de raciocínio que a configuração realmente usará, assim um modelo que não suporta o campo falha aqui mesmo,
      // em vez de explodir só na hora de rodar a tarefa. Passa o profile id: com a caixa da Key vazia, usa-se a Key já salva.
      const r = await api.testLLM(
        format,
        model,
        baseUrl,
        apiKey,
        proxy,
        toStore(thinkingType),
        toStore(effort),
        profileId,
        streaming,
        sessionHeaderKey.trim(),
      );
      // A resposta também é exibida: ver que o modelo de fato falou é o que faz equivaler a ter funcionado na sessão.
      if (r.ok)
        toast.success(`Conexão bem-sucedida · ${r.latency_ms ?? "?"}ms · ${r.model ?? model}`, {
          description: r.reply ? `Resposta: ${r.reply}` : undefined,
        });
      else toast.error(`Falha na conexão: ${r.error ?? "desconhecido"}`);
    } catch (e) {
      toast.error(`Erro no teste: ${(e as Error).message}`);
    } finally {
      setTesting(false);
    }
  }

  async function save() {
    if (!name.trim() || !model.trim()) {
      toast.error("Preencha o nome e o modelo");
      return;
    }
    if (saving) return;
    setSaving(true);
    try {
      const { id } = await api.saveLLMProfile({
        ...(profile ? { id: Number(profile.id) } : {}),
        name: name.trim(),
        format,
        model: model.trim(),
        base_url: baseUrl.trim(),
        proxy: proxy.trim(),
        api_key: apiKey,
        rate_per_second: Number(rps) || 0,
        rate_per_minute: Number(rpm) || 0,
        context_window_k: Number(cw) || 0,
        thinking_type: toStore(thinkingType),
        reasoning_effort: toStore(effort),
        priority: Number(priority) || 0,
        pool_exclude: poolExclude,
        streaming,
        max_tokens: Math.max(0, Number(maxTokens) || 0),
        // O interruptor do nome de campo só faz sentido para openai (Chat Completions); os outros formatos sempre voltam ao padrão;
        // o backend também faz a mesma normalização de novo, aqui é só para a UI não enviar valores contraditórios.
        max_tokens_field: format === "openai" ? toStore(maxTokensField) : "",
        session_header_key: sessionHeaderKey.trim(),
        retry,
      });
      if (isNew) toast.success(`Criada: ${name.trim()} (use "Definir como ativa" no card para habilitá-la)`);
      else toast.success(profile?.is_default ? "Salvo, a configuração ativa vale imediatamente, sem reiniciar" : "Salvo");
      onSaved(String(id));
      onOpenChange(false);
    } catch (e) {
      toast.error(`Falha ao salvar: ${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="flex flex-col gap-0 p-0 data-[side=right]:min-w-[420px] data-[side=right]:sm:max-w-xl"
      >
        <SheetHeader className="px-4">
          <SheetTitle className="flex items-center gap-2">
            {isNew ? "Nova configuração de modelo" : `Editar: ${profile?.name}`}
            {profile?.is_default && (
              <Badge variant="outline" className="border-amber-400/50 text-amber-500">
                Ativa
              </Badge>
            )}
          </SheetTitle>
          <SheetDescription>
            {isNew
              ? "Depois de criada, não é ativada automaticamente; use \"Definir como ativa\" no card para habilitá-la."
              : "Depois de alterar, clique em salvar; a configuração ativa, uma vez salva, vale imediatamente para todos os Agents."}
          </SheetDescription>
        </SheetHeader>

        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="grid gap-2">
              <Label htmlFor="p-name">Nome</Label>
              <Input
                id="p-name"
                placeholder="Ex.: OpenAI produção"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label>Formato</Label>
              <Select value={format} onValueChange={(v) => setFormat(v as "anthropic" | "openai" | "openai-responses")}>
                <SelectTrigger>
                  <SelectValue placeholder="Escolher formato" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="anthropic">Anthropic</SelectItem>
                  <SelectItem value="openai">OpenAI (Chat Completions)</SelectItem>
                  <SelectItem value="openai-responses">OpenAI (Responses API)</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-model">Modelo</Label>
            <div className="flex gap-2">
              <Input
                id="p-model"
                className="font-mono"
                placeholder="claude-opus-4-8"
                value={model}
                onChange={(e) => setModel(e.target.value)}
              />
              {/* modal: o conteúdo deste Popover é levado por portal para o <body>, fora do bloqueio de rolagem do Sheet;
                      sem modal a lista renderiza mas não rola. O modal faz com que ele próprio detenha o bloqueio de rolagem do topo. */}
              <Popover open={modelsOpen} onOpenChange={setModelsOpen} modal>
                <PopoverTrigger asChild>
                  <Button
                    type="button"
                    variant="outline"
                    size="icon"
                    className="shrink-0"
                    disabled={loadingModels}
                    onClick={loadModels}
                    title="Carregar modelos disponíveis da API"
                  >
                    {loadingModels ? <Loader2Icon className="animate-spin" /> : <RefreshCwIcon />}
                  </Button>
                </PopoverTrigger>
                {models.length > 0 && (
                  <PopoverContent className="max-h-72 w-72 gap-0 overflow-y-auto overscroll-contain p-1" align="end">
                    {models.map((m) => (
                      <button
                        key={m}
                        type="button"
                        className="w-full shrink-0 rounded-md px-2 py-1.5 text-left font-mono text-xs hover:bg-accent hover:text-accent-foreground"
                        onClick={() => {
                          setModel(m);
                          setModelsOpen(false);
                        }}
                      >
                        {m}
                      </button>
                    ))}
                  </PopoverContent>
                )}
              </Popover>
            </div>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-base-url">Base URL (opcional)</Label>
            <Input
              id="p-base-url"
              className="font-mono"
              placeholder="https://api.openai.com/v1"
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
            />
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-proxy">Proxy (opcional)</Label>
            <Input
              id="p-proxy"
              className="font-mono"
              placeholder="socks5://user:pass@127.0.0.1:1080 · http://127.0.0.1:8080"
              value={proxy}
              onChange={(e) => setProxy(e.target.value)}
            />
            <p className="text-muted-foreground text-xs">
              Só as requisições de saída do LLM passam por este proxy; suporta http/https/socks5, pode levar usuário e senha (como
              socks5://user:pass@host:port; senhas com caracteres especiais precisam de codificação URL); vazio = não usa proxy (conexão direta).
            </p>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-session-header">Header de sessão personalizado (opcional)</Label>
            <Input
              id="p-session-header"
              className="font-mono"
              placeholder="Ex.: x-session-id (vazio = não enviar)"
              value={sessionHeaderKey}
              onChange={(e) => setSessionHeaderKey(e.target.value)}
            />
            <p className="text-muted-foreground text-xs">
              Depois de preencher o nome do header, toda requisição leva esse header HTTP, com o valor preenchido automaticamente com o <b>session id da sessão atual</b> (sessão de chat como
              conv-12, worker como exp3-worker-i87). Serve para gateways que fazem cache de prompt / roteamento
              sticky por header de session-id; estável entre várias rodadas da mesma sessão e diferente entre sessões distintas. Vazio = não envia.
            </p>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-api-key">API Key</Label>
            <Input
              id="p-api-key"
              type="password"
              placeholder={keyHint ? `Definida (${keyHint}), deixe vazio para manter` : "sk-…"}
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
            />
          </div>

          <div className="grid gap-4 sm:grid-cols-3">
            <div className="grid gap-2">
              <Label htmlFor="p-rps">Limite por segundo</Label>
              <Input id="p-rps" type="number" min={0} value={rps} onChange={(e) => setRps(e.target.value)} />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="p-rpm">Limite por minuto</Label>
              <Input id="p-rpm" type="number" min={0} value={rpm} onChange={(e) => setRpm(e.target.value)} />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="p-cw">Janela de contexto (K)</Label>
              <Input
                id="p-cw"
                type="number"
                min={0}
                max={1000}
                value={cw}
                onChange={(e) => setCw(e.target.value)}
                placeholder="200"
              />
            </div>
          </div>
          <p className="-mt-2 text-muted-foreground text-xs">
            Limite 0 = sem limite, compartilhado por todos os Agents. A janela de contexto é em K (milhares de tokens), 0 = padrão 200K, máximo 1000 (ou seja,
            1M); um valor alto demais faz a compressão não disparar.
          </p>

          <div className="grid gap-3 rounded-lg border p-3">
            <div className="flex items-center justify-between gap-4">
              <div className="grid gap-0.5">
                <Label htmlFor="p-priority" className="text-sm">
                  Prioridade do round-robin
                </Label>
                <p className="text-muted-foreground text-xs">
                  Quanto maior o número, mais cedo é escolhida; a configuração ativa é sempre a 1ª na ordem, independentemente deste valor. Configurações de mesma prioridade se revezam na frente, repartindo naturalmente a cota.
                </p>
              </div>
              <Input
                id="p-priority"
                type="number"
                className="w-24 shrink-0"
                value={priority}
                onChange={(e) => setPriority(e.target.value)}
              />
            </div>
            <div className="flex items-center justify-between gap-4 border-t pt-3">
              <div className="grid gap-0.5">
                <Label className="text-sm">Fora do round-robin</Label>
                <p className="text-muted-foreground text-xs">
                  Quando ativado, não é usada como destino de failover (ainda pode ser especificada explicitamente por Agent / tarefa). Adequado para configurações caras que &quot;são dedicadas a um Agent
                  e que não se quer que outros queimem quando falharem&quot;.
                </p>
              </div>
              <Switch checked={poolExclude} onCheckedChange={setPoolExclude} aria-label="Fora do round-robin" />
            </div>
            <div className="flex items-center justify-between gap-4 border-t pt-3">
              <div className="grid gap-0.5">
                <Label className="text-sm">Saída em streaming · streaming</Label>
                <p className="text-muted-foreground text-xs">
                  Ativado (padrão) usa SSE em streaming, com progresso em tempo real durante a execução e contagem de tokens em tempo real. Desativado usa o modo realmente sem streaming (stream:false,
                  devolve a resposta completa de uma vez) — contorna implementações ruins de SSE de alguns gateways (frames vazios / perda de frame no campo de raciocínio),
                  ao custo de perder o progresso em tempo real durante a execução.
                </p>
              </div>
              <Switch checked={streaming} onCheckedChange={setStreaming} aria-label="Saída em streaming" />
            </div>
          </div>

          <div className="grid gap-3 rounded-lg border p-3">
            <div className="flex items-center justify-between gap-4">
              <div className="grid gap-0.5">
                <Label htmlFor="p-max-tokens" className="text-sm">
                  Limite de saída · max tokens
                </Label>
                <p className="text-muted-foreground text-xs">
                  Máximo de tokens gerados por resposta, enviado a cada requisição. 0 (padrão) = não envia o campo e o padrão do servidor decide.
                  Isto é diferente da &quot;janela de contexto&quot; acima: aquela é a capacidade total do modelo, usada só localmente para calcular o limiar de compressão.
                  Se for pequeno demais, um modelo de raciocínio é cortado já na fase de pensamento e não consegue produzir nem uma palavra da resposta.
                </p>
              </div>
              <Input
                id="p-max-tokens"
                type="number"
                min={0}
                className="w-28 shrink-0"
                value={maxTokens}
                onChange={(e) => setMaxTokens(e.target.value)}
                placeholder="0"
              />
            </div>
            <div className="flex items-center justify-between gap-4 border-t pt-3">
              <div className="grid gap-0.5">
                <Label className="text-sm">Nome do campo de limite</Label>
                <p className="text-muted-foreground text-xs">{MAX_TOKENS_FIELD_HINTS[format]}</p>
              </div>
              <Select
                value={format === "openai" ? maxTokensField : NONE}
                onValueChange={setMaxTokensField}
                disabled={format !== "openai"}
              >
                <SelectTrigger className="w-56 shrink-0">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {MAX_TOKENS_FIELDS.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="grid gap-3 rounded-lg border p-3">
            <div className="flex items-center justify-between gap-4">
              <div className="grid gap-0.5">
                <Label className="text-sm">Interruptor de raciocínio · thinking.type</Label>
                <p className="text-muted-foreground text-xs">
                  Controla se o campo thinking é enviado. Não enviar = sem o campo (compatível com modelos como MiniMax que não suportam); Desligado = envia
                  disabled; Ligado = envia enabled. É independente da intensidade abaixo.
                </p>
              </div>
              <Select value={thinkingType} onValueChange={setThinkingType}>
                <SelectTrigger className="w-32 shrink-0">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {THINKING_TYPES.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex items-center justify-between gap-4 border-t pt-3">
              <div className="grid gap-0.5">
                <Label className="text-sm">Intensidade de raciocínio · reasoning_effort</Label>
                <p className="text-muted-foreground text-xs">
                  Nível de intensidade independente (OpenAI reasoning_effort / Anthropic output_config.effort). Algumas APIs não têm o campo thinking
                  e ativam o raciocínio só pela intensidade, por isso pode ser configurado sozinho, sem enviar o interruptor de raciocínio.
                </p>
              </div>
              <Select value={effort} onValueChange={setEffort}>
                <SelectTrigger className="w-32 shrink-0">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {EFFORT_LEVELS.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <ProfileRetryFields value={retry} onChange={setRetry} />
        </div>

        <div className="flex gap-2 border-t px-4 py-3">
          <Button variant="outline" onClick={testConnection} disabled={testing}>
            {testing ? <Loader2Icon className="animate-spin" /> : <PlugZapIcon />}
            {testing ? "Testando…" : "Testar conexão"}
          </Button>
          <Button onClick={save} disabled={saving} className="flex-1">
            {saving && <Loader2Icon className="animate-spin" />}
            {!saving && (isNew ? <PlusIcon /> : <SaveIcon />)}
            {isNew ? "Criar" : "Salvar"}
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  );
}

// ─────────────────────────────────────────────────────────────────────────────

export default function LLMPage() {
  const [profiles, setProfiles] = React.useState<LLMProfile[]>([]);
  const [pool, setPool] = React.useState<LLMPoolStatus | null>(null);
  const [poolOpen, setPoolOpen] = React.useState(false);
  // O estado de abertura e o conteúdo da gaveta são guardados separadamente: ao fechar, editing permanece inalterado, senão durante a animação de fechamento o título
  // passaria de "Editar X" para "Nova". editing = null significa nova.
  const [editOpen, setEditOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<LLMProfile | null>(null);
  const openEditor = React.useCallback((p: LLMProfile | null) => {
    setEditing(p);
    setEditOpen(true);
  }, []);

  const loadPool = React.useCallback(async () => {
    try {
      setPool(await api.llmPool());
    } catch {
      /* ignore */
    }
  }, []);

  const load = React.useCallback(async () => {
    try {
      setProfiles(await api.llmProfiles());
    } catch {
      /* ignore */
    }
    await loadPool();
  }, [loadPool]);

  React.useEffect(() => {
    void load();
  }, [load]);

  // O selo de saúde no card pega o estado do round-robin pelo profile id.
  const health = React.useMemo(() => {
    const m = new Map<string, LLMPoolMember>();
    for (const c of pool?.chain ?? []) m.set(c.profile_id, c);
    return m;
  }, [pool]);

  async function activate(id: string, name: string) {
    try {
      await api.activateLLMProfile(id);
      toast.success(`Ativada: ${name}`);
      await load();
    } catch (e) {
      toast.error(`Falha ao ativar: ${(e as Error).message}`);
    }
  }

  async function remove(p: LLMProfile) {
    if (p.is_default) {
      toast.error("Não é possível excluir a configuração atualmente ativa");
      return;
    }
    try {
      await api.deleteLLMProfile(p.id);
      toast.success(`Excluída: ${p.name}`);
      await load();
    } catch (e) {
      toast.error(`Falha ao excluir: ${(e as Error).message}`);
    }
  }

  const poolOn = pool?.enabled ?? false;

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="font-semibold text-xl tracking-tight">LLM</h1>
          <p className="text-muted-foreground text-sm">
            Configurações de formato / modelo / limite de taxa compartilhadas por todos os Agents. Clique no card para editar; a estrela marca a configuração ativa no momento.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => setPoolOpen(true)}>
            <ZapIcon /> Configuração do round-robin
            {poolOn && (
              <Badge variant="outline" className="ml-1 border-emerald-500/50 text-emerald-600 dark:text-emerald-400">
                Ativado
              </Badge>
            )}
          </Button>
          <Button size="sm" variant="outline" onClick={() => openEditor(null)}>
            <PlusIcon /> Novo
          </Button>
        </div>
      </div>

      <Tabs defaultValue="profiles" className="flex-1">
        <TabsList>
          <TabsTrigger value="profiles">Configurações de modelo</TabsTrigger>
          <TabsTrigger value="retry">Retry e backoff</TabsTrigger>
        </TabsList>

        <TabsContent value="profiles" className="mt-4">
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
            {profiles.map((p) => {
              const h = healthOf(p, health.get(p.id));
              return (
                // biome-ignore lint/a11y/useSemanticElements: o card contém seus próprios botões de ação, e usar um <button> nativo causaria botões aninhados (HTML inválido)
                <Card
                  key={p.id}
                  role="button"
                  tabIndex={0}
                  onClick={() => openEditor(p)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      openEditor(p);
                    }
                  }}
                  className={cn(
                    "cursor-pointer gap-0 py-4 outline-none transition-colors hover:border-foreground/30",
                    p.is_default && "border-amber-400/50 bg-amber-400/5",
                  )}
                >
                  <CardContent className="grid gap-2 px-4">
                    <div className="flex items-start gap-2">
                      <StarIcon
                        className={cn(
                          "mt-0.5 size-4 shrink-0",
                          p.is_default ? "fill-amber-400 text-amber-400" : "text-muted-foreground",
                        )}
                      />
                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="truncate font-medium text-sm">{p.name}</span>
                          <Badge variant="outline" className="uppercase">
                            {p.format}
                          </Badge>
                          <Badge variant="outline" className={cn("ml-auto", h.cls)} title={h.hint}>
                            {h.label}
                          </Badge>
                        </div>
                        <code className="mt-1 block truncate font-mono text-muted-foreground text-xs">{p.model}</code>
                      </div>
                    </div>

                    <div className="flex flex-wrap gap-x-3 gap-y-0.5 pl-6 text-muted-foreground text-xs">
                      {p.api_key_hint && <span>{p.api_key_hint}</span>}
                      <span>
                        {p.rate_per_second}/s · {p.rate_per_minute}/min
                      </span>
                      {p.proxy && <span className="truncate">Proxy {p.proxy}</span>}
                      {p.reasoning_effort && (
                        <span>Raciocínio {p.reasoning_effort === "off" ? "off" : p.reasoning_effort}</span>
                      )}
                      {/* Os dois campos relacionados ao round-robin só fazem sentido quando o round-robin está ligado; desligado, não ocupam espaço */}
                      {poolOn &&
                        !p.is_default &&
                        (p.pool_exclude ? <span>Fora do round-robin</span> : <span>Prioridade {p.priority ?? 0}</span>)}
                    </div>

                    <div className="mt-1 flex gap-2">
                      <Button
                        size="sm"
                        variant="outline"
                        className="flex-1"
                        disabled={p.is_default}
                        onClick={(e) => {
                          e.stopPropagation();
                          void activate(p.id, p.name);
                        }}
                      >
                        {p.is_default ? "Ativa" : "Definir como ativa"}
                      </Button>
                      <Button
                        size="icon"
                        variant="outline"
                        aria-label="Excluir configuração"
                        onClick={(e) => {
                          e.stopPropagation();
                          void remove(p);
                        }}
                      >
                        <Trash2Icon className="text-destructive" />
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              );
            })}
            {profiles.length === 0 && (
              <div className="col-span-full rounded-lg border border-dashed p-10 text-center text-muted-foreground text-sm">
                Ainda não há configurações de modelo; clique em &quot;Novo&quot; no canto superior direito para criar a primeira.
              </div>
            )}
          </div>
        </TabsContent>

        <TabsContent value="retry" className="mt-4">
          <RetryPolicyPanel />
        </TabsContent>
      </Tabs>

      <ProfileSheet profile={editing} open={editOpen} onOpenChange={setEditOpen} onSaved={() => void load()} />
      <PoolSheet open={poolOpen} onOpenChange={setPoolOpen} pool={pool} onReload={loadPool} />
    </div>
  );
}
