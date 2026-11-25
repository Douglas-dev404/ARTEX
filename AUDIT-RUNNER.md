# Runner de auditoria de artefatos

O ARTEX mantém os jobs no PostgreSQL. Cada runner consome a mesma fila por HTTPS e verifica o SHA-256 dos bytes recebidos. O runner pode rodar em um host Linux dedicado, inclusive uma VM Kali, e usa apenas o binário Go e acesso à API do ARTEX.

## Configuração

1. Gere um segredo aleatório com `openssl rand -hex 32`. Forneça-o como `ARTEX_AUDIT_RUNNER_TOKEN` ao servidor e a cada runner pelo mecanismo de segredos do ambiente. O Compose lê a variável do `.env`. A API fica desativada quando a variável está vazia.
2. Compile o runner com `go build -o artex-audit-runner ./cmd/artex-audit-runner` em Linux. Para compilar a partir de Windows, defina `GOOS=linux` e `GOARCH` conforme a arquitetura da VM.
3. No host do runner, execute `./artex-audit-runner -url https://artex.exemplo.com -concurrency 3`. O runner faz conexões de saída para o ARTEX. A URL deve apontar para a origem HTTPS com certificado confiável. Para teste local, também aceita `http://127.0.0.1:8787`.

## Uso da API

Use o JWT da administração na origem do ARTEX. `POST /api/audit-jobs` aceita um UUID gerado pelo cliente, o hash esperado em hexadecimal minúsculo e até 1 MiB de conteúdo em base64. O exemplo verifica os três bytes ASCII `abc`.

```json
{
  "id": "c5b80051-dbd0-4fc5-a20e-92f415f56395",
  "operation": "verify_sha256",
  "expected_sha256": "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
  "artifact": "YWJj"
}
```

- `GET /api/audit-jobs/{id}` retorna o estado e, ao concluir, o hash calculado e o campo `matches`.
- `POST /api/audit-jobs/{id}/cancel` encerra o job e invalida a concessão em andamento.
- Reenvie o mesmo UUID e conteúdo para repetir uma submissão após uma falha de rede. Reutilizar o UUID com outro conteúdo retorna `409`.

## Capacidade e recuperação

Comece com um runner e três execuções simultâneas. Aumente a concorrência ou adicione runners usando a mesma origem. O PostgreSQL distribui as concessões e o servidor aceita até 128 jobs ativos. Um job tem até 10 minutos e três tentativas. Se um runner cair, a concessão expira em 30 segundos e outro runner pode assumir. Ao encerrar o job, a fila remove o conteúdo da linha e conserva o estado e os hashes para consulta. A limpeza de jobs expirados acontece quando um runner consulta a fila ou quando chega uma nova submissão.
