"use client";

import * as React from "react";

import { CpuIcon, FlaskConicalIcon, KeyboardIcon, RadioTowerIcon, SearchIcon, ShieldAlertIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import { CHAT_SEND_MODE_OPTIONS, type ChatSendMode, setChatSendMode, useChatSendMode } from "@/lib/chat-send-mode";
import type { Settings } from "@/lib/types";

import { UpdateCard } from "./_components/update-card";

export default function SystemSettingsPage() {
  const [trafficCapture, setTrafficCapture] = React.useState(false);
  const [agentTrafficBinding, setAgentTrafficBinding] = React.useState(false);
  const [webSearch, setWebSearch] = React.useState(false);
  const [backend, setBackend] = React.useState("ddgs");
  const [braveKeySet, setBraveKeySet] = React.useState(false);
  const [braveKeyInput, setBraveKeyInput] = React.useState("");
  const [tavilyKeySet, setTavilyKeySet] = React.useState(false);
  const [tavilyKeyInput, setTavilyKeyInput] = React.useState("");
  const [savingTavilyKey, setSavingTavilyKey] = React.useState(false);
  const [proxyInput, setProxyInput] = React.useState("");
  const [savingProxy, setSavingProxy] = React.useState(false);
  const [globalProxyInput, setGlobalProxyInput] = React.useState("");
  const [savingGlobalProxy, setSavingGlobalProxy] = React.useState(false);
  const [testing, setTesting] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [savingKey, setSavingKey] = React.useState(false);
  const [pyInterp, setPyInterp] = React.useState("");
  const [workers, setWorkers] = React.useState("3");
  const [savingWorkers, setSavingWorkers] = React.useState(false);
  // Escopo de injeção das restrições operacionais (por padrão, todos ativados).
  const [injectPlanner, setInjectPlanner] = React.useState(true);
  const [injectWorker, setInjectWorker] = React.useState(true);
  // Recurso experimental: compressão de contexto noa (desativado por padrão).
  const [noaCompaction, setNoaCompaction] = React.useState(false);
  // Preferência puramente de frontend: não passa por /api/settings, lê e grava direto no localStorage.
  const sendMode = useChatSendMode();

  const apply = React.useCallback((s: Settings) => {
    setTrafficCapture(!!s.traffic_capture);
    setAgentTrafficBinding(!!s.agent_traffic_binding);
    setWebSearch(!!s.web_search_enabled);
    setBackend(s.web_search_backend || "ddgs");
    setBraveKeySet(!!s.brave_key_set);
    setTavilyKeySet(!!s.tavily_key_set);
    setProxyInput(s.web_search_proxy ?? "");
    setGlobalProxyInput(s.global_proxy ?? "");
    setPyInterp(s.python_interpreter ?? "");
    setWorkers(String(s.workers ?? 3));
    setInjectPlanner(s.constraints_inject_planner !== false);
    setInjectWorker(s.constraints_inject_worker !== false);
    setNoaCompaction(!!s.noa_compaction);
  }, []);

  const saveWorkers = () => {
    const n = Number(workers);
    if (!Number.isInteger(n) || n <= 0) {
      toast.error("A concorrência deve ser um inteiro maior que 0");
      return;
    }
    setSavingWorkers(true);
    api
      .setSettings({ workers: n })
      .then((s) => {
        apply(s);
        toast.success("Número de agents de trabalho concorrentes salvo (vale para as tarefas iniciadas a partir de agora)");
      })
      .catch((e) => toast.error("Falha ao salvar: " + (e as Error).message))
      .finally(() => setSavingWorkers(false));
  };

  const savePython = () => {
    setSaving(true);
    api
      .setSettings({ python_interpreter: pyInterp.trim() })
      .then((s) => {
        apply(s);
        toast.success("Configuração do interpretador Python salva");
      })
      .catch((e) => toast.error("Falha ao salvar: " + (e as Error).message))
      .finally(() => setSaving(false));
  };
  const detectPython = () => {
    setSaving(true);
    api
      .detectPython()
      .then((r) => setPyInterp(r.python_interpreter))
      .catch(() => undefined)
      .finally(() => setSaving(false));
  };

  React.useEffect(() => {
    api
      .settings()
      .then(apply)
      .catch(() => undefined)
      .finally(() => setLoaded(true));
  }, [apply]);

  const toggleTraffic = (v: boolean) => {
    setTrafficCapture(v); // optimistic
    setSaving(true);
    api
      .setSettings({ traffic_capture: v })
      .then(apply)
      .catch(() => setTrafficCapture(!v)) // revert on failure
      .finally(() => setSaving(false));
  };

  const toggleInjectPlanner = (v: boolean) => {
    setInjectPlanner(v); // optimistic
    api
      .setSettings({ constraints_inject_planner: v })
      .then(apply)
      .catch(() => setInjectPlanner(!v)); // revert on failure
  };

  const toggleAgentTrafficBinding = (v: boolean) => {
    setAgentTrafficBinding(v);
    setSaving(true);
    api
      .setSettings({ agent_traffic_binding: v })
      .then((s) => {
        apply(s);
        toast.success(v ? "Vinculação automática de tráfego pelo Agent ativada" : "Vinculação automática de tráfego pelo Agent desativada");
      })
      .catch((e) => {
        setAgentTrafficBinding(!v);
        toast.error(`Falha ao salvar: ${(e as Error).message}`);
      })
      .finally(() => setSaving(false));
  };

  const toggleInjectWorker = (v: boolean) => {
    setInjectWorker(v); // optimistic
    api
      .setSettings({ constraints_inject_worker: v })
      .then(apply)
      .catch(() => setInjectWorker(!v)); // revert on failure
  };

  const toggleNoaCompaction = (v: boolean) => {
    setNoaCompaction(v); // optimistic
    api
      .setSettings({ noa_compaction: v })
      .then((s) => {
        apply(s);
        toast.success(v ? "Compressão de contexto noa ativada (vale para as execuções iniciadas a partir de agora)" : "Compressão de contexto noa desativada (volta à compressão embutida)");
      })
      .catch((e) => {
        setNoaCompaction(!v); // revert on failure
        toast.error(`Falha ao salvar: ${(e as Error).message}`);
      });
  };

  // Persist a web-search patch (enable and/or backend). Optimistic with refetch.
  const saveWebSearch = (patch: Partial<Settings>) => {
    setSaving(true);
    api
      .setSettings(patch)
      .then((s) => {
        apply(s);
        toast.success("Configuração de busca na web salva");
      })
      .catch((e) => {
        toast.error("Falha ao salvar: " + (e as Error).message);
        api
          .settings()
          .then(apply)
          .catch(() => undefined);
      })
      .finally(() => setSaving(false));
  };

  const saveBraveKey = () => {
    setSavingKey(true);
    api
      .setSettings({ brave_search_api_key: braveKeyInput })
      .then((s) => {
        apply(s);
        setBraveKeyInput("");
        toast.success("Brave API Key salva");
      })
      .catch((e) => toast.error("Falha ao salvar: " + (e as Error).message))
      .finally(() => setSavingKey(false));
  };

  const saveTavilyKey = () => {
    setSavingTavilyKey(true);
    api
      .setSettings({ tavily_search_api_key: tavilyKeyInput })
      .then((s) => {
        apply(s);
        setTavilyKeyInput("");
        toast.success("Tavily API Key salva");
      })
      .catch((e) => toast.error("Falha ao salvar: " + (e as Error).message))
      .finally(() => setSavingTavilyKey(false));
  };

  const saveProxy = () => {
    setSavingProxy(true);
    api
      .setSettings({ web_search_proxy: proxyInput.trim() })
      .then((s) => {
        apply(s);
        toast.success(proxyInput.trim() ? "Proxy de saída salvo" : "Proxy de saída removido (passa a conexão direta)");
      })
      .catch((e) => toast.error("Falha ao salvar: " + (e as Error).message))
      .finally(() => setSavingProxy(false));
  };

  const saveGlobalProxy = () => {
    setSavingGlobalProxy(true);
    api
      .setSettings({ global_proxy: globalProxyInput.trim() })
      .then((s) => {
        apply(s);
        toast.success(globalProxyInput.trim() ? "Proxy global salvo" : "Proxy global removido (passa a conexão direta)");
      })
      .catch((e) => toast.error("Falha ao salvar: " + (e as Error).message))
      .finally(() => setSavingGlobalProxy(false));
  };

  // Run a real "test" search ("test") against the CURRENT form values (backend +
  // proxy + entered key), falling back to saved values server-side. Toasts result.
  const runTest = () => {
    setTesting(true);
    api
      .testWebSearch({
        web_search_backend: backend,
        web_search_proxy: proxyInput.trim(),
        brave_search_api_key: braveKeyInput,
        tavily_search_api_key: tavilyKeyInput,
      })
      .then((r) => {
        if (r.ok) toast.success(`Teste de busca bem-sucedido · ${r.backend} retornou ${r.count} resultados`);
        else toast.error("Falha no teste de busca: " + (r.error || "erro desconhecido"));
      })
      .catch((e) => toast.error("Falha no teste de busca: " + (e as Error).message))
      .finally(() => setTesting(false));
  };

  // brave-free selected but no key stored and none being entered → tool stays off.
  const braveNeedsKey = webSearch && backend === "brave-free" && !braveKeySet;

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">Configuração do sistema</h1>
        <p className="text-muted-foreground text-sm">Chaves globais de execução</p>
      </div>

      {/* Várias colunas em vez de grid: o card de busca na web é várias vezes mais alto que os demais, e a altura varia conforme o backend escolhido (brave/tavily
              têm a entrada de key renderizada condicionalmente). O grid esticaria a linha inteira até o card mais alto, deixando um grande vazio ao lado;
              as várias colunas balanceiam o preenchimento automaticamente pela altura do conteúdo. O espaçamento entre cards usa mb e não gap — no layout de colunas,
              column-gap só controla o espaço entre colunas; o espaço entre linhas precisa ser dado pelos próprios elementos filhos. */}
      <div className="columns-1 gap-4 md:gap-6 lg:columns-2">
        <UpdateCard />

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              Captura de tráfego
            </CardTitle>
            <CardDescription>
              Quando ativada, todo o tráfego HTTP de todos os Agents passa pelo proxy de registro e é gravado integralmente no banco, e as ferramentas traffic_search / traffic_get
              e a configuração de proxy são injetadas no Agent (o prompt inclui a explicação do proxy).
              <br />
              Quando desativada (padrão), nenhum tráfego é registrado: o Agent
              <b>não</b> recebe a configuração de proxy nem as ferramentas de tráfego, e o prompt também <b>não inclui</b> conteúdo relacionado a proxy. Ao alternar, o Agent é reconstruído imediatamente para aplicar.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="traffic-capture" className="text-sm font-normal text-muted-foreground">
              {trafficCapture ? "Ativada · registrando tráfego e injetando o proxy" : "Desativada · sem registro e sem injeção de proxy"}
            </Label>
            <Switch
              id="traffic-capture"
              checked={trafficCapture}
              disabled={!loaded || saving}
              onCheckedChange={toggleTraffic}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              Vinculação automática de tráfego pelo Agent
            </CardTitle>
            <CardDescription id="agent-traffic-binding-description">
              Desativada por padrão. Quando ativada, o Agent de relatório acionado na gravação da vulnerabilidade confere as requisições/respostas HTTP existentes e, depois de associar o tráfego correspondente, redige o relatório.
              <b>Consultar os pacotes e as chamadas de ferramenta extras aumenta o consumo de tokens.</b>
              <br />
              Com TCP, sem captura de pacotes ou sem tráfego correspondente, o reporte continua normal. Esta chave não afeta a captura de tráfego, a vinculação manual nem a visualização de evidências já salvas. Vale a partir da próxima rodada do Agent;
              ao desativar, novas vinculações automáticas são recusadas imediatamente.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="agent-traffic-binding" className="text-sm font-normal text-muted-foreground">
              {agentTrafficBinding ? "Ativada · aumenta o consumo de tokens" : "Desativada · a vinculação manual continua possível"}
            </Label>
            <Switch
              id="agent-traffic-binding"
              aria-describedby="agent-traffic-binding-description"
              checked={agentTrafficBinding}
              disabled={!loaded || saving}
              onCheckedChange={toggleAgentTrafficBinding}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              Proxy global
            </CardTitle>
            <CardDescription>
              Todo o <b>tráfego de alvo</b> de todos os Agents sai por este proxy (oculta o IP de origem / usa jump host). Suporta <b>http / https / socks5</b>, e aceita autenticação{" "}
              <code>user:pass</code>. Vazio = conexão direta.
              <br />
              Com a <b>captura de tráfego</b> ativada, ele funciona como <b>upstream</b> do proxy de registro (o tráfego continua sendo gravado integralmente e depois sai por este proxy); com a captura desativada, é injetado diretamente
              na saída do bash / WebFetch do Agent. É independente do proxy de busca na web e do proxy do LLM.
              <br />
              <b>Dica</b>: com a <b>captura desativada</b>, o socks5 depende do suporte de cada ferramenta de linha de comando a <code>ALL_PROXY</code> (o curl
              suporta, algumas ferramentas podem ignorar); se for usar principalmente socks5, recomenda-se ativar a captura de tráfego — nesse caminho o MITM
              disca por conta própria, as ferramentas não percebem e funciona de forma estável.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-2">
            <Label htmlFor="global-proxy" className="text-sm font-normal text-muted-foreground">
              Endereço do proxy
            </Label>
            <div className="flex items-center gap-2">
              <Input
                id="global-proxy"
                autoComplete="off"
                placeholder="socks5://user:pass@host:1080 ou http://host:port (vazio = conexão direta)"
                value={globalProxyInput}
                disabled={!loaded || savingGlobalProxy}
                onChange={(e) => setGlobalProxyInput(e.target.value)}
              />
              <Button type="button" onClick={saveGlobalProxy} disabled={!loaded || savingGlobalProxy}>
                Salvar
              </Button>
            </div>
            <p className="text-muted-foreground text-xs">
              {globalProxyInput.trim() ? "Configurado · todo o tráfego de alvo sai por este proxy" : "Não configurado · o tráfego de alvo sai direto"}
            </p>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <ShieldAlertIcon className="size-4" />
              Injeção de restrições operacionais
            </CardTitle>
            <CardDescription>
              Quando ativada, as <b>restrições operacionais</b> de cada tarefa (itens allow/deny mantidos em &quot;Restrições operacionais&quot; na visão geral da tarefa) são anexadas ao prompt de sistema do Agent correspondente
              para delimitar a fronteira da exploração (por exemplo, &quot;testar apenas a porta atual&quot;, &quot;proibido força bruta&quot;).
              <br />
              Pode-se controlar separadamente a injeção no <b>planejador (planner)</b> e no <b>executor (worker)</b>
              ; por padrão, ambos ativados. A alternância vale imediatamente (lida na próxima rodada), sem reconstruir o Agent. Desativada, o Agent deixa de ver as restrições.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="inject-planner" className="text-sm font-normal text-muted-foreground">
                Injetar no planejador (planner){injectPlanner ? " · ativado" : " · desativado"}
              </Label>
              <Switch
                id="inject-planner"
                checked={injectPlanner}
                disabled={!loaded}
                onCheckedChange={toggleInjectPlanner}
              />
            </div>
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="inject-worker" className="text-sm font-normal text-muted-foreground">
                Injetar no executor (worker){injectWorker ? " · ativado" : " · desativado"}
              </Label>
              <Switch
                id="inject-worker"
                checked={injectWorker}
                disabled={!loaded}
                onCheckedChange={toggleInjectWorker}
              />
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <FlaskConicalIcon className="size-4" />
              Recursos experimentais
            </CardTitle>
            <CardDescription>
              Mecanismos ainda em validação, desativados por padrão. Podem alterar o comportamento do Agent ou afetar a estabilidade; ative somente depois de entender o impacto.
              <br />
              <b>Compressão de contexto noa</b>: o modelo comprime ativamente o histórico de conversas longas (norma v0.4.0). Quando ativada, os quatro tipos de Agent integrados à plataforma (
              <b>planejador / executor / Agent principal / conversa</b>) passam a usar o noa para gerenciar o contexto, substituindo a compressão embutida,
              e o texto original comprimido é arquivado no diretório de trabalho da tarefa para consulta posterior. A alternância vale imediatamente (para as execuções iniciadas a partir de agora), sem reconstruir o Agent;
              ao desativar, a compressão embutida volta imediatamente.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="noa-compaction" className="text-sm font-normal text-muted-foreground">
              Compressão de contexto noa{noaCompaction ? " · ativada" : " · desativada"}
            </Label>
            <Switch
              id="noa-compaction"
              checked={noaCompaction}
              disabled={!loaded}
              onCheckedChange={toggleNoaCompaction}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <SearchIcon className="size-4" />
              Busca na web
            </CardTitle>
            <CardDescription>
              Esta é a <b>chave geral + configuração de fontes</b> da busca na web. Só depois de ativada é possível escolher, na <b>configuração de cada Agent</b>, se ele habilita individualmente
              a <b>web_search</b> (retorna só título/link/resumo, sem buscar o corpo; a obtenção do corpo fica com o WebFetch). A busca na web <b>não passa</b> pelo
              proxy de registro e é independente da captura de tráfego.
              <br />
              A fonte pode ser <b>DuckDuckGo (ddgs)</b> (sem Key), <b>Brave (versão gratuita)</b> (exige Brave API Key), {" "}
              <b>Tavily</b> (exige Tavily API Key) ou <b>DeepSeek</b> (reutiliza a configuração de LLM atual). Com a chave geral desativada, a chave de busca na web de cada
              Agent fica indisponível.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="web-search" className="text-sm font-normal text-muted-foreground">
                {webSearch ? "Chave geral ativada · pode ser habilitada individualmente na configuração de cada Agent" : "Desativada · nenhum Agent pode habilitar a busca na web"}
              </Label>
              <Switch
                id="web-search"
                checked={webSearch}
                disabled={!loaded || saving}
                onCheckedChange={(v) => {
                  setWebSearch(v); // optimistic
                  saveWebSearch({ web_search_enabled: v });
                }}
              />
            </div>

            {webSearch && (
              <div className="flex items-center justify-between gap-4">
                <Label className="text-sm font-normal text-muted-foreground">Fonte de busca</Label>
                <Select
                  value={backend}
                  disabled={!loaded || saving}
                  onValueChange={(v) => {
                    setBackend(v); // optimistic
                    saveWebSearch({ web_search_backend: v });
                  }}
                >
                  <SelectTrigger className="w-48 shrink-0">
                    <SelectValue placeholder="Escolher fonte" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="ddgs">DuckDuckGo (ddgs · gratuito, sem Key)</SelectItem>
                    <SelectItem value="brave-free">Brave (versão gratuita · exige Key)</SelectItem>
                    <SelectItem value="tavily">Tavily (exige Key)</SelectItem>
                    <SelectItem value="deepseek">DeepSeek (oficial)</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            )}

            {webSearch && backend === "deepseek" && (
              <div className="border-border/60 bg-muted/30 flex flex-col gap-2 rounded-md border p-3">
                <p className="text-sm font-medium">Busca na web oficial do DeepSeek</p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  Esta fonte reutiliza diretamente a <b>configuração de LLM ativa no momento</b>. Por isso ela
                  <b>só suporta modelos oficiais do DeepSeek</b>, e essa configuração <b>deve usar o protocolo anthropic</b>
                  — o endpoint de protocolo OpenAI do DeepSeek não suporta busca no servidor. Ao trocar a configuração de LLM, esta fonte pode deixar de funcionar.
                </p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  Diferente das outras fontes, a busca é <b>executada no servidor do DeepSeek</b>: cada busca consome uma chamada de modelo adicional (gera custo de
                  tokens), a requisição de busca <b>não passa pelo proxy de saída acima</b> e <b>não entra no registro de tráfego</b>; o resultado traz <b>apenas título e link</b>
                  (sem resumo), e quando o corpo é necessário, ele é obtido pelo WebFetch.
                </p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  Cabe a você confirmar se as condições acima são atendidas, o sistema não bloqueia; use o botão &quot;Testar busca&quot; abaixo para executar uma busca real e verificar.
                </p>
              </div>
            )}

            {webSearch && backend === "brave-free" && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="brave-key" className="text-sm font-normal text-muted-foreground">
                  Brave Search API Key
                  {braveKeySet && <span className="ml-2 text-xs text-emerald-500">Configurada</span>}
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="brave-key"
                    type="password"
                    autoComplete="off"
                    placeholder={braveKeySet ? "Configurada (vazio = sem alteração)" : "Informe a Brave API Key"}
                    value={braveKeyInput}
                    disabled={!loaded || savingKey}
                    onChange={(e) => setBraveKeyInput(e.target.value)}
                  />
                  <Button
                    type="button"
                    onClick={saveBraveKey}
                    disabled={!loaded || savingKey || braveKeyInput.trim() === ""}
                  >
                    Salvar
                  </Button>
                </div>
                {braveNeedsKey && (
                  <p className="text-xs text-amber-500">
                    Brave selecionado, mas a Key ainda não foi configurada — até salvar a Key, a ferramenta de busca não será ativada.
                  </p>
                )}
                <p className="text-muted-foreground text-xs">
                  A versão gratuita tem cota de cerca de 2.000 buscas/mês. Acesse https://brave.com/search/api/ para obter a Key.
                </p>
              </div>
            )}

            {webSearch && backend === "tavily" && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="tavily-key" className="text-sm font-normal text-muted-foreground">
                  Tavily Search API Key
                  {tavilyKeySet && <span className="ml-2 text-xs text-emerald-500">Configurada</span>}
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="tavily-key"
                    type="password"
                    autoComplete="off"
                    placeholder={tavilyKeySet ? "Configurada (vazio = sem alteração)" : "Informe a Tavily API Key (tvly-…)"}
                    value={tavilyKeyInput}
                    disabled={!loaded || savingTavilyKey}
                    onChange={(e) => setTavilyKeyInput(e.target.value)}
                  />
                  <Button
                    type="button"
                    onClick={saveTavilyKey}
                    disabled={!loaded || savingTavilyKey || tavilyKeyInput.trim() === ""}
                  >
                    Salvar
                  </Button>
                </div>
                {webSearch && backend === "tavily" && !tavilyKeySet && (
                  <p className="text-xs text-amber-500">
                    Tavily selecionado, mas a Key ainda não foi configurada — até salvar a Key, a ferramenta de busca não será ativada.
                  </p>
                )}
                <p className="text-muted-foreground text-xs">Acesse https://tavily.com para se cadastrar e obter a API Key.</p>
              </div>
            )}

            {webSearch && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="ws-proxy" className="text-sm font-normal text-muted-foreground">
                  Proxy de saída (opcional)
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="ws-proxy"
                    autoComplete="off"
                    placeholder="http://host:port ou socks5://host:port (vazio = conexão direta)"
                    value={proxyInput}
                    disabled={!loaded || savingProxy}
                    onChange={(e) => setProxyInput(e.target.value)}
                  />
                  <Button type="button" onClick={saveProxy} disabled={!loaded || savingProxy}>
                    Salvar
                  </Button>
                </div>
                <p className="text-muted-foreground text-xs">
                  Proxy de saída independente, usado apenas para acessar os endpoints de busca (VPN/SOCKS etc.). Não tem relação com o proxy MITM que registra tráfego; quando a rede não alcança, o acesso é feito por este proxy.
                </p>
              </div>
            )}

            {webSearch && (
              <div className="flex items-center justify-between gap-4 border-t pt-4">
                <p className="text-muted-foreground text-xs">
                  Faz uma busca real por &quot;test&quot; com a configuração atual (fonte + proxy + Key) para verificar se está funcionando.
                </p>
                <Button
                  type="button"
                  variant="outline"
                  onClick={runTest}
                  disabled={!loaded || testing}
                  className="shrink-0"
                >
                  {testing ? "Testando…" : "Testar busca"}
                </Button>
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              Script personalizado · Interpretador Python
            </CardTitle>
            <CardDescription>
              As ferramentas personalizadas do tipo <b>script</b> usam-no para rodar Python. É detectado automaticamente na inicialização (python3 tem prioridade); aqui é possível informar manualmente o caminho absoluto de um venv /
              versão específica; vazio = detecção automática em tempo de execução.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Input
                className="font-mono text-sm"
                placeholder="/usr/bin/python3 (vazio = detecção automática)"
                value={pyInterp}
                disabled={!loaded || saving}
                onChange={(e) => setPyInterp(e.target.value)}
              />
              <Button variant="outline" onClick={detectPython} disabled={!loaded || saving}>
                Detectar novamente
              </Button>
              <Button onClick={savePython} disabled={!loaded || saving}>
                Salvar
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <CpuIcon className="size-4" />
              Concorrência de trabalho · Número de Work Agents
            </CardTitle>
            <CardDescription>
              Quantidade de agents de trabalho executados em paralelo por tarefa (padrão 3). Quanto maior o valor, mais sondagens concorrentes e maior o consumo. Após a alteração,
              <b>vale para as tarefas iniciadas a partir de agora</b>; as tarefas em execução não são afetadas.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Input
                type="number"
                min={1}
                className="w-32 font-mono text-sm"
                placeholder="3"
                value={workers}
                disabled={!loaded || savingWorkers}
                onChange={(e) => setWorkers(e.target.value)}
              />
              <Button onClick={saveWorkers} disabled={!loaded || savingWorkers}>
                Salvar
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <KeyboardIcon className="size-4" />
              Tecla de envio da caixa de entrada da sessão
            </CardTitle>
            <CardDescription>
              A página de conversa e a caixa de entrada da sessão do Agent principal nos detalhes da tarefa compartilham esta configuração; vale imediatamente após escolher, sem precisar salvar.
              <br />
              Esta preferência <b>existe só neste navegador</b>, não é sincronizada com a conta, e precisa ser reconfigurada ao trocar de navegador ou limpar os dados do site.
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="chat-send-mode" className="text-sm font-normal text-muted-foreground">
              Forma de envio
            </Label>
            <Select value={sendMode} onValueChange={(v) => setChatSendMode(v as ChatSendMode)}>
              <SelectTrigger id="chat-send-mode" className="w-72">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CHAT_SEND_MODE_OPTIONS.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
