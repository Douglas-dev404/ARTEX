# Conversas paralelas longas e orçamento de contexto

Corrigido em 2026-09-11. A implementação original tratava a contagem de caracteres do JSON da requisição diretamente como tokens e herdava a reserva de saída de 32K da tarefa principal, rejeitando antes da hora perguntas paralelas normais quando havia muito HTML, JS e resultados de ferramentas.

## Conferência das implementações de código aberto

- [Contexto paralelo do Grok CLI](https://github.com/superagent-ai/grok-cli/blob/fb97af83f06dca873281d60168430f06c8de6324/src/agent/agent.ts#L739): extrai trechos dos textos mais recentes do usuário e do assistente, com orçamento de cerca de 2000 caracteres e no máximo 400 caracteres por item. Não mantém um histórico contínuo de perguntas paralelas nesse caminho.
- [Requisição independente do Grok CLI](https://github.com/superagent-ai/grok-cli/blob/fb97af83f06dca873281d60168430f06c8de6324/src/utils/side-question.ts): sinal de cancelamento independente; quando o modelo suporta, o limite de saída é 2048 tokens; não fornece ferramentas.
- [Compactação da sessão principal do Grok CLI](https://github.com/superagent-ai/grok-cli/blob/fb97af83f06dca873281d60168430f06c8de6324/src/agent/compaction.ts): estima tokens, preserva o conteúdo recente, atualiza o resumo antigo com o conteúdo novo e trata truncamento entre turnos.
- [Compactação de sessão do OpenCode](https://github.com/anomalyco/opencode/blob/b3f1a96c6dd7adeb28b36dd11add1998fc84d67b/packages/core/src/session/compaction.ts): estimativa da requisição completa, reserva de saída/buffer, conteúdo recente mais resumo contínuo, requisição de resumo sem ferramentas; por padrão essa implementação usa orçamento recente de 8000 e limite de saída do resumo de 4096 tokens.
- [Recuperação de estouro do OpenCode](https://github.com/anomalyco/opencode/blob/b3f1a96c6dd7adeb28b36dd11add1998fc84d67b/packages/core/src/session/runner/llm.ts): só tenta recuperação de estouro quando a saída do assistente ainda não começou, e a chamada após a recuperação não entra de novo no mesmo caminho de recuperação.

O ARTEX aproveita as práticas de orçamento de saída independente, conteúdo recente com resumo contínuo e recuperação limitada. Mantém a estrutura de mensagens e o pareamento de ferramentas do norma v0.3.6, sem copiar a extração de texto do Grok; não grava os eventos de compactação da sessão principal do OpenCode no transcript principal do ARTEX.

## Orçamento e execução da requisição

- As mensagens seguem a estimativa por bytes UTF-8 por bloco de conteúdo do norma, com margem de 4/3; além disso, contam-se o prompt de sistema, o schema das ferramentas e o overhead do envelope das mensagens. A estimativa não é a contagem exata de tokens do modelo.
- A saída paralela é de no máximo 8192 tokens por padrão, sem ultrapassar o limite de saída já definido na configuração principal. A variável de ambiente do serviço `ARTEX_BTW_MAX_OUTPUT_TOKENS` define um limite entre 256 e 32768; não altera o modelo padrão do produto nem os parâmetros da tarefa principal.
- O orçamento de entrada é a janela de contexto menos o limite de saída e a margem de segurança; janela desconhecida usa o padrão da plataforma, de 200K. A margem de segurança é 5% da janela, no mínimo 128 e no máximo 8192 tokens.
- Perguntas e respostas bem-sucedidas são carregadas em lotes de no máximo 20 grupos, por número de sequência crescente. Mantêm-se no máximo 20 grupos em texto original, com orçamento de tokens de no máximo 1/4 do orçamento de entrada e sem passar de 16K.
- O excedente de perguntas e respostas é atualizado no resumo contínuo. O resumo traz a origem histórica e o horário do contexto; a resposta histórica do assistente não equivale a nova evidência de ferramenta, e em caso de conflito prevalece o snapshot principal mais recente.
- Se o contexto principal ainda for longo demais, apenas as mensagens antigas da cópia são resumidas, mantendo-se no máximo 8K tokens recentes; o ponto de corte não separa chamada de ferramenta e resultado. Um único grupo grande demais entra inteiro no resumo.
- A entrada do resumo é dividida em blocos seguros para UTF-8 conforme a janela efetivamente restante, com limite de saída de 2048 tokens; resumo vazio, truncado, com chamada de ferramenta ou acima do orçamento do resumo não é gravado no cache. Cada pergunta paralela faz no máximo 12 chamadas de resumo, sujeitas ao mesmo timeout de 120 segundos; atingir o limite resulta em falha explícita, sem loop infinito.
- Se o modelo retornar estouro de contexto na primeira vez, ainda sem saída de texto nem chamada de ferramenta, após nova redução reenvia-se no máximo uma vez; se o tamanho estimado não diminuir, a recuperação para imediatamente. Outros erros do modelo e saída parcial em streaming não disparam essa recuperação.
- Todo o uso obtido, incluindo resumo, tentativas falhas e uso no cancelamento, é somado à mesma requisição paralela. Quando o Provider não retorna uso, só se pode registrar zero, sem passar uma estimativa por uso real.

## Persistência e interface

`side_question_sessions.memory` guarda o resumo das perguntas e respostas antigas, o ordinal coberto e o resumo do contexto principal em cache pela identidade do snapshot. `side_question_requests.context_info` guarda a fase de preparação, o número de grupos efetivamente reproduzidos, o uso do resumo e a estimativa do orçamento.

O resumo só é salvo se a requisição original ainda estiver em execução e a versão de limpeza coincidir; limpar também apaga o cache, e gravações tardias não restauram dados já limpos. Um snapshot novo não reutiliza o resumo de um snapshot antigo. Os campos do resumo são salvos junto com o arquivo de tarefa v3; ao restaurar um v3 antigo sem esses campos, preenche-se um objeto vazio, e v1/v2 continuam compatíveis.

O POST primeiro admite e retorna a requisição; a preparação e a compactação rodam em segundo plano, sem segurar o lock de admissão nem transação do banco. O SSE/histórico exibem as fases de preparação, organização das perguntas e respostas, compactação da cópia e resposta; falha de compactação é salva como estado terminal de falha dessa pergunta paralela. O front-end mantém o erro e restaura o rascunho da pergunta que falhou, sem usar notificação flutuante que tape a caixa de entrada; o polling do histórico não apaga mais o erro de envio.

## Registro de validação

- Reprodução de 19/20/21/50 grupos, conservação de conclusões antigas além de 20 grupos, reuso do cache de resumo após reinício: aprovado na automação.
- Respostas muito longas em chinês e contexto de código, orçamento de requisições em blocos, pareamento de ferramentas, snapshot imutável, invalidação de cache com snapshot novo: aprovado na automação.
- Resumo com falha/cancelamento/truncamento/excesso de tamanho/retorno de ferramenta, disputa na limpeza, limite de chamadas, recuperação de estouro apenas uma vez e sem nova tentativa em stream parcial: aprovado na automação.
- Em PostgreSQL independente: paginação, reinício, arquivos v1/v2/v3, restauração de arquivo com cache de resumo e metadados de orçamento, v3 antigo sem os campos novos, 20 sessões-pai compartilhando quatro vagas de concorrência: aprovado.
- Build do serviço candidato em Go, verificação TypeScript do front-end, verificação Biome dos componentes modificados e build de produção do Next.js em diretório independente: aprovado.
- O navegador embutido, com fixtures de UI independentes, verificou em 1280×720 e 390×844 as fases de organização, a indicação do escopo do resumo, a restauração do rascunho após falha, a ausência de notificação flutuante de erro, a ausência de overflow horizontal e a ausência de erros no console. As fixtures temporárias foram removidas.
- Reprodução somente leitura dos snapshots de Worker existentes na máquina; a nova verificação de orçamento passou; por exemplo, o snapshot de 293085 caracteres do Worker #3 não é mais rejeitado por engano pela contagem local de caracteres. Este item não fez chamadas a modelos externos.
- O teste de conversa real que enviaria um snapshot privado de Worker ao Grok foi recusado pela revisão de aprovação automática e não foi executado, não contando como item aprovado.
- Em 2026-09-11 00:37, a pedido do usuário, o backend local foi reiniciado, mantendo o banco de dados, o diretório de dados e a configuração de login originais. O arquivo em execução e o binário candidato têm o mesmo SHA-256; o `/api/health` do backend e do proxy do front-end retornaram normal.

Comandos de validação (apenas com banco de testes independente):

```sh
go test -race ./sidequestion ./db ./server -run 'TestSide|TestCheckpoint|TestSnapshot|TestBuildRequest|TestService|TestMainSide|TestTaskArchive' -count=1
go build ./cmd/artex
npx tsc --noEmit
npm run build -- --webpack
```

O build de produção do front-end usa uma cópia independente, para não sobrescrever o `.next` do preview atual. O serviço candidato fica em `/private/tmp/artex-btw-budget-candidate`, foi copiado para `/private/tmp/artex-btw-preview/artex` e iniciado; o binário original foi salvo como `artex.before-context-budget` no mesmo diretório.
