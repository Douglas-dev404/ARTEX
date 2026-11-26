#!/usr/bin/env bash
# Modo de desenvolvimento: backend (:8787) + proxy de tráfego (:8788) e front-end next dev (:5173) rodando juntos.
# O /api do front-end é repassado por proxy reverso ao backend; Ctrl-C encerra tudo junto.
#
# Para o modo de binário único (front-end embutido), veja a seção "Binário único" do README; este script não é usado nele.
set -euo pipefail
cd "$(dirname "$0")"

# Ao sair, encerra todos os processos filhos deste grupo de processos (backend + front-end).
cleanup() { kill 0 2>/dev/null || true; }
trap cleanup EXIT INT TERM

# Backend (go run comum, sem front-end embutido); o número de work agents concorrentes é configurado em "Configurações do sistema".
go run ./cmd/artex -addr :8787 -proxy 127.0.0.1:8788 &

# Hot reload do front-end (servidor dev Vite/Next, /api repassado por proxy reverso para :8787).
( cd web && npm run dev ) &

echo "[dev] backend :8787 / proxy :8788 / front-end http://localhost:5173  (Ctrl-C para sair)"
wait
