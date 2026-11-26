#!/bin/sh
# Script guardião de inicialização do ARTEX (Linux / macOS / Docker ENTRYPOINT)
#
# Uso:
#   ./start.sh                       executa em primeiro plano (Ctrl-C para parar)
#   nohup ./start.sh >artex.log 2>&1 &   executa em segundo plano, residente
#   ./start.sh -addr :9000           argumentos extras são repassados ao artex sem alteração
#
# Ele faz uma única coisa: executa o artex e, quando o processo termina, decide pelo código de saída se o relança.
#
#   0      parada normal pelo usuário   → sai do loop
#   75     o programa pediu reinício    → reexecuta imediatamente (clicou em "Atualização com um clique" ou "Rollback" na página)
#   outro  crash                        → reexecuta após backoff (1→2→4… no máximo 60 segundos)
#
# Deliberadamente não faz aqui download, verificação SHA256 nem troca do binário: essa lógica precisaria ser escrita em duas versões, sh e bat,
# e é justamente o elo que menos pode falhar — se um binário que não executa for instalado, este script o
# relançará fielmente, sem parar, e o usuário só resolveria entrando na máquina. Por isso verificação/troca ficam todas em Go (pacote selfupdate),
# feitas pelo próprio artex na inicialização, e o script permanece simples.
set -u

cd "$(dirname "$0")" || exit 1

BIN=./artex
[ -x "$BIN" ] || { echo "[artex] executável $BIN não encontrado" >&2; exit 1; }

RESTART_CODE=75
MAX_DELAY=60

child=0
stopping=0

# Encaminha o sinal de parada ao próprio artex.
#
# No Docker isso é indispensável: o docker stop só envia SIGTERM ao PID 1 (isto é, este script),
# e não ao processo filho. Sem o encaminhamento, o artex não recebe o sinal e não consegue fazer um shutdown gracioso; após 10 segundos recebe SIGKILL
# e as tarefas em execução são cortadas no meio do caminho.
forward() {
	stopping=1
	if [ "$child" -ne 0 ]; then
		kill -TERM "$child" 2>/dev/null || true
	fi
}
trap forward INT TERM

delay=1
while :; do
	"$BIN" "$@" &
	child=$!

	# Um sinal interrompe o wait e o faz retornar >128. Nesse momento o processo filho ainda está fazendo o shutdown gracioso,
	# então é preciso dar wait mais uma vez para obter o código de saída real dele.
	wait "$child"
	code=$?
	if [ "$code" -gt 128 ]; then
		wait "$child"
		code=$?
	fi
	child=0

	if [ "$stopping" -eq 1 ]; then
		echo "[artex] parado"
		exit 0
	fi

	case "$code" in
		0)
			echo "[artex] saída normal"
			exit 0
			;;
		"$RESTART_CODE")
			# Atualização/rollback pronto: após a reexecução o artex conclui a troca na inicialização (veja selfupdate.Bootstrap).
			echo "[artex] reinício solicitado, aplicando a nova versão…"
			delay=1
			;;
		*)
			echo "[artex] saída anormal (code=$code), reiniciando em ${delay}s" >&2
			sleep "$delay"
			delay=$((delay * 2))
			[ "$delay" -gt "$MAX_DELAY" ] && delay=$MAX_DELAY
			;;
	esac
done
