# Registro de validação do `/btw`

Data: 2026-09-10. Branch: `codex/btw-side-question`. Baseline: `8dae851b9b622f2ff2631f332fde9719d0b16fba`.

Usados um banco de testes PostgreSQL e um diretório de dados independentes; as credenciais reais do modelo foram injetadas apenas no ambiente de teste independente, sem serem gravadas no código nem neste registro, e o modelo padrão do produto não foi modificado. Go 1.26.3, norma v0.3.6, Next.js 16.2.9.

As conversas reais com o modelo, os objetos retornados, as asserções de engenharia e o texto bruto da revisão do Qwen estão salvos em [validation-2026-09-10.json](validation-2026-09-10.json), sem credenciais de API.

## Verificações de engenharia

| Escopo | Resultado | Evidência |
| --- | --- | --- |
| Mensagens estruturadas, cópia profunda dos parâmetros de ferramenta | Aprovado | `TestCheckpointDeepCopyAndBoundaries` |
| Requisições de resumo / compactação não sobrescrevem, publicação de resposta completa e estado terminal, exclusão de respostas parciais | Aprovado | `TestCheckpointDeepCopyAndBoundaries`, `TestSnapshotExcludesPartialStreamAndSelectsPoolMember` |
| Identidade do membro real do pool de modelos | Aprovado | `TestSnapshotExcludesPartialStreamAndSelectsPoolMember` |
| Pareamento de ferramentas, reprodução de 20 grupos, corte por orçamento e erro de estouro | Aprovado | `TestBuildRequestCompactionToolPairingAndBudget` |
| Paralelismo principal/paralela, isolamento de cancelamento bidirecional | Aprovado | Provider bloqueante, `TestMainSideConcurrencyAndIndependentCancellation` |
| Sem execução de ferramentas, streaming / não streaming, uso já existente em caso de falha | Aprovado | `TestServiceNoToolsAndUsageOnFailure` |
| ChatAgent real do norma + ferramenta Read local, isolamento do transcript principal / atividade | Aprovado | `TestSideActualChatCheckpointToolResultAndTranscriptIsolation`, subcasos de streaming e não streaming |
| Persistência, paginação, idempotência, preservação de resposta parcial após reinício | Aprovado | `TestSideHistoryIdempotencyPagingAndRecovery` |
| Disputa entre limpeza e gravações tardias, exclusão do recurso-pai, comparação de versão | Aprovado | `TestSideClearLateWritersAndDeletedParent` |
| Arquivamento e restauração de MainAgent / Worker, v1/v2/v3 | Aprovado | `TestSideTaskArchiveVersions` |
| Três interfaces-pai, autenticação, propriedade de recursos, exclusão lógica de Worker | Aprovado | `TestSideHTTPGlobalLimitTaskWorkerAndDeletion`, `TestSideCheckpointPersistsBeforeAdmissionAndRestart` |
| Sessão principal ocupada pode receber pergunta paralela, reconexão / desconexão de SSE independente, cancelamento, limpeza | Aprovado | `TestSideHTTPBusyIsolationClearAndReconnect` |
| Concorrência de 1 por sessão-pai / 4 globais | Aprovado | Dois casos `TestSideHTTP…` |
| Snapshot gravado antes do envio, continuação após reinício, sessão antiga não pode forjar snapshot | Aprovado | `TestSideCheckpointPersistsBeforeAdmissionAndRestart` |
| Recusa de continuar quando a configuração em cache é excluída ou o modelo muda | Aprovado | `TestSideRejectsDeletedOrChangedCachedProfile` |
| Cancelar antes do arquivamento e esperar a gravação da resposta final e do uso | Aprovado | `TestSideTaskDrainPersistsBeforeArchive` |
| Cancelamento antecipado do consumidor de streaming registra o uso e a atribuição paralela apenas uma vez | Aprovado | `TestSideUsageRecordedOnceOnConsumerCancellation` |
| Worker restaurado automaticamente após reinício / contexto de execução com deadline continua publicando novo snapshot | Aprovado | `TestSideRestoredWorkerRuntimePublishesNewCheckpoint` |
| Verificação de race nos pacotes relacionados | Aprovado | Comando abaixo |
| TypeScript e build de produção | Aprovado | `npx tsc --noEmit`, `npm run build` |
| Biome nos novos módulos de front-end | Aprovado | `biome check`, 3 módulos novos |

Depois de configurar `ARTEX_PG_DSN` em um banco descartável separado, é possível reproduzir as verificações automatizadas (não aponte para o banco de produção):

```sh
go test -race ./agent ./db ./server ./sidequestion ./llmrec ./llmpool \
  -run 'Test(Side|Checkpoint|Snapshot|BuildRequest|Service|MainSide|CaptureRun|TaskArchive|CompleteForwards|StopIntent|CancelIntent)' -count=1
cd web
npx tsc --noEmit
npx biome check src/lib/side-questions.ts src/hooks/use-side-questions.ts src/components/side-question-workspace.tsx
npm run build
```

A regressão completa em Go não está toda verde: no pacote `server`, dois testes já existentes falham na etapa de limpeza do diretório temporário, ambos reportando `TempDir RemoveAll … directory not empty`:

- `TestInheritedActivityDetailAndRelationDeletion`
- `TestTaskMetadataPatchReturnsRenameAndPin`

Exportando o código-fonte da baseline não modificada acima e rodando o pacote `server` de novo no mesmo ambiente isolado, essas duas falhas de limpeza também se reproduzem. A execução da baseline mostrou ainda a falha da asserção de número de nós-alvo em `TestCoreTaskLifecyclePG`; a regressão final do `server` após as modificações não apresenta essa falha. Os demais pacotes passaram, e os casos relacionados à pergunta paralela e a verificação de race passaram. Os problemas da baseline não foram marcados como aprovados nesta entrega, e nenhuma asserção existente foi modificada para esconder problemas.

A saída do build do Next.js traz o aviso já existente de inferência de múltiplos lockfiles / workspace root; o build foi concluído e todas as páginas foram geradas com sucesso.

## Verificação no navegador

Usando o Codex In-app Browser, conectado a um serviço Go local independente e ao servidor de desenvolvimento do Next.js. No desktop e em tela estreita de 390 × 844 foram feitas as seguintes operações manuais automatizadas, verificando capturas de tela e logs do navegador:

- Digitar `/btw` durante a execução do chat comum: o conteúdo principal e a pergunta paralela aparecem ao mesmo tempo; a barra lateral do desktop funciona normalmente.
- Perguntas de acompanhamento em sequência; após interromper a pergunta paralela, a parte já gerada é mantida; o fluxo principal continua.
- Ao fechar o painel a requisição continua, e ao reabrir a resposta concluída é restaurada; após atualizar a página, um `/btw` vazio restaura o histórico.
- Entrada, botões, histórico e fechamento do Drawer em tela estreita funcionam normalmente, sem overflow horizontal.
- A limpeza usa um diálogo de confirmação; depois de limpar o histórico desaparece, e o transcript principal e o snapshot são preservados.
- O MainAgent da tarefa e dois Workers fazem perguntas separadamente e alternam; as abas de Agent e o histórico não se misturam.
- Uma fixture de modelo local bloqueante mantém o Worker em execução; enviando `/btw` pela entrada principal do Worker e interrompendo a pergunta paralela, o Worker continua mostrando a execução em tempo real e seu próprio botão de pausa, e a pergunta paralela guarda a resposta parcial.
- Os logs de erro / aviso do navegador estão vazios.

A fixture controlável serviu para validar com precisão a temporização da concorrência, sem depender da velocidade de saída do modelo real. Durante a depuração, duas verificações com o Worker em execução não formaram uma janela de concorrência válida (tarefa já encerrada / resposta terminou antes); após corrigir a fixture, foram refeitas e aprovadas; essas operações iniciais não são registradas como aprovação válida.

## Conversa real com o modelo

Primeiro sondou-se o `grok-4.6`, interface compatível com OpenAI `http://127.0.0.1:12580/tingly/openai`. Sondagem com HTTP 200, retornando o nome do modelo `grok-4.6` e `READY`, em 2,82 segundos. A primeira opção estava disponível, portanto não foram ativadas as cadeias de reserva Tingly `glm` nem Zhipu `glm-5.3`; esses dois serviços de reserva não foram validados desta vez.

| Cenário | Resultado real |
| --- | --- |
| Perguntar sobre ativo, objetivo e marcador durante a execução da sessão principal | Retornou `redhaze.top`, leitura da página inicial e objetivo de resumo, `BTW-REAL-0910`; pergunta paralela concluída, 16,97 segundos |
| Perguntar a base das ferramentas depois que a sessão principal concluiu a leitura da página inicial | Citou corretamente WebFetch 200, redirecionamento do curl 301 → 302 → 200 e o título da página; 7,24 segundos |
| Pergunta paralela pedindo para criar arquivo de teste com Bash | Recusou-se a executar, arquivo de destino não criado; 7,74 segundos |
| Pergunta paralela concluída não altera o contexto principal | O SHA-256 do transcript principal e os registros de atividade principal permanecem consistentes; o número de execuções de ferramenta da pergunta paralela é 0 |
| Parar / reiniciar de verdade o serviço Go e continuar perguntando | Preservou os 3 históricos paralelos anteriores, respondendo ativo, marcador e título direto do snapshot persistido, sem reexecutar o Agent principal |
| Nova sessão com configuração não streaming do Grok | Respondeu corretamente ativo e `ATOMIC-0910`; uso retornado e salvo: input 11734, output 138, cache_read 11520 |

A sessão principal do caso de ativo usou WebFetch e Bash/curl para ler a página inicial pública; a página de destino foi `https://id.redhaze.top/home`, com título "红幕科技 RedHaze Group · 全球综合集团门户". O Bash guardou a resposta em um arquivo de teste local; nada foi gravado no remoto. Esse fato foi verificado separadamente de "a pergunta paralela não executou ferramentas".

Valor de verificação do transcript principal: `e7e61f135a4a120954b539f357e8c4205d7d5cd7460dcaf3dc0fd066463e1d00`.

**Limitação de uso:** a resposta em streaming do Grok via Tingly não retornou usage. Enviando diretamente `stream_options.include_usage=true` para verificar: HTTP 200, 12 frames de dados, 0 frames de usage. Portanto o 0 nos testes de streaming significa que o endpoint não forneceu uso, e não deve ser interpretado como ausência de cobrança. O uso não streaming e o uso de falha / cancelamento da fixture foram salvos corretamente.

## Revisão do Qwen

Modelo de revisão `qwen-flash`, interface compatível com OpenAI `https://dashscope.aliyuncs.com/compatible-mode/v1`, HTTP 200. Foram fornecidas as três primeiras conversas paralelas reais, a base das ferramentas da sessão principal e as asserções de engenharia; retornou `verdict: accept`, `concerns: []`, considerando que as respostas são consistentes com as evidências de ativo, marcador e leitura da página, e que a recusa de ferramenta na pergunta paralela atende à restrição. Uso da revisão: prompt 6625, completion 312, total 6937.

Esta revisão do Qwen não cobre o reinício do serviço e o teste não streaming adicionados depois. A generalização do Qwen sobre "sem escrita" foi ampla demais: o curl da sessão principal de fato criou um arquivo temporário local de resposta, como já registrado acima. Concorrência, execução zero de ferramentas e isolamento do transcript são julgados pelas asserções de engenharia; a revisão do modelo apenas auxilia a avaliar a qualidade das respostas.
