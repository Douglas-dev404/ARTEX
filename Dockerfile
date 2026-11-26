# syntax=docker/dockerfile:1
#
# Imagem de execução (não compila dentro da imagem): instala só as ferramentas comuns e coloca o **binário único Linux pré-compilado**.
# O binário é compilado de forma cruzada pelo job binaries do CI (Go puro, sem QEMU) e fica, conforme a arquitetura alvo,
# no contexto de build em dist/<TARGETARCH>/artex. Assim, no build multi-arquitetura o arm64 só precisa emular a camada apt,
# sem emular a compilação de Next/Go, e fica bem mais rápido.
#
# Ao construir a imagem manualmente na máquina local, prepare o binário antes:
#   cd web && npm run build:static && cd ..
#   cp -r web/out server/webui/dist
#   CGO_ENABLED=0 GOARCH=amd64 go build -tags embedui -o dist/amd64/artex ./cmd/artex
#   docker build -t artex:local .
FROM python:3.12-slim-bookworm
ARG TARGETARCH
# Ferramentas comuns: ripgrep / curl / vim, mais um conjunto de utilitários de recon (adicione ou remova conforme a necessidade).
# Node 20.x instalado do NodeSource: o nodejs do apt do bookworm é o 18, e o Playwright exige >=20.
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ripgrep curl wget vim git jq unzip \
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*
# Pré-instala o Playwright MCP e a CLI (globalmente), para que em tempo de execução não seja preciso baixar via npx.
# @playwright/mcp: o MCP de browser usa direto `npx @playwright/mcp` (já instalado globalmente, sem necessidade de -y/@latest).
# @playwright/cli: fornece o playwright-cli; depois de instalar, roda --help para verificar que é executável.
# Depois instala o playwright (gerenciamento de browsers) e usa --with-deps para pré-instalar o chromium e suas dependências de sistema,
# de modo que o MCP/CLI dentro do contêiner funcione já na primeira inicialização, sem baixar o browser pela rede.
RUN npm install -g @playwright/mcp@latest @playwright/cli@latest playwright@latest \
    && playwright-cli --help \
    && playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
# Binário pré-compilado da arquitetura correspondente (dist/amd64/artex ou dist/arm64/artex)
COPY dist/${TARGETARCH}/artex /app/artex
# Script de inicialização (daemon): após a saída do processo, decide pelo código de saída se deve subir de novo; a atualização com um clique pela página depende dele para concluir a troca.
# Ele também encaminha o SIGTERM para o artex — o docker stop só envia o sinal ao PID 1;
# sem o encaminhamento o artex não o recebe, não consegue encerrar de forma graciosa e é morto à força por SIGKILL após 10 segundos.
COPY start.sh /app/start.sh
RUN chmod +x /app/artex /app/start.sh
COPY skills/ /app/skills/
# Ponto de persistência de data/ (SQLite + jwt.key)
VOLUME ["/app/data"]
EXPOSE 8787 8788
ENTRYPOINT ["/app/start.sh"]
CMD ["-addr", ":8787", "-proxy", ":8788"]
