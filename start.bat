@echo off
rem Muda o console para UTF-8, senão os textos acentuados deste arquivo ficam ilegíveis em terminais com codepage legada.
chcp 65001 >nul 2>&1
rem Script guardião de inicialização do ARTEX (Windows)
rem
rem Uso:
rem   start.bat                  executa em primeiro plano (Ctrl-C para parar)
rem   start.bat -addr :9000      argumentos extras são repassados ao artex sem alteração
rem
rem Ele faz uma única coisa: executa o artex.exe e, quando o processo termina, decide pelo código de saída se o relança.
rem
rem   0      parada normal pelo usuário   -> sai do loop
rem   75     o programa pediu reinício    -> reexecuta imediatamente (clicou em "Atualização com um clique" ou "Rollback" na página)
rem   outro  crash                        -> reexecuta após backoff (1->2->4… no máximo 60 segundos)
rem
rem Download, verificação SHA256 e troca do binário não ficam aqui; tudo é feito pelo próprio artex na inicialização
rem (pacote selfupdate). O script se mantém simples; veja a explicação no topo do start.sh.

setlocal enabledelayedexpansion
cd /d "%~dp0"

set "BIN=artex.exe"
if not exist "%BIN%" (
	echo [artex] executável %BIN% não encontrado 1>&2
	exit /b 1
)

set "RESTART_CODE=75"
set "MAX_DELAY=60"
set /a delay=1

:loop
"%BIN%" %*
set "code=!ERRORLEVEL!"

if "!code!"=="0" (
	echo [artex] saída normal
	exit /b 0
)

if "!code!"=="%RESTART_CODE%" (
	rem Atualização/rollback pronto: após a reexecução o artex conclui a troca na inicialização.
	echo [artex] reinício solicitado, aplicando a nova versão…
	set /a delay=1
	goto loop
)

echo [artex] saída anormal ^(code=!code!^), reiniciando em !delay!s 1>&2
rem O timeout falha em console redirecionado, então usa-se ping como alternativa (atraso de N segundos exige N+1 tentativas).
set /a pings=!delay!+1
ping -n !pings! 127.0.0.1 >nul 2>&1
set /a delay=!delay!*2
if !delay! gtr %MAX_DELAY% set /a delay=%MAX_DELAY%
goto loop
