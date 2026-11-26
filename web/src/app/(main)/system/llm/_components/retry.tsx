"use client";

// Componente compartilhado da configuração de retry do LLM: "quantidade + intervalo" de cada uma das cinco camadas de retry.
//
// As cinco camadas, de dentro para fora: conexão (SDK) → resposta vazia (SDK) → janela segura do mesmo provider → circuit breaker do round-robin → reexecução da intenção.
// As três primeiras acompanham o endpoint, então cada configuração de modelo pode sobrescrever o padrão global; as duas últimas são de nível de processo, só existe uma global.
//
// Todas as entradas seguem a mesma semântica "vazio = não configurado", alinhada ao db.RetryRule do backend:
//   quantidade   vazio/0 = usa o padrão embutido | -1 = desliga o retry desta camada | >0 = usa essa quantidade
//   intervalo    vazio/0 = usa o backoff exponencial original da camada | >0 = passa a usar este intervalo fixo em milissegundos

import * as React from "react";

import { Loader2Icon, SaveIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import type { LLMRetryOverride, LLMRetryPolicy, LLMRetryRule } from "@/lib/types";

export const ZERO_RULE: LLMRetryRule = { attempts: 0, interval_ms: 0 };
export const ZERO_OVERRIDE: LLMRetryOverride = {
  connect: ZERO_RULE,
  empty: ZERO_RULE,
  stream: ZERO_RULE,
};
const ZERO_POLICY: LLMRetryPolicy = {
  ...ZERO_OVERRIDE,
  breaker: ZERO_RULE,
  intent: ZERO_RULE,
};

type LayerMeta = {
  title: string;
  /** Onde este retry acontece e quem o executa */
  where: string;
  /** Que tipo de erro chega a esta camada — detalhado até o código de status, para ninguém ter de adivinhar */
  trigger: string;
  /** Erros parecidos que [não] passam por esta camada, para quem preencheu e não viu efeito não achar que é bug */
  skips?: string;
  desc: string;
  attemptsLabel: string;
  /** Valor padrão da quantidade quando vazia, usado como placeholder */
  defAttempts: number;
  /** Estratégia padrão do intervalo quando vazio, usada como placeholder */
  defInterval: string;
  /** Significado de preencher a quantidade com -1 */
  offHint: string;
};

export const RETRY_LAYERS = {
  connect: {
    title: "Retry de conexão",
    where: "SDK · antes de receber o 200",
    trigger:
      "Não conectou ou ainda não recebeu o 200: reset de conexão / timeout de leitura e escrita / falha de DNS e outros erros de camada de rede, além de HTTP 408, 429, 500, 502, 503, 504.",
    skips: "Os demais códigos de status (400 / 401 / 403 / 404 / 413 / 422 etc.) são recusas determinísticas; reenviar falha do mesmo jeito, então o erro é repassado direto para cima.",
    desc: "Reenvia a mesma requisição tal como estava. Depois que o stream começa (o 200 já foi recebido), uma queda no meio do caminho deixa de ser responsabilidade desta camada.",
    attemptsLabel: "Quantidade de retries",
    defAttempts: 3,
    defInterval: "0.5s→1s→2s exponencial (teto de 8s)",
    offHint: "-1 = nenhum retry, a falha é repassada imediatamente",
  },
  empty: {
    title: "Retry de resposta vazia",
    where: "SDK · apenas formato openai",
    trigger:
      "HTTP 200, finish_reason com stop normal, mas a resposta inteira não tem nenhum bloco de conteúdo — frames vazios do gateway, perda de frame no campo de raciocínio e soluços de amostragem têm essa aparência.",
    skips: "Não conta o caso sem conteúdo por truncamento de max_tokens (isso se resolve aumentando o limite de saída; reenviar só bate no limite de novo).",
    desc: "Reenvia o prompt inteiro, por isso é caro em contextos longos; não convém dar uma quantidade grande.",
    attemptsLabel: "Quantidade de retries",
    defAttempts: 2,
    defInterval: "0.5s→1s→2s exponencial (teto de 8s)",
    offHint: "-1 = a resposta vazia é entregue como está",
  },
  stream: {
    title: "Retry na janela segura do mesmo provider",
    where: "Este projeto · antes de entregar qualquer saída",
    trigger:
      "O problema só aparece depois que o stream foi estabelecido (o 200 já foi recebido): conexão cai no meio, provedor overloaded, eventos de erro 429 / 5xx dentro do stream — e nenhum token ainda foi entregue ao chamador.",
    skips:
      "Cota esgotada (402 / insufficient_quota, entregue ao round-robin para trocar de configuração), contexto longo demais (413 / context length, entregue à compressão), recusas determinísticas 400 / 401 / 403 / 404 / 422: nenhuma sofre retry.",
    desc: "Reexecuta a mesma requisição na mesma configuração. Como nenhuma saída foi entregue ainda, o replay não duplica a saída do modelo nem a execução de ferramentas.",
    attemptsLabel: "Quantidade de retries",
    defAttempts: 2,
    defInterval: "0.5s→1s exponencial (teto de 4s)",
    offHint: "-1 = a queda do stream vai direto para a reexecução da intenção, na camada externa",
  },
  breaker: {
    title: "Circuit breaker do round-robin",
    where: "Este projeto · nível de processo, uma só global",
    trigger:
      "Falhas transitórias (429, 5xx, erros de rede) acumuladas consecutivamente até o limiar acionam o circuit breaker; falhas determinísticas como saldo insuficiente (402), chave inválida (401 / 403) e modelo inexistente (404) ignoram o limiar e acionam o breaker na primeira vez.",
    skips: "Um sucesso zera a contagem, então uma configuração que só falha de vez em quando não vai acumulando até disparar o breaker.",
    desc: "Depois do breaker, entra em resfriamento; durante o resfriamento o round-robin pula esta configuração. O estado é gravado no banco e não se perde ao reiniciar.",
    attemptsLabel: "Quantas falhas consecutivas acionam o breaker",
    defAttempts: 3,
    defInterval: "1min→5min→30min em degraus",
    offHint: "-1 = falhas transitórias nunca acionam o breaker (falhas determinísticas ainda acionam)",
  },
  intent: {
    title: "Reexecução da intenção",
    where: "Este projeto · nível de processo, uma só global",
    trigger:
      "As camadas anteriores não seguraram: o worker terminou com model_error — os retries internos se esgotaram, ou o stream caiu depois de já ter começado a entregar saída (nesse ponto o replay não é seguro e só resta refazer tudo).",
    skips: "Cota esgotada já é tratada pelo round-robin trocando de configuração, não é reexecutada aqui; quando a tarefa é pausada / encerrada / entra na finalização, cede imediatamente, sem ocupar tempo de backoff.",
    desc: "A intenção inteira é executada de novo do início. É a camada mais externa; uma reexecução significa que as quantidades das camadas internas se multiplicam mais uma vez.",
    attemptsLabel: "Quantidade de reexecuções",
    defAttempts: 2,
    defInterval: "Fixo 3s",
    offHint: "-1 = não reexecuta, a intenção é julgada blocked diretamente",
  },
} satisfies Record<string, LayerMeta>;

type LayerKey = keyof typeof RETRY_LAYERS;

/** Milissegundos em linguagem humana, usado apenas para ecoar ao lado da caixa de entrada, para ninguém contar zeros. */
function humanMs(ms: number) {
  if (!Number.isFinite(ms) || ms <= 0) return "";
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${Number((ms / 1000).toFixed(2))}s`;
  return `${Number((ms / 60_000).toFixed(2))}min`;
}

/** Entrada numérica controlada: string vazia ↔ 0; estados intermediários ("-", "1e") ficam como estão localmente, sem incomodar o componente pai. */
function NumField({
  id,
  value,
  onChange,
  placeholder,
  min,
}: {
  id: string;
  value: number;
  onChange: (n: number) => void;
  placeholder: string;
  min: number;
}) {
  const [text, setText] = React.useState(value === 0 ? "" : String(value));
  // Acompanha quando o pai troca um conjunto inteiro de valores (ao ler a política, ao trocar de configuração); ao digitar, não passa por aqui,
  // porque nesse momento value já é igual ao resultado do parse do texto local.
  React.useEffect(() => {
    const incoming = value === 0 ? "" : String(value);
    setText((cur) => (Number(cur || 0) === value ? cur : incoming));
  }, [value]);
  return (
    <Input
      id={id}
      type="number"
      min={min}
      className="w-28 shrink-0"
      value={text}
      placeholder={placeholder}
      onChange={(e) => {
        setText(e.target.value);
        const n = Number(e.target.value);
        onChange(e.target.value.trim() === "" || !Number.isFinite(n) ? 0 : Math.trunc(n));
      }}
    />
  );
}

/** Os dois botões de ajuste de uma camada de retry. idPrefix serve para preservar o htmlFor do label quando aparece várias vezes na mesma página. */
export function RetryRuleFields({
  layer,
  idPrefix,
  value,
  onChange,
  compact,
}: {
  layer: LayerKey;
  idPrefix: string;
  value: LLMRetryRule;
  onChange: (r: LLMRetryRule) => void;
  /** true = versão compacta dentro da gaveta de configuração: omite a explicação expandida e mantém só a frase "que erros chegam a esta camada" */
  compact?: boolean;
}) {
  const meta = RETRY_LAYERS[layer];
  const human = humanMs(value.interval_ms);
  return (
    <div className={compact ? "grid gap-2" : "grid gap-3 rounded-lg border p-3"}>
      <div className="grid gap-0.5">
        <div className="flex flex-wrap items-baseline gap-2">
          <Label className="text-sm">{meta.title}</Label>
          <span className="text-muted-foreground text-xs">{meta.where}</span>
        </div>
        {/* Quais erros chegam a esta camada, detalhado até o código de status — se você preencheu o ajuste e não vê efeito, provavelmente o erro nem cai nesta camada. */}
        <p className="text-muted-foreground text-xs">
          <span className="font-medium text-foreground">Gatilho</span>: {meta.trigger}
        </p>
        {!compact && meta.skips && (
          <p className="text-muted-foreground text-xs">
            <span className="font-medium text-foreground">Não passa por esta camada</span>: {meta.skips}
          </p>
        )}
        {!compact && <p className="text-muted-foreground text-xs">{meta.desc}</p>}
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="flex items-center gap-2">
          <Label htmlFor={`${idPrefix}-${layer}-n`} className="text-muted-foreground text-xs">
            {meta.attemptsLabel}
          </Label>
          <NumField
            id={`${idPrefix}-${layer}-n`}
            min={-1}
            value={value.attempts}
            placeholder={`Padrão ${meta.defAttempts}`}
            onChange={(n) => onChange({ ...value, attempts: n })}
          />
        </div>
        <div className="flex items-center gap-2">
          <Label htmlFor={`${idPrefix}-${layer}-ms`} className="text-muted-foreground text-xs">
            Intervalo ms
          </Label>
          <NumField
            id={`${idPrefix}-${layer}-ms`}
            min={0}
            value={value.interval_ms}
            placeholder="Backoff padrão"
            onChange={(n) => onChange({ ...value, interval_ms: n })}
          />
          <span className="text-muted-foreground text-xs">{human ? `fixo ${human}` : meta.defInterval}</span>
        </div>
      </div>
      {!compact && <p className="text-muted-foreground text-xs">Vazio = usa o padrão; {meta.offHint}.</p>}
    </div>
  );
}

/** As três camadas de sobrescrita na gaveta de configuração do modelo (as três que acompanham o endpoint). */
export function ProfileRetryFields({
  value,
  onChange,
}: {
  value: LLMRetryOverride;
  onChange: (o: LLMRetryOverride) => void;
}) {
  return (
    <div className="grid gap-3 rounded-lg border p-3">
      <div className="grid gap-0.5">
        <Label className="text-sm">Sobrescrita de retry</Label>
        <p className="text-muted-foreground text-xs">
          Vale só para esta configuração e sobrescreve o padrão global em &quot;Retry e backoff&quot;. Cada campo vazio = segue o global; quantidade -1 = desliga o retry desta camada;
          intervalo preenchido usa o intervalo fixo no lugar do backoff exponencial. O circuit breaker e a reexecução da intenção são de nível de processo e só podem ser ajustados na página global.
        </p>
      </div>
      {(["connect", "empty", "stream"] as const).map((k) => (
        <div key={k} className="border-t pt-3 first:border-t-0 first:pt-0">
          <RetryRuleFields
            compact
            layer={k}
            idPrefix="pf"
            value={value[k]}
            onChange={(r) => onChange({ ...value, [k]: r })}
          />
        </div>
      ))}
    </div>
  );
}

/** Aba "Retry e backoff": valores padrão globais das cinco camadas. */
export function RetryPolicyPanel() {
  const [policy, setPolicy] = React.useState<LLMRetryPolicy>(ZERO_POLICY);
  const [loading, setLoading] = React.useState(true);
  const [saving, setSaving] = React.useState(false);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const p = await api.llmRetryPolicy();
      setPolicy({ ...ZERO_POLICY, ...p });
    } catch (e) {
      toast.error(`Falha ao ler a política de retry: ${(e as Error).message}`);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  async function save() {
    if (saving) return;
    setSaving(true);
    try {
      // O backend limita valores fora do intervalo e os devolve; usa-se o valor devolvido para atualizar, assim o que se vê é o que foi salvo.
      const saved = await api.saveLLMRetryPolicy(policy);
      setPolicy({ ...ZERO_POLICY, ...saved });
      toast.success("Salvo, vale imediatamente (a rodada de chamadas em andamento ainda usa os parâmetros antigos)");
    } catch (e) {
      toast.error(`Falha ao salvar: ${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  const set = (k: LayerKey) => (r: LLMRetryRule) => setPolicy((p) => ({ ...p, [k]: r }));

  if (loading) {
    return (
      <div className="flex items-center gap-2 rounded-lg border border-dashed p-10 text-muted-foreground text-sm">
        <Loader2Icon className="size-4 animate-spin" /> Lendo a política de retry…
      </div>
    );
  }

  return (
    <div className="grid gap-4">
      <div className="rounded-lg border bg-muted/30 p-3 text-muted-foreground text-xs leading-relaxed">
        A falha de uma chamada de modelo passa sucessivamente pelas cinco camadas de retry, de dentro para fora:
        <span className="text-foreground"> conexão → resposta vazia → janela segura do mesmo provider → circuit breaker do round-robin → reexecução da intenção</span>
        . A camada externa só entra depois que a interna se esgota, então as quantidades se
        <span className="text-foreground">multiplicam</span>
        : com todas as camadas no máximo, um único solavanco pode queimar dezenas de requisições.
        Deixar tudo vazio equivale aos valores padrão atuais, exatamente o mesmo comportamento de quando esta página não existia. As três primeiras camadas podem ser sobrescritas individualmente em cada configuração de modelo.
      </div>

      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {(Object.keys(RETRY_LAYERS) as LayerKey[]).map((k) => (
          <RetryRuleFields key={k} layer={k} idPrefix="gl" value={policy[k]} onChange={set(k)} />
        ))}
      </div>

      <div className="flex gap-2">
        <Button onClick={save} disabled={saving}>
          {saving ? <Loader2Icon className="animate-spin" /> : <SaveIcon />}
          Salvar
        </Button>
        <Button variant="outline" onClick={() => setPolicy(ZERO_POLICY)} disabled={saving}>
          Restaurar todos os padrões
        </Button>
      </div>
    </div>
  );
}
