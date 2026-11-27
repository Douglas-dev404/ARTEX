"use client";

import { useEffect, useRef, useState } from "react";

import { useRouter } from "next/navigation";

import { AlertTriangle, ShieldCheck } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogClose, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import { auth } from "@/lib/auth";

export default function LoginPage() {
  const router = useRouter();
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [checking, setChecking] = useState(true);
  const [agreed, setAgreed] = useState(false);
  const [termsOpen, setTermsOpen] = useState(false);
  const [readToEnd, setReadToEnd] = useState(false);
  const termsBodyRef = useRef<HTMLDivElement>(null);

  // Só é possível clicar em "Concordo" depois de rolar até o fim dos termos (inclui o caso em que o conteúdo cabe sem rolagem).
  function handleTermsScroll() {
    const el = termsBodyRef.current;
    if (!el) return;
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 8) setReadToEnd(true);
  }

  useEffect(() => {
    if (!termsOpen) return;
    // Reseta ao abrir e trata o caso em que o conteúdo já cabe em uma tela e não dispara rolagem.
    setReadToEnd(false);
    const el = termsBodyRef.current;
    if (el && el.scrollHeight <= el.clientHeight + 8) setReadToEnd(true);
  }, [termsOpen]);

  useEffect(() => {
    // Já logado: vai direto para a interface principal (no export estático não há middleware para fazer esse redirecionamento).
    const token = auth.getToken();
    if (token) {
      // O localStorage ainda pode ter credenciais, mas o cookie já se perdeu. Sincroniza primeiro e depois faz uma requisição nova,
      // evitando que o guard do servidor ou o cache de rotas devolva o redirecionamento para a tela de login ainda em estado checking.
      auth.setToken(token);
      window.location.replace("/function/tasks");
      return;
    }
    api
      .authStatus()
      .then(({ initialized }) => {
        if (!initialized) router.replace("/setup");
      })
      .catch(() => setError("Não foi possível conectar ao serviço backend"))
      .finally(() => setChecking(false));
  }, [router]);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!agreed) {
      setError("Leia e aceite primeiro os Termos de Uso");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const { token } = await api.login("ARTEX", password);
      auth.setToken(token);
      window.location.replace("/function/tasks");
    } catch {
      setError("Usuário ou senha incorretos");
    } finally {
      setLoading(false);
    }
  }

  if (checking) {
    return (
      <div role="status" className="flex min-h-dvh items-center justify-center text-muted-foreground">
        Verificando status de login…
      </div>
    );
  }

  return (
    <div className="flex h-dvh">
      {/* Left panel */}
      <div className="hidden flex-col items-center justify-center bg-primary p-12 text-center lg:flex lg:w-1/3">
        <div className="relative flex items-center justify-center">
          <div className="absolute size-80 rounded-full border border-primary-foreground/10" />
          <div className="absolute size-60 rounded-full border border-primary-foreground/15" />
          <div className="absolute size-40 rounded-full border border-primary-foreground/20" />
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img src="/logo.png" alt="ARTEX" width={160} height={160} className="relative brightness-0 invert" />
        </div>
      </div>

      {/* Right panel */}
      <div className="flex w-full items-center justify-center bg-background p-8 lg:w-2/3">
        <div className="w-full max-w-md space-y-10 py-24 lg:py-32">
          <div className="space-y-4 text-center">
            <h2 className="text-2xl font-medium tracking-tight">Entrar</h2>
            <p className="mx-auto max-w-xl text-muted-foreground">Bem-vindo de volta. Digite a senha para continuar usando o ARTEX</p>
          </div>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <div className="space-y-1.5">
              <Label htmlFor="username">Usuário</Label>
              <Input id="username" value="ARTEX" readOnly className="bg-muted text-muted-foreground" />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="password">Senha</Label>
              <Input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="Digite a senha"
                autoFocus
                autoComplete="current-password"
              />
            </div>
            <div className="flex items-start gap-2">
              <Checkbox
                id="agree-terms"
                checked={agreed}
                onCheckedChange={(v) => setAgreed(v === true)}
                className="mt-0.5"
              />
              <Label htmlFor="agree-terms" className="text-sm font-normal leading-relaxed text-muted-foreground">
                Li e concordo com os
                <button
                  type="button"
                  onClick={() => setTermsOpen(true)}
                  className="mx-0.5 font-medium text-primary underline-offset-4 hover:underline"
                >
                  Termos de Uso
                </button>
              </Label>
            </div>
            {error && <p className="text-sm text-destructive">{error}</p>}
            <Button type="submit" className="w-full" disabled={loading || !password || !agreed}>
              {loading ? "Entrando..." : "Entrar"}
            </Button>
          </form>
        </div>
      </div>

      <Dialog open={termsOpen} onOpenChange={setTermsOpen}>
        <DialogContent className="gap-0 p-0 sm:max-w-2xl">
          <DialogHeader className="flex-row items-center gap-3 border-b px-6 py-4">
            <div className="flex size-10 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
              <ShieldCheck className="size-5" />
            </div>
            <div className="space-y-0.5">
              <DialogTitle className="text-base">ARTEX — Termos de Uso e Aviso Legal</DialogTitle>
              <p className="text-xs text-muted-foreground">
                Versão v1.0 · Data de vigência 2026-09-18 · Leia todos os termos abaixo antes de entrar
              </p>
            </div>
          </DialogHeader>

          <div
            ref={termsBodyRef}
            onScroll={handleTermsScroll}
            className="max-h-[60vh] space-y-5 overflow-y-auto px-6 py-5 text-sm leading-relaxed text-muted-foreground"
          >
            <p className="rounded-lg border bg-muted/40 p-3 text-foreground/80">
              Este "Termos de Uso e Aviso Legal" (doravante "este Aviso") é o acordo entre você e os autores e colaboradores
              do projeto ARTEX sobre o uso deste software. Leia com atenção antes de usar e entenda todos os termos, especialmente as cláusulas de
              isenção de responsabilidade, limitação de responsabilidade e proibições destacadas em negrito ou com fundo colorido.
              <span className="font-medium text-foreground">
                {" "}
                Ao baixar, instalar, acessar ou usar este software de qualquer forma, você é considerado como tendo lido, entendido e aceitado todas as condições deste Aviso.
              </span>
            </p>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  1
                </span>
                Artigo 1 · Definições e licença de código aberto
              </h4>
              <p className="pl-7">
                Este software (ARTEX) é um programa de código aberto distribuído sob a GNU Affero General Public License
                v3.0 (AGPL-3.0). Você pode usar, copiar, modificar e distribuir livremente este software de acordo com essa licença; porém, qualquer obra derivada (incluindo serviços online oferecidos a terceiros pela rede) também deve ser
                distribuída sob a AGPL-3.0 e disponibilizar aos usuários o código-fonte completo correspondente. Os termos completos da AGPL-3.0 são os do arquivo LICENSE que acompanha o software.
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  2
                </span>
                Artigo 2 · Escopo de uso autorizado
              </h4>
              <p className="pl-7">
                Este software destina-se apenas a estudo pessoal, pesquisa de código, discussão de princípios técnicos de segurança e validação técnica em um ambiente local isolado montado por você mesmo, adequado para aprendizado, pesquisa acadêmica, revisão de código e outros usos não ofensivos e não destrutivos. Salvo nos casos expressamente permitidos neste artigo, você não pode usar este software para qualquer outra finalidade.
              </p>
            </section>

            <section className="space-y-2">
              <h4 className="flex items-center gap-2 font-medium text-destructive">
                <span className="flex size-5 items-center justify-center rounded-md bg-destructive/10 text-xs font-semibold text-destructive">
                  3
                </span>
                <AlertTriangle className="size-4" />
                Artigo 3 · Condutas proibidas
              </h4>
              <ul className="ml-7 list-decimal space-y-1.5 rounded-lg border border-destructive/20 bg-destructive/5 p-3 pl-8 text-foreground/80 marker:text-destructive/70">
                <li>
                  É proibido fazer varreduras, sondagens, exploração ou ataques contra qualquer site, serviço online, pessoa ou sistema conectado de terceiros (com ou sem autorização, mesmo que seja ativo seu);
                </li>
                <li>É proibido usar este software em qualquer pentest real, disputa de ataque e defesa, exercício de red/blue team ou ambiente de produção;</li>
                <li>É proibido usar este software para invasão ilegal, roubo de dados, extorsão, negação de serviço (DoS/DDoS) ou qualquer atividade destrutiva ou criminosa;</li>
                <li>É proibido remover, alterar ou contornar qualquer aviso de direitos autorais, licença ou segurança presente no software ou em suas saídas;</li>
                <li>É proibido qualquer conduta que viole as leis, regulamentos e normas regulatórias do país ou região onde você está.</li>
              </ul>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  4
                </span>
                Artigo 4 · Propriedade intelectual
              </h4>
              <p className="pl-7">
                Os direitos autorais e demais direitos de propriedade intelectual deste software pertencem aos autores e colaboradores do projeto, e são concedidos a você dentro do escopo da licença AGPL-3.0. Fora dos direitos expressamente concedidos por essa licença, este Aviso não concede a você, de forma expressa ou implícita, nenhum outro direito.
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  5
                </span>
                Artigo 5 · Dados e privacidade
              </h4>
              <p className="pl-7">
                Este software é um programa de código aberto implantável por você mesmo; os autores não operam nenhum serviço centralizado e não coletam nem enviam seus dados de uso. Todos os dados gerados, processados ou acessados durante o uso são de sua responsabilidade, incluindo sua legalidade e segurança; quaisquer consequências decorrentes de tratamento inadequado de dados são de sua exclusiva responsabilidade.
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  6
                </span>
                Artigo 6 · Conformidade e responsabilidade legal
              </h4>
              <p className="pl-7">
                Você deve cumprir por conta própria todas as leis e regulamentos do país ou região onde está sobre segurança cibernética, segurança de dados, proteção de informações pessoais e crimes informáticos (incluindo, na China continental, mas não limitado a, a Lei de Segurança Cibernética, a Lei de Segurança de Dados, a Lei de Proteção de Informações Pessoais e suas interpretações judiciais).
                <span className="font-medium text-foreground">
                  {" "}
                  Toda responsabilidade legal e consequência decorrente de sua violação dessas leis e regulamentos ou das condições deste Aviso é exclusivamente sua, e não dos autores ou colaboradores do software.
                </span>
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  7
                </span>
                Artigo 7 · Isenção de garantias e limitação de responsabilidade
              </h4>
              <p className="pl-7">
                Este software é fornecido no estado em que se encontra ("AS IS") e conforme disponível ("AS AVAILABLE"), sem qualquer garantia expressa ou implícita, incluindo, mas não limitada a, garantias de comercialização, adequação a uma finalidade específica, precisão e não violação. Na máxima extensão permitida pela lei aplicável, os autores e colaboradores do software não respondem por quaisquer perdas diretas, indiretas, acidentais, especiais ou consequenciais decorrentes do uso ou da impossibilidade de uso do software (independentemente de a forma de uso ser adequada ou não), incluindo, mas não se limitando a, perda de dados, danos a sistemas, interrupção de negócios, lucros cessantes ou disputas legais.
              </p>
            </section>

            <section className="space-y-1.5">
              <h4 className="flex items-center gap-2 font-medium text-foreground">
                <span className="flex size-5 items-center justify-center rounded-md bg-muted text-xs font-semibold text-muted-foreground">
                  8
                </span>
                Artigo 8 · Alterações dos termos e interpretação final
              </h4>
              <p className="pl-7">
                Os autores podem atualizar este Aviso de tempos em tempos conforme exigido por lei ou pelo desenvolvimento do projeto; a versão atualizada será publicada com o projeto e passará a valer a partir da data de sua publicação. Continuar usando o software significa aceitar os termos revisados. Na medida permitida por lei, a interpretação final deste Aviso cabe aos autores do projeto. Se qualquer cláusula deste Aviso for considerada inválida, as demais continuam válidas.
              </p>
            </section>
          </div>

          <DialogFooter className="mx-0 mb-0 flex-col items-stretch gap-2 rounded-b-xl px-6 sm:flex-row sm:items-center sm:justify-between">
            <p className="text-xs text-muted-foreground">
              {readToEnd ? "Você visualizou todos os termos" : "Role os termos até o final antes de confirmar"}
            </p>
            <DialogClose asChild>
              <Button
                type="button"
                disabled={!readToEnd}
                onClick={() => {
                  setAgreed(true);
                  setError("");
                }}
              >
                Li e concordo com todos os termos
              </Button>
            </DialogClose>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
