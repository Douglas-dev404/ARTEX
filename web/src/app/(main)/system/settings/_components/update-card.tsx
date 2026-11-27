"use client";

import * as React from "react";

import {
  CheckCircle2Icon,
  DownloadIcon,
  ExternalLinkIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  TriangleAlertIcon,
} from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { api, sseUrl } from "@/lib/api";
import type { UpdateCheck, UpdateProgress } from "@/lib/types";

/** Tempo máximo de espera para a nova versão ficar no ar. Uma atualização passa por três inicializações de processo (preparação → troca → nova versão),
 *  cada uma leva segundos; três minutos bastam para cobrir discos lentos e a recriação de contêiner Docker. */
const RESTART_TIMEOUT_MS = 180_000;

function humanSize(n?: number): string {
  if (!n || n <= 0) return "";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i === 0 ? 0 : 1)} ${units[i]}`;
}

const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

export function UpdateCard() {
  const [info, setInfo] = React.useState<UpdateCheck | null>(null);
  const [checking, setChecking] = React.useState(true);
  const [progress, setProgress] = React.useState<UpdateProgress | null>(null);
  // Separado do progress: depois de concluída a preparação o processo deixa de existir e o SSE cai; aí é preciso passar a fazer polling em /api/health.
  const [restarting, setRestarting] = React.useState(false);
  const [busy, setBusy] = React.useState(false);

  // quiet também decide se o cache do backend é ignorado: a verificação automática ao entrar na página usa o cache (a barra superior acabou de consultar),
  // e quando o usuário clica manualmente em "Verificar atualizações" força-se a consulta à origem; caso contrário, uma versão recém-publicada só apareceria quando o cache expirasse.
  const check = React.useCallback((quiet = false) => {
    setChecking(true);
    api
      .checkUpdate(!quiet)
      .then((r) => {
        setInfo(r);
        if (!quiet) {
          if (r.error) toast.error("Falha ao verificar atualizações: " + r.error);
          else if (r.has_update) toast.success(`Nova versão ${r.latest} encontrada`);
          else if (r.comparable) toast.success("A versão atual já é a mais recente");
        }
      })
      .catch((e) => {
        if (!quiet) toast.error("Falha ao verificar atualizações: " + (e as Error).message);
      })
      .finally(() => setChecking(false));
  }, []);

  React.useEffect(() => {
    check(true);
  }, [check]);

  // Faz polling em /api/health até o número da versão mudar.
  //
  // O critério tem que ser "a versão mudou" e não "voltou a conectar": durante a troca a versão antiga sobe brevemente mais uma vez
  // (essa vez serve só para colocar o artex.new no lugar e sair imediatamente); olhar só a conectividade levaria a concluir sucesso por engano.
  const waitForNewVersion = React.useCallback(async (fromVersion: string) => {
    setRestarting(true);
    const deadline = Date.now() + RESTART_TIMEOUT_MS;
    while (Date.now() < deadline) {
      await sleep(2000);
      try {
        const r = await fetch("/api/health", { cache: "no-store" });
        if (r.ok) {
          const j = (await r.json()) as { version?: string };
          if (j.version && j.version !== fromVersion) {
            toast.success(`Atualizado para ${j.version}, recarregando a página`);
            await sleep(800);
            window.location.reload();
            return;
          }
        }
      } catch {
        // Não conseguir conectar durante a janela de reinício é esperado; continua o polling.
      }
    }
    setRestarting(false);
    toast.error(
      "Tempo esgotado aguardando o serviço reiniciar. Verifique os logs do backend ou confirme que o artex foi iniciado por start.sh / start.bat.",
    );
  }, []);

  // Assina o progresso da atualização. O SSE não passa pelo rewrite /api do Next (essa camada faz buffer e os eventos não saem).
  const openStream = React.useCallback(
    (fromVersion: string) => {
      const es = new EventSource(sseUrl("/api/update/stream"));
      es.onmessage = (ev) => {
        let p: UpdateProgress;
        try {
          p = JSON.parse(ev.data) as UpdateProgress;
        } catch {
          return;
        }
        setProgress(p);
        if (p.phase === "failed") {
          es.close();
          setBusy(false);
          toast.error("Falha na atualização: " + (p.error || p.message));
          return;
        }
        if (p.phase === "staged") {
          es.close();
          void waitForNewVersion(fromVersion);
        }
      };
      es.onerror = () => {
        // Quando o processo encerra, o SSE necessariamente cai. Se já se entrou na espera pelo reinício, isso é normal;
        // basta deixar o polling de /api/health seguir decidindo.
        es.close();
      };
      return es;
    },
    [waitForNewVersion],
  );

  const doUpdate = () => {
    if (!info) return;
    const from = info.current;
    const ok = window.confirm(
      `Confirmar atualização para ${info.latest}?\n\n` +
        "A atualização reinicia o programa e as tarefas em execução serão interrompidas.\n" +
        (info.mode === "docker"
          ? "\nAtenção: a atualização dentro do contêiner substitui apenas o programa em si, não atualiza as ferramentas como playwright / nmap da imagem; " +
            "se a nova versão depender de novas ferramentas, use docker compose pull."
          : ""),
    );
    if (!ok) return;

    setBusy(true);
    setProgress({ phase: "downloading", percent: 0, message: "Preparando…" });
    const es = openStream(from);
    api.applyUpdate().catch((e) => {
      es.close();
      setBusy(false);
      setProgress(null);
      toast.error("Falha ao iniciar a atualização: " + (e as Error).message);
    });
  };

  const doRollback = () => {
    if (!info) return;
    if (
      !window.confirm(
        "Voltar para a versão anterior?\n\nO programa será reiniciado e as tarefas em execução serão interrompidas.\nAtenção: a estrutura do banco de dados não é revertida, e a versão antiga pode não reconhecer dados gravados pela nova versão.",
      )
    )
      return;
    const from = info.current;
    setBusy(true);
    api
      .rollbackUpdate()
      .then(() => {
        toast.success("Revertido para a versão anterior, reiniciando…");
        void waitForNewVersion(from);
      })
      .catch((e) => {
        setBusy(false);
        toast.error("Falha na reversão: " + (e as Error).message);
      });
  };

  const phase = progress?.phase;
  const showProgress = busy || restarting;
  // Só a fase de download fornece uma porcentagem real (calculada por Content-Length). Verificação/descompactação/espera pelo reinício são todas fases
  // de duração desconhecida; a barra de progresso é preenchida e recebe uma animação de pulso para indicar "ocupado, mas sem saber quanto falta".
  const downloading = !restarting && phase === "downloading";
  const pct = downloading ? Math.max(progress?.percent ?? 0, 0) : 100;

  return (
    // A página de configurações usa layout de colunas em cascata; o card cuida do próprio espaçamento entre linhas e impede quebra entre colunas (ver o comentário em page.tsx).
    <Card className="mb-4 break-inside-avoid md:mb-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <DownloadIcon className="size-4" />
          Versão e atualizações
        </CardTitle>
        <CardDescription>
          Verifica e instala novas versões a partir do GitHub. A atualização reinicia o programa e as tarefas em
          execução serão interrompidas.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <span className="text-muted-foreground">Versão atual</span>
          <Badge variant="secondary" className="font-mono">
            {info?.current ?? "…"}
          </Badge>
          {info && (
            <>
              <Badge variant="outline" className="font-mono">
                {info.os}/{info.arch}
              </Badge>
              <Badge variant="outline">{info.mode === "docker" ? "Docker" : "Programa independente"}</Badge>
            </>
          )}
          {info?.latest && (
            <>
              <span className="text-muted-foreground">Versão mais recente</span>
              <Badge variant={info.has_update ? "default" : "secondary"} className="font-mono">
                {info.latest}
              </Badge>
            </>
          )}
          {info?.html_url && (
            <a
              href={info.html_url}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1 text-xs text-muted-foreground underline-offset-4 hover:underline"
            >
              Changelog <ExternalLinkIcon className="size-3" />
            </a>
          )}
        </div>

        {info?.boot_notice && (
          <p className="flex items-start gap-2 rounded-md border border-sev-medium/40 bg-sev-medium-bg p-2 text-xs text-sev-medium">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            {info.boot_notice}
          </p>
        )}

        {info?.error && (
          <p className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            Não foi possível conectar ao GitHub: {info.error} Configure um proxy global acima e tente novamente.
          </p>
        )}

        {info && !info.comparable && info.reason && <p className="text-xs text-muted-foreground">{info.reason}</p>}

        {info?.has_update && info.asset_available === false && (
          <p className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-2 text-xs text-destructive">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" />
            {info.latest} não fornece pacote de release para {info.os}/{info.arch} (falta {info.asset}), não é possível
            atualizar automaticamente.
          </p>
        )}

        {info?.has_update && info.asset_available !== false && (
          <p className="text-xs text-muted-foreground">
            Vai baixar <span className="font-mono">{info.asset}</span>
            {info.size ? ` (${humanSize(info.size)})` : ""}, verificar o SHA256 e fazer um smoke test antes de
            substituir; em caso de falha, a versão atual é mantida automaticamente.
          </p>
        )}

        {info && !info.has_update && info.comparable && !info.error && (
          <p className="flex items-center gap-2 text-xs text-muted-foreground">
            <CheckCircle2Icon className="size-3.5 text-state-done" />A versão atual já é a mais recente.
          </p>
        )}

        {info?.mode === "docker" && info.has_update && (
          <p className="text-xs text-muted-foreground">
            A atualização no Docker substitui apenas o programa em si, não atualiza as ferramentas como playwright /
            nmap da imagem, e<span className="font-mono"> docker compose up -d </span>
            ao recriar o contêiner volta para a versão que acompanha a imagem. Para atualizar também a imagem, execute
            <span className="font-mono"> docker compose pull artex &amp;&amp; docker compose up -d artex</span>.
          </p>
        )}

        {showProgress && (
          <div className="space-y-1.5">
            <Progress value={pct} className={downloading ? undefined : "animate-pulse"} />
            <p className="text-xs text-muted-foreground">
              {restarting
                ? "Reiniciando e aplicando a nova versão, aguarde (a página será atualizada automaticamente)…"
                : progress?.message}
            </p>
          </div>
        )}

        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={() => check(false)} disabled={checking || busy || restarting}>
            <RefreshCwIcon className={checking ? "size-4 animate-spin" : "size-4"} />
            Verificar atualizações
          </Button>
          <Button
            size="sm"
            onClick={doUpdate}
            disabled={busy || restarting || !info?.has_update || info?.asset_available === false}
          >
            <DownloadIcon className="size-4" />
            {info?.has_update ? `Atualizar para ${info.latest}` : "Atualizar agora"}
          </Button>
          {info?.has_backup && (
            <Button variant="ghost" size="sm" onClick={doRollback} disabled={busy || restarting}>
              <RotateCcwIcon className="size-4" />
              Reverter para a versão anterior
            </Button>
          )}
        </div>

        <p className="text-xs text-muted-foreground">
          A atualização com um clique depende do script guardião para reiniciar o programa. Inicie o ARTEX por{" "}
          <span className="font-mono">start.sh</span> (no Windows,
          <span className="font-mono"> start.bat</span>); ao executar o binário artex diretamente, o programa não será
          reiniciado automaticamente depois de sair.
        </p>
      </CardContent>
    </Card>
  );
}
