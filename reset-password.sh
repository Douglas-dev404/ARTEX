#!/usr/bin/env bash
# =============================================================================
# Script de redefinição da senha de administrador do ARTEX
#
# O nome de usuário de login é fixo: ARTEX; a senha fica armazenada como hash bcrypt na chave
# auth.password_hash da tabela settings do banco de dados. Este script, depois de conectar ao banco, usa o pgcrypto para gerar o hash bcrypt dentro do banco
# e gravá-lo de volta nessa chave — totalmente compatível com a validação de login do backend (golang.org/x/crypto/bcrypt).
#
# Dois tipos de implantação:
#   local (padrão) —— conecta ao banco diretamente com psql no host. As informações de conexão são obtidas na seguinte ordem de prioridade:
#                    argumentos de linha de comando > --dsn/$ARTEX_PG_DSN > database.* do config.json
#   docker        —— executa psql dentro do contêiner postgres por meio de `docker compose exec` (ou `docker exec`)
#                    (o compose por padrão não expõe a 5432 ao host, por isso passa pelo contêiner).
#
# Exemplos de uso:
#   ./reset-password.sh                          # local, lê config.json/ambiente automaticamente, pede a nova senha de forma interativa
#   ./reset-password.sh -p 'NewPass!'            # local, informa a nova senha diretamente
#   ./reset-password.sh --dsn postgres://u:p@h:5432/artex
#   ./reset-password.sh -H 127.0.0.1 -P 5433 -U autopentest -W pass -d artex
#   ./reset-password.sh -m docker                # implantação docker (lê POSTGRES_* do .env)
#   ./reset-password.sh -m docker -c nome-do-container-pg --exec docker
#
# Segurança: a nova senha é passada por variável de ambiente + psql \getenv (não entra no argv do processo), e escapada com :'var'
# automaticamente (previne SQL injection); a senha do banco é passada por PGPASSWORD, e também não entra no argv.
# =============================================================================
set -euo pipefail

PASS_KEY="auth.password_hash"
BCRYPT_COST=10

MODE=""            # local | docker (vazio = detecção automática)
DSN=""
HOST="" PORT="" USER="" DBPASS="" DBNAME="" SSLMODE=""
CONFIG=""
CONTAINER=""       # nome do serviço/contêiner postgres no modo docker (padrão postgres)
EXEC_KIND=""       # compose | docker (qual exec usar no modo docker; vazio = automático)
NEWPASS=""
ASSUME_YES=0

die() { echo "Erro: $*" >&2; exit 1; }
info() { echo "· $*" >&2; }

usage() { sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'; exit 0; }

# ---- Interpretação dos argumentos -------------------------------------------------------------
while [[ $# -gt 0 ]]; do
  case "$1" in
    -m|--mode)        MODE="${2:-}"; shift 2 ;;
    --dsn)            DSN="${2:-}"; shift 2 ;;
    -H|--host)        HOST="${2:-}"; shift 2 ;;
    -P|--port)        PORT="${2:-}"; shift 2 ;;
    -U|--user)        USER="${2:-}"; shift 2 ;;
    -W|--db-password) DBPASS="${2:-}"; shift 2 ;;
    -d|--dbname)      DBNAME="${2:-}"; shift 2 ;;
    --sslmode)        SSLMODE="${2:-}"; shift 2 ;;
    --config)         CONFIG="${2:-}"; shift 2 ;;
    -c|--container)   CONTAINER="${2:-}"; shift 2 ;;
    --exec)           EXEC_KIND="${2:-}"; shift 2 ;;
    -p|--new-password) NEWPASS="${2:-}"; shift 2 ;;
    -y|--yes)         ASSUME_YES=1; shift ;;
    -h|--help)        usage ;;
    *) die "argumento desconhecido: $1 (use -h para ver o uso)" ;;
  esac
done

# ---- Lê database.* do config.json (somente no modo local e quando a conexão não foi informada explicitamente) -----
# Prefere python3 para interpretar (mais robusto); sem python3, recorre ao grep (o config.json tem campos regulares e separados).
read_config_json() {
  local path="$1"
  [[ -f "$path" ]] || return 1
  if command -v python3 >/dev/null 2>&1; then
    python3 - "$path" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1])).get("database", {})
except Exception:
    sys.exit(1)
# Suporta informar o dsn diretamente, ou campos separados
if d.get("dsn"):
    print("DSN\t" + d["dsn"]); sys.exit(0)
for k in ("host","port","user","password","dbname","sslmode"):
    if d.get(k) is not None:
        print(k.upper() + "\t" + str(d[k]))
PY
  else
    # Alternativa mínima: grep chave por chave (valor string ou número)
    local k
    for k in host port user password dbname sslmode; do
      local v
      v=$(grep -oE "\"$k\"[[:space:]]*:[[:space:]]*(\"[^\"]*\"|[0-9]+)" "$path" 2>/dev/null \
            | head -1 | sed -E "s/.*:[[:space:]]*//; s/^\"//; s/\"$//") || true
      [[ -n "$v" ]] && echo -e "${k^^}\t$v"
    done
  fi
}

apply_config_fields() {
  local line key val
  while IFS=$'\t' read -r key val; do
    [[ -z "$key" ]] && continue
    case "$key" in
      DSN)      [[ -z "$DSN" ]] && DSN="$val" ;;
      HOST)     [[ -z "$HOST" ]] && HOST="$val" ;;
      PORT)     [[ -z "$PORT" ]] && PORT="$val" ;;
      USER)     [[ -z "$USER" ]] && USER="$val" ;;
      PASSWORD) [[ -z "$DBPASS" ]] && DBPASS="$val" ;;
      DBNAME)   [[ -z "$DBNAME" ]] && DBNAME="$val" ;;
      SSLMODE)  [[ -z "$SSLMODE" ]] && SSLMODE="$val" ;;
    esac
  done
}

# ---- Detecção automática do modo ---------------------------------------------------------
if [[ -z "$MODE" ]]; then
  if [[ -n "$DSN$HOST$USER$DBNAME" || -n "${ARTEX_PG_DSN:-}" || -f "${CONFIG:-config.json}" ]]; then
    MODE="local"
  elif command -v docker >/dev/null 2>&1 && [[ -f docker-compose.yml ]]; then
    MODE="docker"
  else
    MODE="local"
  fi
fi
info "modo de implantação: $MODE"

# ---- Coleta da nova senha -----------------------------------------------------------
if [[ -z "$NEWPASS" ]]; then
  read -r -s -p "Digite a nova senha (o usuário é fixo: ARTEX):" NEWPASS; echo >&2
  [[ -n "$NEWPASS" ]] || die "a senha não pode ser vazia"
  read -r -s -p "Digite novamente para confirmar:" NEWPASS2; echo >&2
  [[ "$NEWPASS" == "$NEWPASS2" ]] || die "as duas entradas não coincidem"
fi
[[ -n "$NEWPASS" ]] || die "a senha não pode ser vazia"

# Entrega a senha ao psql por variável de ambiente (lida com \getenv, sem entrar em argv/ps)
export ARTEX_RESET_NEWPASS="$NEWPASS"

# Gera o bcrypt dentro do banco e faz upsert; a senha é escapada automaticamente com :'newpw'. CREATE EXTENSION é idempotente,
# se o papel do banco não tiver permissão para criar extensões, o erro ocorre aqui (veja a dica no ramo de falha do run abaixo).
SQL=$(cat <<SQL
\\set ON_ERROR_STOP on
\\getenv newpw ARTEX_RESET_NEWPASS
CREATE EXTENSION IF NOT EXISTS pgcrypto;
INSERT INTO settings(key, value)
VALUES ('$PASS_KEY', crypt(:'newpw', gen_salt('bf', $BCRYPT_COST)))
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now();
SQL
)

# ---- Execução -----------------------------------------------------------
if [[ "$MODE" == "local" ]]; then
  # Prioridade das informações de conexão: linha de comando > --dsn/$ARTEX_PG_DSN > config.json
  if [[ -z "$DSN" && -z "$HOST$USER$DBNAME" ]]; then
    [[ -n "${ARTEX_PG_DSN:-}" ]] && DSN="$ARTEX_PG_DSN"
  fi
  if [[ -z "$DSN" && -z "$HOST$USER$DBNAME" ]]; then
    cfg="${CONFIG:-config.json}"
    if [[ -f "$cfg" ]]; then
      info "lendo a configuração do banco de $cfg"
      apply_config_fields < <(read_config_json "$cfg")
    fi
  fi

  command -v psql >/dev/null 2>&1 || die "psql não encontrado nesta máquina (instale o postgresql-client ou use -m docker)"

  declare -a PSQL_ARGS=()
  if [[ -n "$DSN" ]]; then
    PSQL_ARGS=("$DSN")
    target="$DSN"
  else
    [[ -n "$USER"   ]] || die "falta o usuário do banco (-U) ou um config.json/DSN válido"
    [[ -n "$DBNAME" ]] || die "falta o nome do banco (-d) ou um config.json/DSN válido"
    HOST="${HOST:-127.0.0.1}"; PORT="${PORT:-5432}"; SSLMODE="${SSLMODE:-disable}"
    PSQL_ARGS=(-h "$HOST" -p "$PORT" -U "$USER" -d "$DBNAME")
    [[ -n "$SSLMODE" ]] && export PGSSLMODE="$SSLMODE"
    [[ -n "$DBPASS" ]] && export PGPASSWORD="$DBPASS"
    target="$USER@$HOST:$PORT/$DBNAME"
  fi

  info "banco de dados de destino: $target"
  if [[ "$ASSUME_YES" -ne 1 ]]; then
    read -r -p "Confirmar a redefinição da senha do ARTEX neste banco? [y/N]" ans
    [[ "$ans" == "y" || "$ans" == "Y" ]] || die "cancelado"
  fi

  if ! printf '%s\n' "$SQL" | psql "${PSQL_ARGS[@]}" -v ON_ERROR_STOP=1 -q >/dev/null; then
    die "falha ao gravar. Se o erro for de permissão/ausência do pgcrypto, use um papel com permissão para criar extensões ou execute manualmente antes CREATE EXTENSION pgcrypto."
  fi

else
  # ---- docker ----
  command -v docker >/dev/null 2>&1 || die "docker não encontrado"
  CONTAINER="${CONTAINER:-postgres}"

  # Escolha do modo de exec: prefere docker compose exec (nome do serviço), senão docker exec (nome do contêiner)
  if [[ -z "$EXEC_KIND" ]]; then
    if docker compose version >/dev/null 2>&1 && [[ -f docker-compose.yml ]]; then
      EXEC_KIND="compose"
    else
      EXEC_KIND="docker"
    fi
  fi

  # Credenciais do psql dentro do contêiner: prefere a linha de comando, depois POSTGRES_* do .env, e por fim o padrão do compose (artex)
  if [[ -f .env ]]; then
    # shellcheck disable=SC1091
    set -a; . ./.env; set +a
  fi
  DUSER="${USER:-${POSTGRES_USER:-artex}}"
  DNAME="${DBNAME:-${POSTGRES_DB:-artex}}"
  [[ -n "$DBPASS" ]] && export PGPASSWORD="$DBPASS"
  [[ -z "${PGPASSWORD:-}" && -n "${POSTGRES_PASSWORD:-}" ]] && export PGPASSWORD="$POSTGRES_PASSWORD"

  info "alvo: contêiner $CONTAINER, psql -U $DUSER -d $DNAME (exec=$EXEC_KIND)"
  if [[ "$ASSUME_YES" -ne 1 ]]; then
    read -r -p "Confirmar a redefinição da senha do ARTEX no banco deste contêiner? [y/N]" ans
    [[ "$ans" == "y" || "$ans" == "Y" ]] || die "cancelado"
  fi

  # -e só leva o nome sem o valor → herda do ambiente atual, a senha não aparece no argv do comando docker.
  declare -a EXEC_CMD
  if [[ "$EXEC_KIND" == "compose" ]]; then
    EXEC_CMD=(docker compose exec -T -e ARTEX_RESET_NEWPASS -e PGPASSWORD "$CONTAINER"
              psql -U "$DUSER" -d "$DNAME" -v ON_ERROR_STOP=1 -q)
  else
    EXEC_CMD=(docker exec -i -e ARTEX_RESET_NEWPASS -e PGPASSWORD "$CONTAINER"
              psql -U "$DUSER" -d "$DNAME" -v ON_ERROR_STOP=1 -q)
  fi

  if ! printf '%s\n' "$SQL" | "${EXEC_CMD[@]}" >/dev/null; then
    die "falha ao gravar. Confirme o nome do contêiner (-c), a conta do banco (POSTGRES_* do .env) e se o papel tem permissão do pgcrypto."
  fi
fi

unset ARTEX_RESET_NEWPASS
echo "✓ Senha de administrador do ARTEX redefinida. Entre com o usuário ARTEX + a nova senha (não é preciso reiniciar o serviço)."
