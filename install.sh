#!/usr/bin/env bash
# Script de instalação do ARTEX: ① tudo em Docker  ② compilar e executar localmente
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }
rand(){ head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24; }

# ── detecção / instalação automática do ambiente docker ───────────────────
ensure_docker(){
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    ok "docker e docker compose detectados"; return
  fi
  warn "docker / docker compose não detectado"
  case "$(uname -s)" in
    Linux)
      if [ "$(ask 'Instalar o Docker automaticamente? (y/n)' y)" = y ]; then
        curl -fsSL https://get.docker.com | sh
        sudo usermod -aG docker "$USER" || true
        ok "Docker instalado (a mudança de grupo exige novo login para dispensar o sudo)"
      else
        die "instale o docker por conta própria e tente novamente"
      fi ;;
    Darwin) die "no macOS, instale o Docker Desktop:https://www.docker.com/products/docker-desktop/" ;;
    *)      die "instale o docker por conta própria e tente novamente" ;;
  esac
}

# ── ① Tudo em Docker ───────────────────────────────
install_docker(){
  ensure_docker
  if [ ! -f .env ]; then
    cp .env.example .env 2>/dev/null || true
    local pw key
    pw="$(ask 'Senha do Postgres (Enter para gerar aleatória)' "$(rand)")"
    key="$(ask 'ANTHROPIC_API_KEY (pode deixar em branco e configurar depois na UI)' '')"
    sed -i.bak "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=${pw}|" .env
    sed -i.bak "s|^ANTHROPIC_API_KEY=.*|ANTHROPIC_API_KEY=${key}|" .env
    rm -f .env.bak
    ok ".env gerado (POSTGRES_PASSWORD definida)"
  else
    info "reaproveitando o .env existente"
  fi
  info "baixando as imagens e iniciando…"
  docker compose pull || true
  docker compose up -d
  ok "inicialização concluída → http://localhost:8787"
  info "ver logs: docker compose logs -f artex"
}

# ── ② Compilar e executar localmente ──────────────────────────────
install_local(){
  echo "Forma de instalação do banco de dados:"
  echo "  1) conectar a um PostgreSQL existente"
  echo "  2) subir um PostgreSQL com Docker (requer docker)"
  case "$(ask 'Escolha' 1)" in
    2)
      ensure_docker
      local pw; pw="$(ask 'Senha do Postgres (Enter para aleatória)' "$(rand)")"
      docker run -d --name artex-pg -p 5432:5432 \
        -e POSTGRES_USER=artex -e POSTGRES_PASSWORD="$pw" -e POSTGRES_DB=artex \
        -v artex-pg:/var/lib/postgresql/data postgres:16-alpine
      DB_HOST=127.0.0.1 DB_PORT=5432 DB_USER=artex DB_PASS="$pw" DB_NAME=artex DB_SSL=disable ;;
    *)
      DB_HOST="$(ask 'Endereço do banco de dados' 127.0.0.1)"
      DB_PORT="$(ask 'Porta' 5432)"
      DB_USER="$(ask 'Usuário' artex)"
      DB_PASS="$(ask 'Senha' '')"
      DB_NAME="$(ask 'Nome do banco de dados' artex)"
      DB_SSL="$(ask 'sslmode (disable/require)' disable)" ;;
  esac

  # Gera o config.json
  cat > config.json <<JSON
{
  "database": {
    "host": "${DB_HOST}",
    "port": ${DB_PORT},
    "user": "${DB_USER}",
    "password": "${DB_PASS}",
    "dbname": "${DB_NAME}",
    "sslmode": "${DB_SSL}"
  }
}
JSON
  ok "config.json gerado"

  # Verificação do ambiente go
  command -v go >/dev/null 2>&1 || die "Go não detectado, instale primeiro o Go (>=1.26): https://go.dev/dl/"
  ok "Go: $(go version)"

  # O front-end embutido precisa que o node gere os artefatos estáticos
  if command -v npm >/dev/null 2>&1; then
    info "compilando os artefatos estáticos do front-end…"
    ( cd web && npm ci && npm run build:static )
    rm -rf server/webui/dist && cp -r web/out server/webui/dist
    info "compilando o binário único embutido…"
    CGO_ENABLED=0 go build -tags embedui -trimpath -o artex ./cmd/artex
  else
    warn "npm não detectado: será compilado o backend **sem front-end embutido** (o front-end precisa rodar à parte com npm run dev)"
    CGO_ENABLED=0 go build -o artex ./cmd/artex
  fi
  ok "compilação concluída → ./artex"

  info "iniciando… (Ctrl-C para sair)"
  ./artex
}

echo "=============================="
echo "  Instalação do ARTEX"
echo "  1) instalação toda em Docker"
echo "  2) execução local (compilação com go)"
echo "=============================="
case "$(ask 'Escolha' 1)" in
  1) install_docker ;;
  2) install_local ;;
  *) die "escolha inválida" ;;
esac
