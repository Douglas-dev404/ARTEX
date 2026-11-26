#!/usr/bin/env bash
# Script de atualização do ARTEX: ① atualização Docker (baixar nova imagem e recriar)  ② atualização com compilação local (recriar o binário)
# Corresponde ao install.sh: o install cuida da primeira implantação, o update cuida da atualização para uma nova versão.
# Migração de DB não precisa ser executada manualmente — o artex reexecuta o schema.sql de forma idempotente a cada inicialização (incluindo ADD COLUMN/CREATE
# INDEX IF NOT EXISTS), então "reiniciar é migrar". Os dados (volume pgdata, ./data, ./skills) não são afetados.
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }

# ── Opcional: sincronizar o repositório com o código mais recente (compose/scripts/código-fonte da compilação local dependem disso) ───────
sync_repo(){
  [ -d .git ] && command -v git >/dev/null 2>&1 || { warn "não é uma cópia de trabalho git, pulando git pull"; return; }
  [ "$(ask 'Baixar o código mais recente (git pull --ff-only)? (y/n)' y)" = y ] || return
  if ! git pull --ff-only; then
    warn "git pull não conseguiu fazer fast-forward (há alterações locais ou os branches divergiram) — resolva manualmente e tente de novo; desta vez continua com o código atual"
  fi
}

# ── ① Atualização Docker ───────────────────────────────
update_docker(){
  command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 \
    || die "docker / docker compose não detectado, instale e implante primeiro com ./install.sh"
  [ -f .env ] || die ".env não encontrado, execute primeiro o ./install.sh para concluir a primeira implantação"

  # Opcional: atualizar para uma tag de versão específica (se vazio, mantém o ARTEX_TAG do .env, padrão latest)
  local tag; tag="$(ask 'Tag da imagem de destino (Enter para manter .env / latest)' '')"
  if [ -n "$tag" ]; then
    if grep -q '^ARTEX_TAG=' .env; then
      sed -i.bak "s|^ARTEX_TAG=.*|ARTEX_TAG=${tag}|" .env && rm -f .env.bak
    else
      printf '\nARTEX_TAG=%s\n' "$tag" >> .env
    fi
    ok "ARTEX_TAG definida como ${tag}"
  fi

  # Mexe só no artex: o postgres é fixo em 16-alpine e não precisa ser atualizado junto (baixá-lo seria puro desperdício de banda,
  # e uma mudança de versão maior ainda traria risco de compatibilidade). O artex declara depends_on postgres, então ao passar o nome do serviço
  # em up, se o pg não estiver de pé ele sobe automaticamente, e o que já está rodando é mantido como está, sem recriar.
  info "baixando nova imagem (somente artex)…"
  docker compose pull artex
  info "recriando e iniciando (o artex migra o schema automaticamente ao reiniciar)…"
  docker compose up -d artex
  ok "atualização concluída → http://localhost:8787"
  info "ver logs: docker compose logs -f artex"
  info "limpar imagens antigas (opcional): docker image prune -f"
}

# ── ② Atualização com compilação local ──────────────────────────────
update_local(){
  command -v go >/dev/null 2>&1 || die "Go não detectado (>=1.26): https://go.dev/dl/"
  [ -f config.json ] || warn "config.json não encontrado — se for a primeira implantação, use ./install.sh"
  ok "Go: $(go version)"

  if command -v npm >/dev/null 2>&1; then
    info "recompilando os artefatos estáticos do front-end…"
    ( cd web && npm ci && npm run build:static )
    rm -rf server/webui/dist && cp -r web/out server/webui/dist
    info "recompilando o binário único embutido…"
    CGO_ENABLED=0 go build -tags embedui -trimpath -o artex ./cmd/artex
  else
    warn "npm não detectado: compilando o backend **sem front-end embutido** (o front-end precisa rodar à parte com npm run dev)"
    CGO_ENABLED=0 go build -o artex ./cmd/artex
  fi
  ok "compilação concluída → ./artex"
  warn "reinicie o processo artex em execução para aplicar (o schema é migrado automaticamente ao reiniciar)"
}

echo "=============================="
echo "  Atualização do ARTEX"
echo "  1) atualização Docker (baixar nova imagem e recriar)"
echo "  2) atualização local (recompilação com go)"
echo "=============================="
case "$(ask 'Escolha' 1)" in
  1) sync_repo; update_docker ;;
  2) sync_repo; update_local ;;
  *) die "escolha inválida" ;;
esac
