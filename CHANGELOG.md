# Registro de alterações

As alterações relevantes deste projeto são registradas neste arquivo, em formato baseado no [Keep a Changelog](https://keepachangelog.com/pt-BR/1.1.0/).

## [Unreleased]

## [0.3.15] - 2026-10-07

### Interceptação

#### Novidades

- **Nova regra de interceptação embutida "caminhos de endpoints de exclusão"**: a regra HTTP destrutiva embutida até então só reconhecia o **método** DELETE, mas na maioria das aplicações o endpoint de exclusão pode ser acionado com GET/POST, e chamadas como `curl 'http://t/api/user/delete?id=1'` apagavam de verdade os dados do alvo. Agora há uma regra `deny` que cobre `/delete /del /remove /unlink /erase /destroy` (o verbo precisa ser seguido de um separador, então `/delivery`, `/details` e `/delta` não são bloqueados por engano). Instâncias já existentes também a recebem após o upgrade, e ela pode ser desativada ou removida em "Sistema → Interceptação de comandos".
- **O consumo de tokens da aprovação de fallback pelo modelo pode ser visto separadamente**: as chamadas de aprovação não tinham atribuição de medição própria, então só era possível estimar quanto custavam. Agora são medidas à parte, e abaixo do interruptor "Aprovação de fallback pelo modelo" em "Sistema → Interceptação de comandos" há um cartão de uso — estatísticas acumuladas de cinco itens (número de chamadas, entrada, saída, leitura e escrita de cache), além de um minigráfico de colunas com o consumo diário dos últimos 30 dias.

### Notificação de vulnerabilidades

#### Novidades

- **Achados de vulnerabilidades podem ser enviados para IM**: nova página "Sistema → Notificações push", que envia os achados das varreduras por seis canais: **DingTalk, Feishu, WeCom, Webhook genérico, Telegram e e-mail**. É possível configurar quantas instâncias quiser do mesmo tipo, cada uma com ativação/desativação, limite de taxa e regras de filtro independentes.
- **O momento do envio tem dois modos, em tempo real e resumido**: no modo em tempo real, cada achado é enviado assim que bate com as regras; no modo resumido, os achados são agrupados em uma única mensagem conforme o período global (padrão 30 minutos), que começa informando a quantidade de novos e a distribuição por severidade. Para fazer "alta severidade em tempo real, o resto resumido", crie dois canais e configure cada um.
- **As regras de filtro têm quatro dimensões**: severidade mínima, limitação por escopo de tarefa / ativo, palavras-chave de tipo de vulnerabilidade a incluir e a excluir (a exclusão tem prioridade) e se deve receber mudanças de status de tratamento (desativado por padrão). A mensagem traz um botão "Ver detalhes", cujo destino é definido pelo "endereço de retorno" global; se ficar vazio, o botão não aparece.
- **Histórico de entregas e reenvio manual**: lista o status, o número de tentativas, o motivo da falha e o canal de cada entrega, com filtro por canal e status; itens com falha podem ser reenviados com um clique (o reenvio zera o contador de tentativas).
- **Exibição mascarada das credenciais dos canais**: endereço do Webhook, chave de assinatura, Bot Token, senha SMTP etc. são exibidos apenas como valor mascarado com dica dos últimos caracteres; reenviar o valor sem alteração significa "não modificar", e esvaziar o campo remove esse campo.

#### Correções

> Esta seção reúne as conclusões de uma rodada de auditoria de segurança feita após a conclusão da primeira versão do recurso; cada item foi primeiro reproduzido, depois corrigido, e por fim recebeu teste de regressão.

- **Corrigido o bypass da máscara "alterar o endereço de destino mantendo a credencial" (crítico)**: a mesclagem de configuração preservava o valor antigo do banco para qualquer "chave não mencionada"; assim, bastava mudar o endereço e não dizer nada sobre a credencial para que o servidor enviasse a credencial real do banco para um endereço qualquer, de forma totalmente silenciosa. Agora, assim que o endereço de destino muda, cada campo de credencial precisa se posicionar explicitamente: informar um novo valor ou deixá-lo explicitamente vazio, indicando que não é mais necessário.
- **Corrigida a ausência total de escape de conteúdo não confiável nos canais baseados em markdown**: o título e o resumo da vulnerabilidade vêm da saída do modelo e a URL do ativo vem do resultado da varredura; um título construído podia ser renderizado como link externo clicável no DingTalk / WeCom / Feishu, e a sintaxe de imagem era buscada pelo cliente na renderização, vazando o IP de quem lê. Agora tudo é normalizado em linha única + escape dos metacaracteres de markdown, e o escape ocorre na saída de renderização de cada canal, e não na função de título compartilhada.
- **Corrigida a superfície de SSRF do endereço de entrega**: antes, endereços de loopback, de rede interna e de metadados de nuvem podiam ser entregues, e os primeiros 200 bytes do corpo da resposta de falha iam para `last_error` e eram exibidos no histórico de entregas, formando uma primitiva de leitura semicega. Agora a defesa é feita na **fase de discagem**, cobrindo DNS rebinding, e redirecionamentos entre hosts são recusados; endereços de loopback e link-local exigem definir explicitamente `ARTEX_NOTIFY_ALLOW_LOCAL=1`, e redes privadas RFC1918 continuam liberadas (relays SMTP internos são comuns).
- **Corrigido o vazamento de credenciais de push por mensagens de erro**: o `access_token` do DingTalk, a `key` do WeCom e o bot token do Telegram estão na URL, e em caso de falha de entrega a URL completa fluía para `last_error`, o histórico de entregas, o log do servidor e a mensagem do frontend. Agora tudo é sanitizado, mantendo apenas `scheme://host` e a causa subjacente.
- **Corrigida a perda silenciosa causada por marcar o lote inteiro como entregue após o truncamento de uma mensagem resumida**: as vulnerabilidades cortadas não estavam na mensagem nem na lista de falhas, mas o histórico de entregas mostrava sucesso. Agora o empacotamento é feito por **mensagem inteira**, marcando apenas as que couberam nela; as demais voltam à fila para serem enviadas na mensagem seguinte sem consumir tentativas, e o cabeçalho da mensagem informa quantas continuarão na próxima.
- **Corrigido o erro de digitação no limite de nível mínimo que invalidava o filtro silenciosamente**: quando `min_severity` era escrito errado, como `hgih`, o ordinal do nível desconhecido era 0 e a condição degenerava em sempre verdadeira; o usuário achava que receberia só alta severidade, mas todas as vulnerabilidades eram despejadas no grupo. Agora o valor é validado no caminho de escrita e as opções válidas são listadas.
- **Corrigida a inalcançabilidade de `rate_per_min = 0` (sem limite)**: na camada de escrita no banco, o 0 explícito era silenciosamente trocado pelo valor padrão, e o operador achava ter liberado o limite quando ele continuava em vigor. O valor padrão passou a ser preenchido na camada da interface quando o campo está ausente.
- **Corrigido o canal de resumo que contornava totalmente o token bucket**: `rate_per_min` não tinha nenhum efeito no modo digest. Agora a quantidade retirada é limitada ao mesmo tempo pela cota da rodada e pelo limite superior de memória.
- **Corrigido o tratamento de falha que decidia pelo máximo de tentativas do lote inteiro, fazendo entregas antigas arrastarem as novas**: uma entrega antiga já tentada duas vezes levava para failed, junto com ela, entregas novas do mesmo lote, e novas vulnerabilidades eram perdidas para sempre sem usar nem uma tentativa. Agora a decisão é tomada item a item.
- **Corrigida a marcação silenciosa como sucesso de entregas cujo snapshot não pôde ser interpretado dentro de um lote resumido**: esses itens eram ignorados na fase de renderização, mas contados como sucesso junto com o lote. Agora são explicitamente consideradas falha, com o motivo informado.
- **Corrigido o número de entregas por rodada de um único canal poder exceder a duração do lease**: em implantações com várias instâncias, linhas com lease expirado eram retomadas, reenviadas em duplicidade e tinham as tentativas incrementadas em dobro. Agora o limite por rodada é calculado de trás para frente a partir de "lease / timeout de uma tentativa", e há uma asserção que fixa a relação entre essas constantes.
- **Corrigido o truncamento no Telegram que podia cortar entidades HTML**: o truncamento só evitava tags cortadas ao meio, mas não fragmentos de entidades como `&amp`, e o parser podia por isso recusar a mensagem **inteira**. Agora ambos são evitados.
- **Corrigido o canal de e-mail que tratava falhas SMTP temporárias como permanentes**: respostas 4xx como a `450` do greylisting são recusas temporárias e deveriam ser tentadas de novo mais tarde, mas antes eram sempre dadas como mortas, e em servidores com greylisting todo push falhava logo na primeira tentativa. Agora a distinção é feita pelo primeiro dígito do código de resposta: 4xx pode ser tentado de novo, 5xx é falha permanente.
- **Corrigido o envio de estruturas aninhadas que gravava de verdade o literal da máscara no banco**: campos de objeto como `webhook.headers` só podem ser mascarados ou enviados por inteiro, e colocar a sentinela de máscara dentro do objeto fazia com que fosse armazenada como valor real, invalidando silenciosamente a autenticação. Agora esse envio é explicitamente rejeitado.

#### Notas de design

- **A transação que grava a vulnerabilidade faz apenas um INSERT cego**: `notification_events` e a vulnerabilidade são gravados na mesma transação, e o commit garante a consistência atômica de ambos. Esse INSERT deliberadamente não lê a tabela de canais nem executa as regras de filtro do usuário, e é envolvido por um `SAVEPOINT` — caso contrário, uma condição de filtro mal configurada poderia abortar a transação e impedir que uma vulnerabilidade de alta severidade fosse salva.
- **A entrega usa lease para retirar itens, e não uma transação longa**: depois de retirar com `FOR UPDATE SKIP LOCKED`, a linha é marcada como `sending` e `next_attempt_at` é empurrado para o futuro como lease, sem manter bloqueios do banco durante a entrega; linhas deixadas por uma queda do processo são retomadas após o lease expirar, curando-se sozinhas sem tentativas infinitas.
- **O limite de taxa não consome o orçamento de tentativas**: primeiro se calcula a cota da rodada pelo token bucket e depois se retira essa quantidade. Na ordem inversa, entregas barradas pelo limite contariam uma tentativa à toa, e o orçamento de três tentativas seria gasto só com espera, caindo em falha.

### Infraestrutura de testes

#### Correções

- **Corrigido o resíduo permanente de ativos causado pela ordem de limpeza do caso de teste de atribuição de ICP da empresa**: o `defer d.Close()` executa primeiro na saída da função e o `t.Cleanup` depois, de modo que todas as instruções de limpeza caíam numa conexão já fechada, com o erro descartado, e os ativos e empresas de teste ficavam para sempre no banco, ainda fazendo outros casos que afirmam contagem de ativos falharem sem motivo aparente. Agora o fechamento da conexão também passa por `t.Cleanup`, registrado antes, e as falhas de limpeza passam a aparecer.

### Tráfego

#### Novidades

- **A lista de tráfego passa a ter busca por conteúdo, filtros de caminho / código de status / tamanho e ordenação por coluna**: a linha de filtros permite filtrar por **conteúdo da resposta** (via índice de texto completo), **caminho**, **código de status** (valor exato ou faixas como `2xx`) e **faixa de tamanho da resposta**, e o cabeçalho da tabela ordena por horário, código de status e tamanho da resposta, com a preferência de ordenação guardada localmente. Dois índices novos foram adicionados e entram em vigor automaticamente em instâncias existentes após o upgrade; o contrato de query das ferramentas do Agent não foi alterado.
- **A lista de tráfego ganha "Limpar tudo"**: apaga de uma vez todos os registros de tráfego (ignorando o filtro atual) e limpa os diretórios de host históricos que o índice já não registra; após a limpeza, faz também uma compactação completa e informa quanto espaço foi realmente liberado. As evidências de tráfego já vinculadas a vulnerabilidades ficam em um banco de evidências independente e não são afetadas.

#### Correções

- **Corrigido o espaço em disco não ser liberado depois de apagar tráfego**: o banco de índices era criado sem `auto_vacuum`, então o arquivo nunca encolhia; e o índice de texto completo é `contentless_delete`, em que o `DELETE` só grava tombstone e não recupera os postings originais — apagar tráfego até fazia o índice crescer (medido: capturar 6MB de corpo → índice de 16MB, e continuava 16MB depois de apagar tudo). Agora os bancos de índice novos habilitam `auto_vacuum=incremental`, e após cada exclusão a fusão do índice de texto completo e a recuperação incremental são executadas em blocos em segundo plano, devolvendo o espaço ao sistema aos poucos (no mesmo cenário, caiu para 104KB); a recuperação libera o bloqueio de escrita entre os blocos, sem bloquear a gravação de tráfego.

> Nota de upgrade: o `auto_vacuum` só pode ser definido na criação do banco, portanto **os bancos de índice de instâncias existentes continuam no modo antigo**, e a recuperação incremental neles é uma operação nula (uma linha de aviso é impressa na inicialização). Nesses bancos, o crescimento dos tombstones já foi contido após o upgrade, e o volume já ocupado pode ser recuperado de uma vez com "Limpar tudo" — esse passo converte o banco para o modo de recuperação incremental, e as exclusões do dia a dia passam a funcionar sozinhas.

### LLM

#### Correções

- **Corrigido o cabeçalho de sessão personalizado não valer no caminho de "chamada LLM avulsa"**: a decomposição de objetivos (rodada 0) e a compactação de nós frios são chamadas avulsas sem transcript store, o contexto não tem session id e, portanto, o lado do gateway não consegue ler esse cabeçalho — em endpoints como o opencode zen, que retornam 400 direto quando falta o cabeçalho, o sintoma era "a rodada 0 falha, mas as rodadas seguintes do planner funcionam normalmente". Agora esses dois caminhos recebem explicitamente um session id estável por exploração, o que também permite atribuir corretamente o uso de tokens à exploração correspondente (antes não era contabilizado de forma alguma).

### Ferramentas

#### Correções

- **Corrigido o truncamento de saída de MCP e de ferramentas personalizadas que não valia, com resultados grandes inundando o contexto**: o retorno das ferramentas MCP antes não passava por truncamento algum, e as ferramentas personalizadas do tipo HTTP, por receberem um contexto de ferramenta vazio, usavam sempre 6000 caracteres fixos no código, em vez do limite configurado na sessão. Agora os dois caminhos passam por `actool.Capture`: truncam conforme o `MaxOutputChars` da sessão (padrão 30000) e, quando `ToolOutputDir` está configurado, despejam a saída completa em disco, deixando na mensagem só o trecho inicial e um ponteiro para o arquivo.
- **Atualizado o norma para a v0.4.3**: a camada harness passa a executar `CaptureOnce` de forma unificada, de modo que nenhuma ferramenta (inclusive as futuras) volte a estourar o contexto por esquecimento de ligar o truncamento.

### Implantação

#### Correções

- **Corrigido o fluxo de atividades que não conectava em implantações com proxy reverso**: o endereço SSE era montado incondicionalmente como `${hostname}:8787`; em implantações com proxy reverso na 443, a página carregava de `https://domínio/` mas o SSE era apontado para a porta 8787, não exposta na rede pública, e o sintoma era o fluxo de atividades reconectando repetidamente. Agora há distinção por `NODE_ENV`: em produção o padrão é mesma origem, e só o `next dev` mantém o `:8787` para contornar o buffering do Next.js sobre SSE; o ponto de sobrescrita `NEXT_PUBLIC_SSE_BASE` foi mantido. O README ganhou instruções de implantação com proxy reverso.

### Achados

#### Correções

- **Corrigida a falta de barra de rolagem na lista de ativos da visão "por ativo" quando ela transborda**: o cartão externo só tinha `max-height` e nenhuma altura definida, então a altura em porcentagem do viewport de rolagem não era resolvida e, com muitos ativos, a lista ou estourava o cartão ou era cortada sem poder rolar. Agora o limite de altura é aplicado diretamente ao contêiner de rolagem nativo da árvore de ativos, adaptando-se à altura da janela.

### Banco de dados

#### Correções

- **Definido um limite para o pool de conexões**: o `database/sql` por padrão não limita o número de conexões que a própria aplicação pode abrir, e quando não há conexão ociosa no pool ele cria uma nova sem condição, subindo até o `max_connections` do PostgreSQL (padrão 100) para só então ser recusado. Agora o teto é de 32 conexões, e consultas excedentes passam a esperar uma conexão ociosa, de modo que, sob a mesma carga, o sintoma é lentidão e não erro; se o `max_connections` já tiver sido reduzido, é preciso reduzir este valor também.
- **Maior reaproveitamento de conexões**: o padrão de `MaxIdleConns` é 2, e conexões acima de 2 simultâneas eram fechadas após o uso, exigindo refazer o handshake de TCP e autenticação do PostgreSQL na próxima requisição. Agora o limite de ociosas foi elevado ao mesmo valor do limite total, com vida útil de conexão de 30 minutos e descarte por ociosidade de 5 minutos, para que o pool possa substituir naturalmente conexões ruins de longa duração.

### Contribuidores

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.14] - 2026-09-24

### Lista de tarefas

#### Novidades

- **Nova coluna "Vulnerabilidades" na lista de tarefas**: mostra a quantidade por faixa de severidade (crítica / alta / média / baixa), com as faixas diferentes de zero coloridas conforme a severidade, para ver de relance o volume e a distribuição de vulnerabilidades de cada tarefa.

#### Alterações

- **As colunas "Descrição / Objetivo" ficaram mais estreitas**: conteúdos longos são exibidos truncados e o texto completo aparece ao passar o mouse, reduzindo o espaço horizontal da lista tomado por textos longos.

### Grafo de exploração e visão geral da situação de planejamento

#### Alterações

- **Visão geral da situação de planejamento (graph_overview) simplificada, com limites de quantidade em cada lista**: fatos recentes, intenções encerradas, intenções pendentes, resumo da zona fria e detalhes de vulnerabilidades confirmadas passam a ser "janela mais recente + contagem de reserva", e o que não é exibido pode ser consultado sob demanda; o volume de contexto de LLM por rodada cai de forma significativa, evitando que o contexto cresça demais em tarefas longas (a zona fria da visão geral de tarefas relacionadas também foi limitada).

#### Correções

- **Eliminada a aresta reversa duplicada entre digest e finding no grafo de exploração depois de recolher nós frios**.

### Contribuidores

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.13] - 2026-09-19

### Interceptação de ativos

#### Novidades

- **Nova regra global de interceptação de ativos (lista de bloqueio)**: permite cadastrar domínios / IPs / URLs com correspondência exata e aproximada, além de faixas CIDR, com criar, listar, alterar e excluir regras e ativar/desativar; a página de gerenciamento fica em "Sistema → Interceptação de ativos"; por padrão vem embutida a interceptação aproximada de sites governamentais (`.gov` / `.gov.cn`) e educacionais (`.edu` / `.edu.cn`).
- **A interceptação de ativos entra na cadeia de execução**: antes de despachar intenções (`add_intent`) e inserir ativos (`insert_assets`), o Agent faz o julgamento de interceptação dos ativos alvo — intenções que batem com a interceptação não são despachadas, ativos que batem não são inseridos, e as informações do ativo e o motivo da interceptação são devolvidos ao Agent.
- **Novas regras de interceptação / permissão (lista de permissão) em nível de tarefa**: independentes das regras globais e válidas só para esta tarefa, com ordem de julgamento "primeiro interceptar, depois permitir" — bateu na interceptação, está proibido; não bateu na interceptação mas a tarefa tem regras de permissão configuradas e nenhuma bate, então "teste não permitido"; sem regras de permissão configuradas, a lista de permissão não é ativada. Podem ser cadastradas ao criar a tarefa, ou criadas, alteradas, removidas e ativadas/desativadas em "Visão geral" nos detalhes da tarefa.
- **Os modelos de tarefa passam a suportar categoria predefinida e regras de interceptação/permissão em nível de tarefa**: um modelo pode guardar a categoria da tarefa e um conjunto de regras em nível de tarefa, trazidos juntos para o formulário de nova tarefa ao aplicar o modelo.

### Revisão de operações

#### Correções

- **Reforçado o protocolo de saída do juiz do modelo, reduzindo liberações indevidas por truncamento**: o limite da explicação do veredito (comment) caiu de 500 para 120 caracteres chineses e passou a exigir "saída apenas em JSON, sem preâmbulo nem bloco de código", evitando que o veredito seja truncado por `MaxTokens`, fique impossível de interpretar e acabe liberado pela política de falha do modelo (fail-open).

### Arquivamento de tarefas

#### Correções

- **O arquivamento pula links simbólicos em vez de falhar o pacote inteiro**: o formato de arquivamento suporta ponta a ponta apenas arquivos comuns e diretórios, e antes qualquer link simbólico no diretório de trabalho fazia todo o arquivamento da tarefa falhar; agora o link simbólico é pulado e registrado em log, e os demais arquivos são arquivados normalmente (sem seguir links nem sair da árvore de diretórios).

### Conta e conformidade

#### Novidades

- **Novo pop-up "Termos de uso e isenção de responsabilidade" antes do login**: é preciso marcar a concordância para poder entrar.

### Licença e dependências

#### Alterações

- **O projeto adota a licença de código aberto AGPL-3.0**, e a seção de licença e isenção de responsabilidade do README foi aprimorada.
- **Atualizado o norma para a v0.4.1**.

### Contribuidores

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.12] - 2026-09-17

### Painel de transmissão da cadeia de exploração

#### Novidades

- **Novo "Painel de transmissão da cadeia de exploração" na página de detalhes da tarefa** (#144): apresenta os nós de exploração da tarefa (origem/objetivo/intenção/fato/vulnerabilidade/dica/compactação) em visão de linha do tempo, com filtro por tipo, busca por palavra-chave, ordem crescente/decrescente, paginação e atualização automática; na "primeira página com os mais recentes primeiro" fica na posição ao vivo, atualizando a cada rodada, e ao sair dessa posição apenas acumula a contagem de não lidos, sem perturbar a leitura atual, com o retorno em um clique por "N novas transmissões · voltar ao mais recente". Agrupado por dia, de modo que mesmo ao paginar tarefas longas se reconhece "de que dia é isso".
- **O painel de transmissão mostra o id do nó em cada linha** (#145), facilitando comparar com o grafo da cadeia de exploração e localizar um nó específico.
- **A expansão de detalhes do nó no painel mostra upstream/downstream e ativos ancorados** (#147): ao expandir uma transmissão, veem-se as relações upstream (de onde veio) / downstream (o que foi gerado) do nó, e ao passar o mouse sobre um item relacionado aparece o cartão do nó correspondente (tipo/status/origem/horário/resumo/trecho do payload); também são listados os ativos ancorados ao nó (rótulo de tipo + texto identificável). Os dados relacionados descem junto com a página de transmissão, então a expansão não faz requisição extra.
- **A busca do painel de transmissão suporta filtrar por id do nó** (#150): além de "conteúdo / origem", a caixa de busca ganhou correspondência por id do nó; digitar só o número ou a forma "#41" exibida na interface localiza o nó exato.

### Gerenciamento de intenções

#### Novidades

- **A exclusão de intenções suporta dois modos, "exclusão falsa / exclusão real"** (#149): intenções pendentes, em execução e pausadas podem ser excluídas, sendo preciso informar o motivo; o modo de exclusão é escolhido na janela de confirmação.
  - **Exclusão falsa (padrão)**: a intenção passa para "Excluída" e o motivo é gravado em campo próprio, mantendo o nó da intenção com toda a sua produção e linhagem.
  - **Exclusão real**: remove fisicamente a intenção e os nós descendentes exclusivos "sustentados apenas por ela" (em cascata pela cadeia de produção / intenções até as folhas), evitando deixar dados órfãos; a medição de Token da intenção excluída é arquivada e preservada pela data original; nós compartilhados (ainda referenciados por outras intenções), objetivos e fatos raiz da tarefa são sempre preservados, e a janela de exclusão real mostra o número estimado de nós afetados em cascata.

  Ambos os modos notificam o planejador de que "a intenção foi excluída pelo usuário + motivo" e ele replaneja com base nisso.

### Revisão de operações

#### Novidades

- **O registro de aprovações suporta filtro por status e origem do veredito** (#139): é possível filtrar os registros por status de aprovação e origem do veredito, localizando rapidamente o registro desejado.

#### Correções

- **Solicitações de aprovação pendentes carregam independentemente da paginação do histórico** (#133): as solicitações pendentes deixam de ser afetadas pela paginação da lista de histórico e permanecem totalmente visíveis ao percorrer o histórico.

### Agent

#### Alterações

- **A descrição do papel do Worker passou a uma formulação genérica de plataforma de cibersegurança** (#138).

#### Correções

- **Corrigida a compactação de nós frios que nunca disparava**: o Compactor foi ligado ao planejador que roda por tarefa; a compactação de nós frios (cold-digest) não era executada por não estar conectada, e agora foi restabelecida.

### Chat

#### Novidades

- **O chat suporta @ referência de registros de vários tipos e paginação por rolagem** (#135): no chat é possível @ referenciar registros de vários tipos, e os candidatos de referência carregam por paginação ao rolar.

#### Correções

- **Balões de mensagem longos ficam contidos no painel da sessão** (#137): balões de mensagem muito longos deixam de transbordar da área da sessão.
- **Corrigido o erro "arquivo de upload ausente" ao enviar arquivo antes de a nova conversa ser criada**: a seleção de arquivos do Composer transforma o `FileList` em array antes de chamar o callback, e só então limpa o input; antes, no estado de rascunho era preciso criar a sessão de forma assíncrona e, ao retomar a execução, o `FileList` ligado ao vivo ao input já tinha sido esvaziado, fazendo o upload ficar sem o campo de arquivo e o backend retornar 400.

### Tráfego

#### Correções

- **O proxy de gravação de tráfego escuta por padrão apenas em 127.0.0.1** (#129, #130): evita que, na configuração padrão, um proxy aberto fique exposto nas demais interfaces de rede.

### Web

#### Correções

- **Corrigida a perda do cabeçalho global na lista de tarefas da versão de exportação estática**.
- **Completado o mock de tráfego relacionado na página de detalhes de vulnerabilidade da demo, corrigindo a tela branca**.

### Contribuidores

- [@Autumn-27](https://github.com/Autumn-27)
- [@RuoJi6](https://github.com/RuoJi6)
- [@dingpotian](https://github.com/dingpotian)

## [0.3.11] - 2026-09-15

### Revisão de operações

#### Novidades

- **O registro de aprovações suporta localizar a origem** (#125): clicar em "Origem" no registro de aprovação volta à execução de ferramenta que disparou aquela aprovação — abre a sessão completa original, pagina automaticamente até a posição alvo, expande no local o comando e o resultado e os destaca centralizados na área de rolagem, mantendo legíveis as mensagens vizinhas; quando o usuário rola manualmente, a correção automática para. Em sessões de conversa comum, Worker de tarefa, planejador e segmentos do Agent principal, a localização é exata por meio do `tool_use_id` persistido e do mapeamento de tarefa (com novo índice de IDs de chamada no escopo); se estiver ausente, duplicado ou a relação for ambígua, mostra um aviso explícito em vez de saltar para outra execução. Quando a sessão foi excluída ou a tarefa arquivada / o registro está ausente, é exibido um aviso claro.
- **O registro de aprovações suporta paginação** (#115).

#### Alterações

- **Entrada da revisão pelo modelo simplificada** (#124, #125): a entrada da revisão pelo modelo (o juiz LLM depois que as regras não bateram) passa a ser um JSON versionado com "esta chamada de ferramenta completa + um contexto curto escolhido explicitamente + o diretório de trabalho da máquina local". O contexto usa só mensagens reais do usuário (a mensagem atual do usuário no chat / Agent principal da tarefa); o Worker deixa de anexar o resumo da intenção e também limpa o contexto herdado do Agent superior; o planejador e as sessões disparadas automaticamente não fabricam mensagem de usuário. Deixam de ser anexados a descrição da tarefa, os objetivos, as restrições de operação, a situação global da exploração, as chamadas históricas e a intenção completa do Worker — eles continuam sendo usados, pela lógica original, na execução do Agent e na auditoria de sessão independente, apenas não entram na revisão de operações. Todo veredito deve produzir `decision`/`comment` em JSON, e a explicação precisa incluir "a operação real, a consequência em caso de sucesso e a regra atingida"; instruções nos parâmetros ou no contexto não podem alterar a política de revisão. O snapshot da entrada realmente enviada ao modelo e sua impressão digital são salvos; snapshots de versões antigas são preservados e marcados com a versão, sem fabricar entradas antigas com dados atuais.

#### Correções

- **Veredito do modelo envolto em bloco de código deixa de ser liberado silenciosamente** (#126): quando o JSON de veredito devolvido pelo modelo estava envolto em um bloco de código ```, a falha de interpretação antes acabava, pela política de falha do modelo, em liberação silenciosa; agora as cercas do bloco de código são removidas antes da interpretação, e só se a interpretação ainda falhar é aplicada a política de falha configurada.

### Agent

#### Novidades

- **Nova compactação experimental de contexto noa** (norma atualizado para a v0.4.0): recurso experimental ativável nas configurações do sistema, desativado por padrão. Quando ativado, o noa (compactação de contexto conduzida por modelo) assume a compactação de contexto dos quatro tipos de Agent — principal, planejador, Worker e chat — substituindo a compactação embutida; o texto original compactado é arquivado de forma persistente, concentrado em `<workDir>/noa/<ID da sessão>/` (e não espalhado pelos diretórios das tarefas), em subdiretórios globalmente únicos por ID de sessão. Se a integração falhar, volta automaticamente à compactação embutida sem interromper tarefas reais; o interruptor é lido uma vez por execução, e alterná-lo afeta apenas as execuções iniciadas depois.

#### Correções

- **As ferramentas do grafo de exploração recusam, em vez de dar crash por nil, quando não há contexto de tarefa**: ao chamar ferramentas relacionadas ao grafo de exploração fora de um contexto de tarefa, é devolvido um erro claro, sem mais crash por desreferenciar um store vazio.

### MCP

#### Novidades

- **Suporte a serviços MCP SSE legados** (#117): compatível com servidores MCP que oferecem apenas o transporte SSE antigo.

### Tráfego

#### Correções

- **A busca de tráfego passa a considerar a porta ao casar o host dos registros** (#114): o casamento de host de `traffic_search` passa a incluir a porta, evitando interferência entre registros do mesmo host em portas diferentes.
- **Um erro na migração da descrição de `traffic_search` não interrompe mais as migrações seguintes do reporter**: falhas de uma única migração são isoladas, sem afetar a execução das migrações posteriores.

### Web

#### Alterações

- **Adicionado divisor entre as opções de vulnerabilidades do reteste de tarefa** (#122): foi adicionada separação entre os itens de seleção de vulnerabilidades do reteste, deixando o visual mais claro.

#### Correções

- **Corrigido o crash da página inteira de detalhes de tarefa da demo**: no modo mock, a aba "Sessões" dos detalhes da tarefa busca `GET /api/tasks/<id>/side-questions` (histórico de perguntas paralelas), mas o handler de mock não tinha essa rota, e o fallback de leitura, pela regra "caminho que termina em s é coleção", devolvia `[]`, fazendo `data.items` ser `undefined`, e o `merge()` do hook de perguntas paralelas lançava `TypeError: t is not iterable` ao iterá-lo; essa exceção ocorria dentro do updater de `setItems`, era adiada pelo React para a fase de render e relançada, o `catch` do chamador não a capturava, e a página inteira era assumida pelo error boundary exibindo "This page couldn't load". Agora o handler de mock devolve explicitamente um histórico de perguntas paralelas vazio, e `sideAPI.history` também normaliza o valor retornado (qualquer `items` que não seja array vira `[]`) como defesa em profundidade.

### Contribuidores

- [@Autumn-27](https://github.com/Autumn-27)
- [@RuoJi6](https://github.com/RuoJi6)

## [0.3.10] - 2026-09-13

### Rede

#### Novidades

- **Nova fonte oficial DeepSeek na busca na web**: reaproveita diretamente a configuração de LLM atualmente ativa. Ela é diferente das outras três fontes — o DeepSeek não tem uma interface de busca chamável diretamente, e a busca só existe dentro da sua interface compatível com Anthropic (a ferramenta de servidor `web_search_20250305`), executada no servidor do DeepSeek. Por isso ela **só suporta o endpoint oficial do DeepSeek + protocolo anthropic** (endpoints de protocolo OpenAI recusam diretamente ferramentas de servidor), e cada busca **consome uma chamada extra de modelo**, a requisição **não passa pelo proxy de saída da busca**, **não entra no registro de tráfego**, e o resultado retornado **traz só título e link** (sem resumo; quando o corpo for necessário, o WebFetch o busca). A página de configurações explica essas limitações, mas **não bloqueia com validação**: cabe ao usuário confirmar se são atendidas, e o botão "Testar busca" permite executar uma vez para verificar.

### Agent

#### Novidades

- **O worker ganha a capacidade de consultar outros works**: novos `search_all_worker_traces` (sem precisar saber antes o intent_id, busca passos por palavra-chave globalmente no processo de execução de todos os works desta tarefa) e `get_worker_trace` (depois de fixar um work, lista o fluxo de passos, busca por palavra-chave no local e obtém o conteúdo completo por step_id), para reaproveitar observações que outros works viram mas não escreveram em fact e evitar trabalho repetido.
- **`node_detail` foi delegado ao worker**: junto com as ferramentas de consulta acima, de posse do intent_id / id do nó o worker pode consultar diretamente os detalhes completos daquele nó.
- **A rodada de planejamento disparada por `add_hint` passa a ser anunciada explicitamente**: antes, um novo hint só era incorporado à visão geral da situação para o planejador descobrir sozinho; agora um `add_hint` registra para o planejador um gatilho "o humano adicionou N dicas estratégicas: …" (um lote conta como um, sem inundar linha a linha), o planejador é avisado claramente de que "esta rodada foi disparada por novo hint" e vê diretamente o conteúdo das dicas.

#### Alterações

- **A dica de situação global passou a um tom mais "solto"**: incentiva a exploração divergente e o relato oportuno de pistas entre intenções, em vez de convergir cedo demais.
- **Redação das fronteiras do worker simplificada**: o primeiro bloqueio não significa que algo foi esgotado, com foco em "esgotar os desvios possíveis dentro desta intenção antes de concluir".
- `insert_assets` removeu o parâmetro `related` de cada ativo: sua única função era decidir se o ativo entraria no escopo desta tarefa, mas o valor não era gravado (se o mesmo ativo fosse registrado de novo, perdia a validade, e na interface não se via quem foi considerado irrelevante), servindo na prática apenas para o modelo fazer um julgamento a mais que não ficava retido.
- **O escopo de teste (`task_scope`) deixa de ser afetado pelo interruptor de cobertura de ativos**: a entrada automática no escopo (`source='auto'`) de `insert_assets` é executada com a cobertura ligada ou não, e `add_task_scope` também é sempre fornecido ao planner / Agent principal da tarefa / Agent de decomposição de objetivos. O escopo é a fronteira de autorização da tarefa e a base de filtro das consultas de ativos; o interruptor de cobertura só decide se ele é usado como denominador para calcular métricas, e não deveria decidir se o próprio escopo é acumulado. Antes, desligar a cobertura invalidava ao mesmo tempo os dois caminhos de escrita `auto`/`agent`, e o `task_scope` ficava só com as linhas adicionadas manualmente na interface. `list_untested_assets` continua oculto quando a cobertura está desligada (ele próprio é uma visão puramente de cobertura).

#### Correções

- **Corrigida a mistura de ativos entre tarefas** (#59): `list_assets` antes fixava o id da tarefa em 0 e consultava às cegas todo o banco compartilhado de ativos, e o modelo não tinha como escrever uma condição de filtro por escopo, então tomava ativos de outras tarefas (em especial IPs) como alvos desta tarefa, desviando a direção dos testes. Agora ele só retorna ativos dentro do escopo de teste (`task_scope`) de [esta tarefa e das tarefas diretamente relacionadas], com correspondência por **pertencimento** e não por valor literal: com um domínio raiz no escopo, encontram-se todos os subdomínios/serviços/interfaces sob ele; com uma faixa de rede, os hosts e serviços dentro dela; buscar por id um ativo fora do escopo também não retorna nada. Em contextos que não são de tarefa (Auto/pentest), sem escopo em que se basear, continua voltando ao banco inteiro. Também corrigido o ponto cego de pertencimento em que hosts de IP direto (como `http://1.2.3.4/api`) não eram alcançados pelo escopo de faixa de rede. A visão "Ativos de teste" da UI (filtrada pelo produtor da tarefa) não é afetada.
- **Corrigida a remoção indevida das ferramentas de consulta entre works do worker**: depois de `search_all_worker_traces` / `get_worker_trace` (e `node_detail`) serem adicionados ao conjunto padrão de ferramentas do worker, uma migração antiga de "reduzir a superfície de ferramentas do worker" as desvinculava de novo na inicialização, e o worker acabava sem elas. Elas foram tiradas da lista de desvinculação, e para bancos que já tinham rodado a migração antiga é feita uma revinculação única ao worker.
- **Corrigida a falha ocasional ao gravar perguntas paralelas `/btw`**: antes de gravar o checkpoint de side_question, remove-se o escape NUL (`\u0000`) que o JSONB não suporta, evitando erro de gravação de conteúdo que contenha esse caractere.

### Gatilhos

#### Correções

- **O gatilho de sessões mescladas deduplica a descrição/objetivo da tarefa por tarefa**: no gatilho mesclado de várias tarefas, a descrição/objetivo da mesma tarefa não é mais concatenada repetidamente na mensagem, evitando que objetivos longos empilhados repetidamente estourem o contexto.

### Contribuidores

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.9] - 2026-09-11

### Agent

#### Novidades

- O Agent principal dentro da tarefa suporta **várias sessões**: é possível criar, alternar e reiniciar o contexto de forma independente, e cada sessão é interativa.
- Suporte a **perguntas paralelas** `/btw` persistentes: perguntas de acompanhamento sem interromper a linha principal, com perguntas e respostas gravadas no banco e preservadas após reinício.
- `spawn_task` ganhou `source_task_ids`, e a nova tarefa pode **herdar, somente leitura,** os ativos e achados das tarefas de origem.

#### Alterações

- Desativada a memória entre engagements do worker e reduzida sua superfície padrão de ferramentas: ler contexto e revisar entre works são responsabilidades do planejamento; o worker cuida apenas da execução e do write-back de uma única intenção.
- Prompt padrão do worker simplificado; `get_worker_output` removeu os campos `terminated` e `worker_name`; simplificadas as descrições de `insert_assets` / `list_assets`.

#### Correções

- Corrigida a interrupção prematura de um work por **turno ocioso**: às vezes o modelo gasta uma rodada inteira só produzindo raciocínio, sem dar corpo de texto nem chamar ferramenta; o que o harness vê é um término natural (`end_turn` sem `tool_use`) e encerra direto com `completed` + resumo vazio — uma intenção ainda não concluída é cortada no meio, e o sintoma é "a tarefa terminou normalmente, mas sem nenhum resumo em texto". Nenhuma das cinco camadas de retry de LLM a alcançava: não é um erro, a entrada do retry em janela segura do mesmo provider é a "falha de stream", o disjuntor dá sucesso direto quando `err == nil`, e o reexecutar de intenção só reconhece `model_error`; o retry de resposta vazia do SDK também não a alcançava, porque essa camada julga vazio por "se algum evento foi emitido", e os próprios deltas de raciocínio são eventos. Agora o hook Stop do work reconhece esse tipo de turno e injeta uma instrução de continuação, para o modelo seguir para o próximo passo com o raciocínio já produzido — deliberadamente não se escolheu "reenviar como está", porque esse tipo de ociosidade costuma ser determinado pela forma do prompt e do contexto, é um comportamento estável e não um capricho aleatório, e reenviar só faria o modelo pensar tudo de novo.
- O número de vezes reaproveita a **contagem de retry de resposta vazia** em "Retry e backoff" da página LLM (ambos respondem a "o modelo terminou, mas não produziu conteúdo substancial", apenas com critério e meio diferentes), padrão 2, e preencher `-1` o desliga, voltando ao comportamento anterior de "ociosidade encerra". É um limite do **total** de uma intenção, e não de vezes consecutivas: o próprio harness já limita a empurrar uma vez a ociosidade consecutiva, e a cota só é renovada quando de fato ocorreu uma rodada de ferramentas, então este número barra ciclos do tipo "ferramenta → ociosidade → empurrão → ferramenta → ociosidade" que esgotariam o orçamento da intenção. Quando dispara, o log registra `[work <worker> · #<intenção>] turno ocioso…instrução de continuação injetada (n/N)`. Para o design, veja `docs/LLM重试设计.md` §1.1.
- Corrigidos o orçamento de contexto e o layout de entrada do `/btw` em conversas longas; em contextos não seguros (acesso sem HTTPS), o ID da requisição paralela é gerado de forma degradada.

### Tráfego

#### Novidades

- Vulnerabilidades suportam **vincular várias evidências de tráfego**, com ordenação, observações e distinção por papel (requisição/resposta/corroboração).
- O Agent de relatório **vincula automaticamente** as evidências de tráfego relacionadas antes de escrever o relatório.
- Novo **interruptor de vínculo de tráfego** do Agent, e completada a passagem de evidências entre Agents.

### Tarefas

#### Novidades

- Suporte a **reteste de vulnerabilidade em nível de sessão**: o reteste roda em uma sessão de Agent independente, e a lista e os detalhes exibem o estado de execução.

### Interceptação

#### Novidades

- Os registros de aprovação ganham **detalhes e auditoria de execução**: é possível ver o contexto da requisição da ferramenta, o veredito inicial do modelo/regra, a saída da execução e a impressão digital dos parâmetros.

### Ativos

#### Novidades

- Os ativos de teste da tarefa suportam **busca em DSL**.

### LLM

#### Correções

- Corrigido o teste de conexão que não reenviava o cabeçalho de sessão personalizado, fazendo o opencode zen retornar 400.

### Rede

#### Novidades

- O transporte HTTP do MCP suporta **pular a validação do certificado TLS**, facilitando a conexão com serviços de certificado autoassinado.

### UI

#### Novidades

- A lista de sessões é agrupada por Agent, com expandir/recolher e fixação independentes; a conversa suporta filtro por Agent.
- O grafo da cadeia de ataque renderiza nós compactados (digest) e recolhe seus membros.

#### Correções

- Corrigida a tela branca causada por credenciais de login dessincronizadas.
- O texto do aviso de interceptação passou a indicar explicitamente "controle da plataforma", evitando ser lido por engano como uma defesa do lado do alvo.

### Implantação e atualização

#### Novidades

- Suporte à **atualização com um clique pela página**: a página de configuração do sistema ganhou o cartão "Versão e atualização", e a barra superior destaca um aviso quando há nova versão. O fluxo é baixar o pacote de release → validar o `SHA256SUMS` → smoke test → staging → sair e deixar o script daemon subir de novo para concluir a troca, com a página recarregando sozinha.
- Novos scripts daemon de inicialização `start.sh` / `start.bat` como entrada oficial de inicialização (já incluídos nos pacotes de release e nas imagens Docker, com o `install.sh` inalterado), que decidem pelo código de saída se devem subir de novo e encaminham o SIGTERM ao artex. A lógica de validação e troca fica toda em Go, e os scripts permanecem bem simples.
- Proteção automática em caso de falha: se a validação ou o smoke test não passar, o pacote é descartado e a versão atual continua rodando; se a nova versão falhar em 3 inicializações seguidas, faz rollback para a versão anterior. A página de configurações também tem rollback manual (atenção: a estrutura do banco de dados não é revertida).
- A atualização só aceita domínios do GitHub e força HTTPS, e a fonte de release não é configurável; builds de desenvolvimento desativam a atualização com um clique. O resultado da consulta ao GitHub fica em cache por 30 minutos, para que o aviso da barra superior não esgote a cota da API.

#### Limitações conhecidas

- No Docker só o programa é trocado, não a imagem: as ferramentas não são atualizadas, e recriar o contêiner volta à versão que vem na imagem; quando necessário, continue usando `docker compose pull artex`.
- Não sincroniza o `skills/` do pacote de release, então os novos skills embutidos adicionados na nova versão não entram em vigor automaticamente.
- Atualizar é reiniciar, o que interrompe as tarefas em execução.

### Dependências

#### Alterações

- Atualizado o norma para a v0.3.7.

### Contribuidores

- [@Autumn-27](https://github.com/Autumn-27)
- [@RuoJi6](https://github.com/RuoJi6)

## [0.3.8] - 2026-09-09

### LLM

#### Novidades

- A página LLM ganhou a aba "Retry e backoff": tanto o **número** quanto o **intervalo** das cinco camadas de retry são configuráveis. A falha de uma chamada de modelo passa, de dentro para fora, por retry de conexão (SDK, reset de conexão / timeout / 429 / 5xx antes de o stream começar), retry de resposta vazia (SDK, término normal sem nenhum conteúdo, apenas formato openai), retry em janela segura do mesmo provider (replay de stream interrompido antes de qualquer saída ser entregue ao chamador), disjuntor de rodízio (falhas consecutivas até o limite entram em resfriamento e são puladas) e reexecução de intenção (a intenção inteira do worker é reexecutada depois de terminar com model_error) — as camadas internas se esgotam antes de chegar às externas. Cada camada tem dois botões, com semântica uniforme: vazio = usa o número padrão original e o backoff exponencial; preencher o número usa esse número; preencher o intervalo troca o backoff exponencial por intervalo fixo; preencher -1 = desliga o retry dessa camada. As três primeiras camadas acompanham o endpoint, e em cada configuração de modelo é possível sobrescrever campo a campo o padrão global (quem fixa só o intervalo ainda herda o número global); o disjuntor e a reexecução de intenção têm semântica de nível de processo, com uma única cópia global. Entra em vigor a quente após salvar, sem reiniciar. Tudo vazio equivale ao comportamento atual, e bancos antigos após o upgrade ficam byte a byte idênticos (coluna nova com padrão 0, chave de settings inexistente = tudo padrão). O teste de conexão deliberadamente não leva esses parâmetros — ele tem timeout rígido de 30s, e somar retries configurados pelo usuário só faria um endpoint utilizável falhar por timeout no teste. Para o design, veja `docs/LLM重试设计.md`.
- Cada configuração de LLM suporta **cabeçalho de sessão** personalizado (`session_header_key`): quando não vazio, toda requisição LLM leva esse cabeçalho HTTP, cujo valor é o session id da execução atual (`conv-<id>` para sessões de chat, `exp<x>-worker-i<intent>` para worker etc.), para gateways que usam cabeçalho de session-id para cache de prompt / roteamento sticky. Na implementação, o session id é lido do context da requisição e injetado, sem alterar o norma, e um mesmo provider compartilhado também consegue enviar valores de cabeçalho diferentes por sessão. Bancos antigos recebem migração, e o diálogo de configuração de LLM do frontend ganhou um campo de entrada.

#### Correções

- Corrigido o erro `23502` direto ao salvar a configuração de LLM com o campo de cabeçalho de sessão vazio: essa coluna é `NOT NULL DEFAULT ''`, e antes aplicava-se por engano `NULLIF($n,'')`, gravando NULL no cabeçalho de sessão não preenchido e violando a restrição de não nulo; agora o valor é passado direto, com semântica de string vazia.

### Agent

#### Novidades

- O timeout de relógio de parede passou a **encerrar no local**: depende do norma v0.3.6; ao chegar `MaxDuration`, interrompe a ferramenta em execução e continua no ctx vivo pelo número de rodadas de encerramento (grava de volta o que já foi identificado + resumo, com estado final timeout), sem depender mais do ctx rígido externo `maxDur+90s` de worker/planner que matava um run travado como `aborted_tools`. O chat usa o mesmo harness e ganha automaticamente o mesmo comportamento, e o mainagent, sem `MaxDuration`, não é afetado.
- Proteção contra paralisação: quando o planner é acordado por heartbeat / sem mudanças e o grafo inteiro não tem nenhuma intenção open ou running, a abertura passa a ser um alerta de paralisação, informando claramente que nenhum worker está rodando e nenhuma direção está na fila, e que nesta rodada é obrigatório produzir uma ou mais intenções novas e não duplicadas (não é permitido produzir 0 intenções).

#### Alterações

- Prompt do worker reestruturado: o JSON bruto de intenção / instrução de início / ativos ancorados passou ao system prompt, remontado a cada rodada e nunca descartado pela compactação, e a retomada também não depende de a primeira mensagem do transcript ter sido retida; a mensagem user inicial foi enxugada para apenas o overview da situação global (degradável, tolera stale). O custo é misturar dados por intenção no system e perder o reaproveitamento de cache entre intenções, uma troca deliberada em favor de "não perder a intenção".
- Corpo padrão de planner / worker simplificado e corrigidos vários problemas de campo: o planner distingue `recent_done` por status (blocked/exhausted: consulta o trace antes de decidir, sem tratar como beco sem saída nem reexecutar sem critério), conclusões negativas passam a "observação / dúvida, não definitivo" com checagem de evidence antes de adotá-las, objetivo não atingido e sem intenções open/running obriga a produzir (piso rígido), profundidade tem prioridade sobre cobertura; no worker, conclusões negativas escrevem apenas "observação + leitura tentativa", com o poder de veredito com o planner, e pistas entre intenções vão no summary do fact para o planejador, sem o worker persegui-las. O reseed acrescenta o novo padrão como nova versão e passa a ela, e as versões personalizadas / antigas do usuário permanecem no histórico, com possibilidade de rollback.
- Conjunto padrão de ferramentas do work agent e prompt de write-back de ativos reduzidos: o worker cuida apenas da execução e do write-back de uma única intenção, e ler contexto / revisar entre works é responsabilidade do planejamento, então foram removidos de suas ferramentas padrão `list_facts` / `node_detail` / `list_companies` e a busca entre works (`search_all_worker_traces` / `list_worker_traces` / `get_worker_trace`), mantendo só `list_findings` (checar duplicidade antes de reportar vulnerabilidade) + `add_finding` / `record_fact` + `insert_assets` / `list_assets`; do prompt foram apagadas as descrições de campos obsoletos em conflito com o schema de `insert_assets` (`type=tech`/`on_url`/`props`). Bancos antigos recebem uma migração única que remove os vínculos correspondentes, e os vínculos de mesmo nome de planner/main permanecem.

### Grafo de exploração

#### Novidades

- **Compactação de nós frios do grafo de exploração (cold-digest)**: dobra intenções / fatos antigos e há muito inativos em nós digest exibidos no `graph_overview`, preservando permanentemente os nós originais, que podem ser restaurados por id (armazenamento sem perdas, só a apresentação é compactada, o dobramento é reversível). A classificação frio/quente é por alcançabilidade reversa + qualquer ramo vivo torna quente, com debounce de R=6 rodadas e agrupamento por componente conexo; a compactação em segundo plano (minor dobra blocos frios não cobertos, major recompacta a partir da fonte e mescla fragmentos) tem reconferência de atividade e exclusão mútua com cooldown, nunca entra no caminho quente nem sobrescreve nós que reviveram. O lado do overview oferece `cold_digests` e `cold_index` indexado por ativo, e `expand_digest` / `expand_index` cuidam da restauração. O overview de tarefas relacionadas / herdadas também reaproveita a própria visão dobrada, e `expand_digest` suporta restauração somente leitura entre tarefas. `expand_digest` / `expand_index` são dados só ao planner e ao main agent, não ao worker. Todos acompanham migração de bancos antigos.
- `graph_overview` emite por completo a `finding_list`: findings são o produto de maior valor da tarefa e, em geral, poucos por tarefa, então passam a vir inteiros no overview (ao contrário dos facts, dos quais só se dá a janela recente); planner/worker veem de relance todas as vulnerabilidades confirmadas a cada rodada, sem precisar chamar `list_findings`. Cada item foi simplificado para `{id, summary, evidence?, from_intent?, assets?}`, com os ativos afetados dados diretamente como conteúdo legível (url / domínio / ip:porta) e não como ids brutos.

#### Alterações

- `graph_overview` deixa de listar `hosts` de forma plana: o bloco coverage removeu a lista de hosts (em tarefas de grande escopo, até 500 strings de host por rodada, de pouco valor para a decisão de planejamento), mantendo só `host_count`; hosts específicos são consultados sob demanda com `list_assets`. Novo `done_intents_total` de nível superior (total de intenções encerradas), em paralelo a `recent_done_intents`, truncado a ≤15, para que o planner saiba, ao deduplicar, se houve truncamento.

### Ferramentas

#### Alterações

- O vínculo padrão de `add_company_scope` mudou de worker para planner: definir o escopo de ativos da empresa é responsabilidade de planejamento / controle / Auto, e o worker só executa a exploração. O vínculo padrão do seed em bancos novos é mainagent/planner/auto, e bancos antigos recebem migração única.
- O planner passa a ter vinculado por padrão `list_assets`: agora tem ao mesmo tempo `list_assets` (busca em DSL no banco inteiro) e `list_untested_assets` (não testados dentro do escopo), com preenchimento retroativo único em bancos antigos, sem sobrescrever desvinculações do usuário.

### Tarefas

#### Novidades

- A lista de tarefas ganha a coluna "Workers em execução": conta os nós de intenção com `state='running'` da tarefa, com critério totalmente igual ao de "Workers em execução" na visão geral da página de detalhes da tarefa, exibindo 0 quando não há Worker em execução.

### Rede

#### Novidades

- Nova configuração de **proxy de saída global**: todo o tráfego para os alvos pode sair pela rede por um proxy global único (http/https/socks5, com suporte a `user:pass`). Com a captura de tráfego ligada, serve de upstream do proxy de gravação MITM (o tráfego é gravado e depois sai pelo proxy; tanto o caminho de interceptação quanto o de passagem direta usam o upstream, sem vazar o IP de origem); com a captura desligada, é injetado direto no ambiente bash do agent e no WebFetch (`proxyEnv` ganhou suporte a `ALL_PROXY` com socks5). A configuração fica na tabela KV de settings (sem necessidade de migração), e a página de configuração do sistema do frontend ganhou o cartão "Proxy global", independente do proxy de busca na web e do proxy de LLM.

### UI

#### Correções

- Corrigida a semântica errada dos rótulos de status de intenção: `exhausted` "esgotada" → "orçamento esgotado" (na verdade foi interrompida no meio ao atingir o orçamento de passos / tempo, gravando de volta só resultados parciais, e não significa que a direção foi esgotada), `blocked` "interceptada" → "erro de execução" (na verdade as tentativas se esgotaram por falha de modelo / API / rede, e a intenção basicamente não foi explorada de verdade, e não houve interceptação por alvo / WAF), e acrescentado o `stopped` "parada" que faltava (work parado manualmente pelo usuário).

### Dependências

#### Alterações

- Atualizado o norma para a v0.3.4: a saída das ferramentas MCP ganha truncamento e despejo em disco (veja `651b961`), e a v0.3.6 posterior sustenta o encerramento no local por relógio de parede.

### Contribuidores

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.7] - 2026-08-31

### LLM

#### Novidades

- A configuração de modelo ganha "Limite de saída", com escolha de qual nome de campo de requisição o limite usa: o limite restringe quantos tokens uma única resposta pode gerar, é enviado a cada requisição, e 0 (padrão) = não envia o campo, ficando a cargo do padrão do servidor; é coisa diferente da "janela de contexto" — esta é a capacidade total do modelo, usada só localmente para calcular o limiar de compactação e não aparece na requisição. O interruptor de nome de campo só faz sentido para o formato `openai` (Chat Completions): vazio (padrão) envia `max_tokens`, o único que a grande maioria dos gateways compatíveis reconhece; os modelos de raciocínio oficiais da OpenAI (série o / GPT-5), ao contrário, só reconhecem `max_completion_tokens` e, ao receber `max_tokens`, dão direto `unsupported_parameter`, e esses endpoints precisam ser trocados manualmente para o novo campo. Os dois formatos têm o nome do campo fixado (Anthropic usa `max_tokens`, a Responses API usa `max_output_tokens`), então para formatos que não são openai o interruptor fica esmaecido e é limpo ao salvar. Também foi completado um caminho que nunca havia sido conectado: planner / worker / chat / Agent principal / decomposição de objetivos nunca definiam limite de saída, o openai nem enviava e a Anthropic recorria ao padrão de 8192 do SDK, de modo que apenas adicionar o item de configuração sem conectar esse caminho faria o número preenchido não valer; agora o valor, como o da "saída em streaming", é resolvido a cada rodada conforme a configuração, entrando em vigor na rodada seguinte após um failover. Bancos antigos após o upgrade têm comportamento totalmente inalterado (coluna nova padrão 0 e nome de campo vazio).
- Ao clicar em "Testar" no painel, o código de status de cada tentativa HTTP e o corpo bruto da resposta do gateway são gravados no log do servidor (corpo cortado em 4K), facilitando diagnosticar 401, mensagens de cota, frames vazios, retorno de página HTML etc., sem depender apenas do ok / err recolhido na UI. Esse log não depende do interruptor "Registro de LLM".

#### Alterações

- A cadeia de configuração de LLM da tarefa passa a poder ser alterada em qualquer estado, sem a restrição de "em execução / pausada / cadeia de configuração esgotada": depois de a tarefa terminar (done / failed / timeout), a conversa do Agent principal ainda usa essa cadeia, e antes, quando o modelo da cadeia tinha problemas, não dava para editar e, portanto, não dava para continuar interagindo. Foram removidas as duas travas de estado final, no HTTP do backend e na transação de DB, e o diálogo do frontend também libera edição e salvamento para tarefas em estado final. Ao salvar uma tarefa em estado final, as intenções bloqueadas por cota não são reabertas (elas seriam movidas para open, sem worker para executar e sem satisfazer a condição de "reexecutar intenção"); para continuar rodando, ainda se usa reexecutar intenção / novo objetivo, que trazem a tarefa de volta ao estado de execução.

### Agent

#### Alterações

- "Enviar mensagem a um Worker em execução para ajustar a direção" passa a reaproveitar o mecanismo existente de pausa / retomada + transcript resume, com comportamento igual ao da conversa do Agent principal: antes usava-se um protocolo próprio de persistência de intervenção, que envolvia a barreira de agendamento, o fluxo de retomada e mais de uma dezena de filtros de consulta de atividade. Agora a mensagem é injetada pela entrada da próxima rodada, e a intenção roda diretamente em uma goroutine dedicada, sem a restrição do pool de 3 slots de worker, executando assim que enviada; o frontend mantém a caixa de mensagem e restaura o botão "continuar direto", com o envio agora carregado por SSE. Sem alteração de schema do DB. Compromissos: a mensagem fica só na memória, sem recuperação de crash, e ao enviar a mensagem a tarefa pode excepcionalmente exceder em um worker a concorrência (cenário de baixa frequência, aceitável).

### Tarefas

#### Correções

- Corrigido o crash por esgotamento de memória (OOM) ao arquivar tarefas grandes: o arquivamento passa a gravar o snapshot em streaming, sem ler a tarefa inteira para a memória de uma vez; também foram preenchidas três lacunas de recuperação no caminho de arquivamento a frio — o tráfego das instalações modernas existe apenas no SQLite, e antes, um crash entre o commit do PostgreSQL e o do SQLite deixava o tráfego da tarefa arquivada preso no armazenamento quente, sem como recuperá-lo; agora, com ou sem diretório histórico, grava-se o log de staging, e um `journal.json` ausente também é tratado como descartável.
- Corrigida a lentidão do sistema quando há muitas tarefas: carregar conversas e telas tinha atraso evidente. As consultas da lista de tarefas, do contexto da tarefa e dos registros de exploração foram otimizadas em conjunto, e o painel, a página de conversa e a de detalhes da tarefa no frontend reduziram requisições repetidas.

### Ativos

#### Alterações

- Removido o limite de "no máximo 256 regras" do escopo de ativos da empresa: empresas que cadastram o escopo IP / domínio a um, esbarram com facilidade nesse teto, e depois só conseguiam dividir em várias empresas, sendo que o escopo não fica mais lento por ter mais regras. O teto foi removido de uma vez no frontend, no backend e no mock da demo; cada regra continua limitada a 1024 caracteres, e o limite de 2 MiB do corpo da requisição foi mantido como reserva (equivale a cerca de quarenta a cinquenta mil regras).

### Skills

#### Correções

- Corrigido o erro "Falha no upload: zip: unsupported compression" ao enviar o pacote compactado de um skill: a biblioteca padrão do Go só embute os descompactadores Store / Deflate, e pacotes bzip2 e Zstandard gravados por programas de compressão em níveis não padrão não podiam ser abertos de jeito nenhum. Agora esses dois descompactadores foram adicionados (implementação em Go puro, sem nova dependência externa); métodos que realmente não podem ser descompactados, como Deflate64 / LZMA / XZ / PPMd, e pacotes criptografados, passam a ser reportados com mensagem em chinês antes mesmo da descompactação, apontando diretamente qual arquivo usou qual método de compressão e como reempacotar, sem despejar no usuário o erro bruto em inglês da camada inferior.
- Corrigida a validação de nomes de arquivo dentro de skills que considerava inválidos nomes de arquivo em chinês: a validação de caminho era uma lista branca ASCII `[A-Za-z0-9-_./]`, e bastava um arquivo com nome em chinês no pacote (como `参考/说明.md`) para o upload do pacote inteiro falhar com "o pacote contém caminho inválido". Passou a ser uma lista negra Unicode: permite nomes de arquivo em vários idiomas e espaços, e continua recusando caracteres de controle, UTF-8 inválido, caracteres de largura zero e de controle bidirecional (disfarce de nome de arquivo com RLO), `\ % # ? * : " < > |` e `..` / caminho absoluto / segmento de caminho vazio, sem mudança na proteção contra travessia de diretório. O nome do skill também foi liberado — o ASCII continua limitado a letras minúsculas, números e hífens (especificação agentskills.io), e letras não ASCII como as chinesas podem ser usadas diretamente como nome de skill, mas espaços, pontos e separadores de caminho não são aceitos.
- Corrigidos os nomes de arquivo embaralhados ou o pacote inteiro recusado após descompactar pacotes em chinês criados por programas de compressão do Windows: esses zips não marcam o flag UTF-8 e gravam os nomes de arquivo em GBK; agora há decodificação de fallback em GBK antes da validação de caminho. Também o frontmatter com aspas, como `name: "中文技能"`, passa a obter corretamente o nome do skill.

### UI

#### Novidades

- A página de achados ganha a visão plana "Todos os achados", definida como padrão, alternando com a visão original "agrupado por tarefa" pelas abas do cabeçalho: na visão agrupada é preciso expandir tarefa por tarefa para ver as vulnerabilidades, o que dá um desvio quando só se quer passar o olho em uma lista entre tarefas. A visão plana é uma grande tabela entre tarefas (10/20/50/100 por página), com comportamento totalmente igual ao da visão agrupada — marcar para exportar, expandir na linha para ver evidências e relatório detalhado, editar na linha nome/categoria/severidade, alterar o status de tratamento, aprofundar, excluir. As duas visões compartilham os cartões de estatísticas e a barra de filtros, e alternar não perde os filtros; a visão atual e os filtros também são guardados localmente; o polling consulta só a visão atual, e alterações na linha são gravadas nos dois caches, de modo que ao trocar não se veem dados desatualizados.
- A página de achados ganha a visão "por ativo": à esquerda uma árvore de ativos (empresa → domínio raiz / IP → subdomínio → serviço → interface, com a camada de empresa só aparecendo quando o ativo realmente tem atribuição), e à direita os achados de toda a subárvore do nó selecionado, compartilhando a mesma tabela e as mesmas condições de filtro das outras duas visões. A árvore inclui só ativos com achados, completando a cadeia de ancestrais sob demanda (quando o achado está apenas na interface mais profunda, as camadas acima também são montadas), e a contagem no nó é a agregação da subárvore, deduplicada por achado; achados cujo ativo foi excluído ou que nunca tiveram ativo associado vão para o balde "Sem ativo associado". Ao contrário das outras duas visões, a visão de ativos não faz polling — só consulta ao entrar na visão, alterar filtros, adicionar/excluir/alterar achados na página ou clicar no botão de atualizar da árvore, pois a árvore à esquerda é estrutura de navegação e não precisa ser recalculada a cada 5 segundos. Em cada camada da árvore mostra-se apenas o incremento em relação à camada anterior (subdomínio sem o sufixo do domínio raiz, serviço mostrado como `https :443`, interface só com o caminho), e o valor completo está na dica ao passar o mouse e no breadcrumb; quando há nós demais, descartam-se camadas inteiras de interface/serviço com um aviso, e a contagem ainda é somada às camadas superiores.

#### Correções

- Corrigido o problema de, na tela de detalhes da sessão no celular, o registro da sessão ficar espremido e não aparecer inteiro: em telas estreitas a lista de sessões é recolhida, cedendo a largura ao próprio registro.

### Dependências

#### Alterações

- Atualizado o norma v0.3.2 → v0.3.3: novo `Config.MaxTokensField`, para que o limite de saída do OpenAI Chat Completions possa enviar `max_completion_tokens` (os modelos de raciocínio só reconhecem essa chave). As duas chaves são mutuamente exclusivas, só uma é enviada, e o padrão continua `max_tokens`.
- Atualizado `golang.org/x/mod` v0.37.0 → v0.40.0, corrigindo dois alertas de segurança de dependência.

### Contribuidores

- [@Autumn-27](https://github.com/Autumn-27)
- [@neouks](https://github.com/neouks)
- [@begininvoke](https://github.com/begininvoke)

## [0.3.6] - 2026-08-27

### LLM

#### Correções

- Corrigido o problema de o interruptor "não streaming" voltar a "streaming" ao reabrir depois de salvo (#69): o DTO da interface de lista de configurações esquecia o campo `streaming`, a resposta nunca o devolvia, e o frontend, ao ler `undefined`, sempre recaía no streaming padrão; a gravação e o armazenamento no DB estavam corretos, apenas não podiam ser lidos de volta. O DTO recuperou o campo (sem `omitempty`, pois `false` também precisa aparecer na resposta).
- O teste de conexão passa a verificar que o modelo realmente respondeu, e a testar pelo modo real de envio e recebimento daquela configuração (#65): antes só se verificava se o HTTP tinha sucesso, e uma requisição que passava mas com o modelo sem nenhuma resposta (o raciocínio queimou o orçamento / o corpo foi engolido pela política de segurança / a camada de compatibilidade perdeu o `content`) também reportava "conexão bem-sucedida", em desacordo com o comportamento de "não responde" na sessão; além disso, o teste sempre usava streaming, então configurações não streaming testavam, na verdade, outro canal. Agora resposta vazia é falha direta, em caso de sucesso a resposta do modelo é exibida na mensagem, e o interruptor de streaming atual é levado ao teste, mantendo "teste aprovado" coerente com "a sessão funciona".

### Agent

#### Alterações

- `list_facts` passa a ter paginação + filtro por palavra-chave, evitando que uma única chamada estoure o contexto quando há muitos fatos (#74): por padrão retorna os 20 mais recentes, com suporte a `limit` (máximo 100) / cursor `before` / `q` (palavra-chave do resumo), retornando `{facts, total, has_more, next_before}`; o summary de cada item, se longo demais, é truncado por número de caracteres (o texto completo continua via `node_detail`). Os prompts de worker / planner foram atualizados para a semântica de paginação. Bancos antigos recebem, por migração única, o novo schema de parâmetros no catálogo de ferramentas (`SeedTool` só insere na primeira vez; caso contrário, a página de gerenciamento de ferramentas mostraria "sem parâmetros").

### Ferramentas

#### Novidades

- A página "Execução de ferramentas" ganha estatísticas de contagem de chamadas de ferramenta (#72): o botão "Estatísticas" na barra de ferramentas abre um diálogo mostrando, por ferramenta, o número de chamadas, a proporção e o número de falhas (ordem decrescente de contagem); usa os mesmos filtros de tarefa / palavra-chave da lista, conta todo o conjunto de resultados e não só a página atual, e só busca quando o diálogo é aberto.

### Dependências

#### Alterações

- Atualizado o norma v0.3.1 → v0.3.2: corrigidos reasoning/refusal descartados silenciosamente nas respostas da família OpenAI (a Responses ganhou `reasoning_text`), deduplicação dos três nomes do campo reasoning, retry limitado para resposta vazia (zero blocos de conteúdo), e o 400 do gateway causado por a fronteira de compactação cortar o pareamento de ferramentas (corrigindo também os órfãos já gravados nos transcripts).

### Contribuidores

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.5] - 2026-08-25

### LLM

#### Novidades

- Cada configuração de LLM suporta alternar entre streaming/não streaming (padrão streaming): ligado usa SSE em streaming; desligado usa não streaming de verdade (`stream:false`, devolvendo o JSON completo de uma vez), podendo contornar implementações de SSE ruins de alguns gateways (frames vazios, perda de frames do campo de raciocínio), ao custo de perder o progresso em tempo real durante a execução e a contagem de Token em tempo real. worker / planner / mainagent / chat / goals obtêm o valor dinamicamente conforme a configuração ativa atual; as três camadas de wrapper de Provider (`llmpool` / `llmrec` / runtime da tarefa) são compatíveis com não streaming; `llm_profiles` ganha a coluna `streaming` e bancos antigos recebem `ALTER` (padrão `true`, sem efeito perceptível nas configurações antigas).
- Suporte a configurações de LLM no formato OpenAI Responses API: cada configuração ganha um terceiro formato `openai-responses` (chama `POST /v1/responses`), em paralelo ao Chat Completions / Anthropic; normalização de `BaseURL`, modelo padrão `gpt-5`; a restrição de `format` de `llm_profiles` inclui `openai-responses`, com migração idempotente para bancos antigos; o dropdown de formato do frontend ganha "OpenAI (Responses API)". A dependência foi atualizada para o norma v0.3.1 (com a correção do retorno de `reasoning_content`).
- Gravação do texto bruto HTTP das requisições/respostas de LLM: captura o wire body real na camada de transporte HTTP, preservando o schema de ferramentas, os blocos `tool_use` e os frames SSE brutos que a visão normalizada não mostra; as várias tentativas de retry internas do norma são preservadas uma a uma; `llm_records` ganha as colunas `raw_request` / `raw_response`, com `ALTER` para bancos antigos; o painel de detalhes da página de gravação ganha alternância para a visão "Texto bruto" e botões de copiar requisição/resposta (com fallback `execCommand` compatível com contextos não seguros).

### Conversa

#### Alterações

- A seleção múltipla da lista de sessões passa a ser um interruptor de modo "Seleção múltipla": por padrão a lista não mantém caixas de seleção em cada linha (visual mais limpo), e o cabeçalho vira o botão "N no total + Seleção múltipla"; clicar em "Seleção múltipla" entra no modo de seleção (caixas, selecionar tudo, exclusão em lote), "Concluir" sai e limpa a seleção, e depois que a exclusão em lote tem sucesso total sai automaticamente (havendo falhas, permanece no modo de seleção para facilitar nova tentativa); renomear / fixar / excluir individualmente continuam pelo menu ⋯ de cada linha.

### UI

#### Alterações

- Experiência aprimorada no gerenciamento de tarefas, operações de sessão e visualização de tráfego (#57): a preferência de ordenação das colunas da lista de tarefas passa a ser memorizada de forma persistente, a renomeação na linha é acionada direto por um ícone (sem passar pelo menu), e foram refinadas a interação da gaveta (sheet) e os detalhes da visualização de tráfego.

#### Correções

- A etiqueta de ativo do Worker mostra apenas domínio e IP, e trata corretamente o caso de etiqueta vazia.

### Agent

#### Novidades

- O planner ganha, ao julgar o objetivo, a "conferência de aceitação quantificável": quando o objetivo contém condições de aceitação quantificáveis (cobertura de teste de ativos chegar a X%, obter N flags, obter certa permissão), antes de `prove_goal` é obrigatório conferir os valores medidos do `graph_overview` (`coverage.pct`, contagem de findings etc.); se o medido não atingiu a meta, `prove_goal` é proibido e passa-se a despachar intenções para cobrir a diferença, sem marcar met antecipadamente a pretexto de "a parte principal está concluída / em linhas gerais atingido". Corrigido o problema de objetivo que exigia 100% de cobertura, com apenas 40% medidos, ser dado como concluído.

#### Alterações

- Reescrito o critério de "0 intenções nesta rodada" no prompt do planner: o texto original descrevia 0 intenções como "o princípio mais comum e mais importante", o que fazia o planejador desistir cedo demais quando o objetivo não estava atingido e ainda havia superfície não testada no escopo. Agora só se deve produzir 0 intenções em dois casos concretos — ① as direções pensadas já estão todas cobertas por intenções open/running/recent_done; ② o próximo passo depende da saída de um work em execução e essa saída ainda não apareceu (deve-se esperar que ele termine e planejar no próximo despertar, depois de o grafo ser atualizado). Também foi acrescentada a restrição inversa: quando há de fato uma nova direção não coberta e que não depende de um work em execução, ou o objetivo não foi atingido e ainda há superfície não testada, não se deve desistir por "0 intenções é comum".
- Reforçado o "limiar de evidência para conclusões negativas" no prompt do worker: para conclusões negativas como "não injetável / porta fechada / sem entrada de login", que podem fazer o planejador abandonar uma direção inteira, exige-se esgotar os meios razoáveis dentro da intenção (trocar codificação/parâmetro/caminho/método) antes de concluir; se os meios não foram esgotados ou a evidência é fraca, marca-se sempre `confidence=inferred`, evitando que uma negação leviana com `observed` solde uma rota inteira (negações erradas no início da tarefa, em especial, desviam a direção e dificilmente se curam sozinhas).
- As mudanças dos prompts padrão de planner / worker acima são acrescentadas como nova versão e passam a ser a versão atual por uma migração única (`reseedPlannerPrompt` / `reseedWorkerPrompt`, cada uma com guarda por flag em settings); as versões antigas ficam no histórico de versões, e usuários que personalizaram prompts podem restaurá-las no registro de versões.

### Operações

#### Novidades

- Novo `reset-password.sh` para redefinir a senha do administrador (nome de usuário fixo `ARTEX`): suporta implantações local / docker, com informações de conexão especificáveis explicitamente ou lidas automaticamente de `--dsn`/`$ARTEX_PG_DSN`/`config.json`; dentro do banco, usa `pgcrypto` para gerar um hash bcrypt compatível com o login do backend e o grava em `settings.auth.password_hash`, sem necessidade de reiniciar o serviço após a redefinição. A senha é passada por variável de ambiente, sem entrar no argv do processo, e recebe escape contra injeção.

### Contribuidores

- [@Autumn-27](https://github.com/Autumn-27)
- [@neouks](https://github.com/neouks)

## [0.3.4] - 2026-08-24

### UI

#### Novidades

- Depois de marcar várias tarefas na lista de tarefas, é possível alterar a categoria em lote; a categoria alvo suporta "Sem categoria" para tirar a tarefa da categoria atual; a gravação do lote inteiro é feita em uma única transação, e os itens com falha só podem ser tarefas que foram excluídas depois de marcadas.

#### Alterações

- A "Categoria da tarefa" na criação de tarefa passa de seleção em dropdown a campo de entrada pesquisável: ao digitar, filtra as categorias existentes, e um nome que não existe no banco é criado e selecionado na hora com Enter (ou clicando em "Criar" no dropdown), com o item selecionado exibido como tag removível; continua limitado a uma única categoria.

### Conversa

#### Correções

- Corrigido o bloqueio do envio de mensagem com "LLM não configurado, não é possível conversar" quando a sessão já tinha uma configuração de LLM selecionada, mas não havia configuração ativa global. A verificação antes do envio olhava apenas a configuração ativa global, enquanto a execução real prioriza a configuração escolhida na sessão, e as duas lógicas de resolução eram inconsistentes; agora são unificadas em uma só (vínculo de sessão/Agent primeiro, global como reserva).
- O aviso de quando não é possível conversar foi detalhado por estado: sem nenhuma configuração, mostra "Adicionar configuração"; com configuração mas nenhuma ativa, mostra "Ativar ou indicar uma configuração para esta sessão", sem mais exibir sempre "não configurado", facilitando a localização.

### Agent

#### Novidades

- Em tarefas cujos objetivos já foram todos atingidos, as intenções despachadas pelo Agent principal via `add_intent` podem ser retiradas e executadas diretamente por um Worker, terminando ao concluir: nesse caso o Planner não roda mais (tarefa sem objetivo open não entra em planejamento), evitando que ele julgue de novo o objetivo como atingido e cancele a intenção recém-despachada; depois de o frontier ser esvaziado, a tarefa volta a concluída. Antes de despachar a intenção, o Agent principal pergunta ao usuário, com base no conteúdo da intenção, se deve registrá-la como objetivo formal; se registrar, a tarefa retoma o planejamento autônomo normal.

#### Alterações

- Quando `step_ids` de `get_worker_trace` / `get_task_worker_trace` excede o limite de uma vez (5), deixa de dar erro direto e passa a devolver o conteúdo completo dos 5 primeiros passos, informando no resultado, por `returned_step_ids` / `omitted_step_ids` / `notice`, quais foram obtidos desta vez e quais ainda não, com o aviso "se já for suficiente, não é preciso obter mais". Ids duplicados e inválidos enviados são deduplicados e removidos antes da contagem.

#### Correções

- A lista de tarefas passa a ser ordenada por id de tarefa decrescente (a mais recente criada primeiro), substituindo a ordenação anterior por horário de criação. Várias tarefas criadas no mesmo instante têm o mesmo timestamp e a ordenação era instável; junto com o polling a cada 10 segundos e a iteração aleatória do map em memória, isso fazia com que trocassem de posição na lista com frequência.

### Ativos

#### Correções

- Corrigido o recálculo de atribuição de empresa derrubado por uma única linha de dados sujos: quando `assets.ip` guardava um nome de host (escrito pelo Agent ou pela API de ativos), `a.ip::inet` lançava `22P02`, fazendo falhar e dar rollback em tudo: adicionar/alterar o escopo de ativos da empresa e excluir empresa. Passou-se a usar a conversão segura `try_inet()`, e valores inválidos são pulados em vez de interromper a instrução inteira.
- O `ip` impossível de interpretar deixa de ser pulado em silêncio: depois de salvar o escopo da empresa, é informado claramente quantos ativos têm `ip` que não é um IP válido (com id e valor específicos), lembrando que esses ativos não serão atribuídos pelas regras de IP/CIDR; a mesma informação também é gravada no log do servidor, cobrindo os caminhos sem resposta no frontend (excluir empresa, sincronização do scopesentry, escrita do Agent).
- A correspondência de IP/CIDR do escopo de teste da tarefa também passa a usar `try_inet()`, substituindo a guarda por regex que só verificava o conjunto de caracteres (nomes de host só com letras hex, como `abc.def`, antes ainda escapavam e disparavam o mesmo erro).

#### Alterações

- O campo `ip` dos ativos `ip`, `service` e `endpoint` deixa de aceitar nomes de host: `insert_assets` e a API de ativos devolvem, item a item, um erro com `index` e já indicam como corrigir (usar `type=subdomain` preenchendo `domain`, ou resolver primeiro os registros A/AAAA), e o Agent pode corrigir por conta própria e inserir de novo. Os demais ativos do mesmo lote não são afetados e entram no banco normalmente.

### Contribuidores

- [@neouks](https://github.com/neouks)

## [0.3.3] - 2026-08-23

### Worker

#### Novidades

- Workers em execução suportam pausa, retomada e cancelamento individuais; a pausa preserva intenções, fatos e vulnerabilidades, e o cancelamento limpa, de forma transacional, depois que a execução sai, a intenção atual e seus produtos diretos.
- Adicionados o estado de intenção `paused`, barreiras de execução e motivos de término nomeados, evitando escritas tardias no quadro negro depois de pausa, cancelamento ou exclusão da tarefa.
- Adicionado limite opcional de concorrência da tarefa; criação, retomada, aprofundamento de vulnerabilidade e preenchimento de vagas da fila passam todos por um caminho de admissão FIFO persistente.

#### Alterações

- O tempo padrão de relógio de parede de uma única execução do Worker passou de 600 para 1200 segundos; tarefas em timeout que são revividas voltam a contar o tempo na próxima execução real.
- O estado real de execução de Worker, Planner e Agent principal passa a conduzir de forma unificada o selo de estado da tarefa, corrigindo o problema de continuar aparecendo "ocioso" com o Worker em execução.
- A sessão do Worker mantém o controle individual e o total de Token da sessão atual, e o nome do modelo passa para a direita do título da sessão atual.

#### Remoções

- Removidas a seleção múltipla e seleção total de Workers e a interface de pausar/continuar em lote, junto com a API de controle em lote de Workers e o contrato de Mock.
- Removidos o selo de Token e o Tooltip das linhas da lista de Workers, mantendo-se o livro-razão completo de Token subjacente e a interface de agregação por tarefa.

### LLM

#### Novidades

- As tarefas suportam cadeia ordenada de configurações de LLM; ao identificar claramente cota insuficiente do fornecedor, troca automaticamente para a próxima configuração e persiste a configuração atual, o estado de esgotamento e o resumo do erro.
- Tarefas em execução e pausadas suportam editar a cadeia completa de configurações, ajustar a ordem e trocar manualmente a configuração atual, entrando em vigor a partir da próxima chamada de LLM.
- Troca automática, troca manual e esgotamento da cadeia inteira são gravados como atividade estruturada do sistema e geram, pelo fluxo de atividades da tarefa, um lembrete deduplicável.
- Adicionada a interface de resolução de modelo por papel da tarefa, que resolve de forma unificada a configuração e o modelo usados na próxima chamada do Main Agent, do Planner e do Worker.
- Adicionado o LLM Pool global opcional, com suporte a configurar a ordem de chamada, fallback de falha de modelo específico, estado de saúde, recuperação por resfriamento e redefinição manual.
- Adicionado o livro-razão de uso de LLM, sempre ativo, que agrega Tokens de entrada, saída e cache por tarefa, sessão, modelo e configuração.

#### Alterações

- A decomposição de objetivos, o Planner, o Worker e o Agent principal compartilham o runtime de LLM em nível de tarefa (prioridade de resolução abaixo).
- O failover só reage a erros explícitos de cota, saldo ou cobrança; erros comuns de limite de taxa, autenticação, rede, servidor e contexto não causam troca indevida.
- A página de configuração de LLM passa a lista de cartões de configuração com edição em gaveta à direita, e oferece ordem de rodízio do Pool, prioridade, itens excluídos, estado de saúde e operação de recuperação.
- A informação de cadeia esgotada suporta quebra de linha em tela estreita; o modelo atual passa do ícone da lista de sessões para um rótulo de texto ao lado do título da sessão atual e um Tooltip completo.
- A ordem de resolução de LLM de cada papel dentro da tarefa passou a "vínculo do Agent → cadeia de configuração da tarefa → global": papéis com modelo explicitamente vinculado sempre rodam nesse modelo, apenas papéis sem vínculo caem na cadeia da tarefa, e com a cadeia da tarefa vazia caem no global.
- Proxy de saída vazio significa conexão direta, sem voltar às variáveis de ambiente `HTTP_PROXY`/`HTTPS_PROXY` (o `ARTEX_LLM_PROXY` explícito não é afetado); a entrada de proxy suporta `socks5://user:pass@host:port` com usuário e senha.

#### Correções

- Falhas transitórias de streaming antes do commit (ainda sem nenhuma saída produzida) passam a ter retry com backoff seguro no mesmo provider, reduzindo de forma significativa os casos de "só rodou uma ou duas ferramentas e interrompeu, sem resumo, estado final `model_error`"; cota esgotada, contexto longo demais e erros determinísticos 4xx não são tentados de novo, continuando a cargo do failover e da recuperação por compactação.

#### Remoções

- Removido o identificador em forma de ícone de LLM na lista de sessões, evitando confusão com o ícone de estado do Worker.

### UI

#### Novidades

- As tarefas suportam criar, renomear, excluir e filtrar categorias globais; ao criar uma tarefa, é possível escolher a categoria diretamente.
- Adicionados o CRUD de modelos de tarefa globais e a gaveta de gerenciamento à direita; a nova tarefa pode carregar descrição e objetivos predefinidos, ou salvar o conteúdo atual como modelo.
- A nova tarefa suporta vincular várias tarefas de origem e vários escopos de ativos de empresa; o escopo da empresa reconhece automaticamente domínio, URL, IP, CIDR, ICP e palavras-chave da empresa por uma única caixa de texto de várias linhas.
- Os ativos de teste da tarefa suportam adição e exclusão em tempo real, registrando origens como manual, empresa, herdado ou descoberto pelo Agent; ao lado do título da sessão do Worker aparece o ativo de teste atual e o resumo da origem.
- A página de achados agrupa por tarefa e oferece paginação independente dentro do grupo, permitindo preencher a descrição da vulnerabilidade e criar uma intenção de aprofundamento de alta prioridade.
- A conversa suporta renomear, fixar, desafixar e excluir; a lista de tarefas suporta pausar e continuar em lote na página atual.
- Os detalhes de tráfego e os detalhes de execução de ferramentas passam a gaveta à direita; as mensagens HTTP ganham `Host` completo e destaque da linha de requisição, código de status, Header, JSON e corpo em linguagem de marcação.
- A coluna de ações da lista de tarefas ganha os botões "Detalhes" e "Pausar/Continuar"; a tecla de envio da sessão pode ser configurada nas configurações do sistema (`Enter` / `Cmd+Enter` etc.), e a área de busca Web ganha um campo de proxy.
- O selo à direita do título da sessão passa a mostrar o nome da configuração de LLM, com o ID do modelo movido para o Tooltip no hover; a tarefa pode ter nome opcional, e a lista de tarefas ganha coluna de nome e busca (se vazio, recai na descrição).
- A página de skills ganha estatísticas de número de usos, horário do último uso e dependências ausentes, facilitando localizar skills que não entraram em vigor ou não foram instalados.

#### Alterações

- O título da tarefa passa a ser um link de detalhes focalizável; ativos da tarefa, ativos da empresa, grupos de tarefas de achados e vulnerabilidades dentro do grupo usam paginação real do servidor, ordenação estável e total exato.
- A criação de empresa passa a usar a gaveta à direita, e a entrada de escopo é unificada em texto de várias linhas, com reconhecimento em tempo real, validação e prévia do tipo.
- A caixa de entrada do Agent principal suporta crescimento automático, envio com `Enter` e quebra de linha com `Shift+Enter`, evitando envio indevido no estado de composição de métodos de entrada em chinês.
- A prévia do Agent, o relatório da tarefa e os detalhes relacionados passam a usar renderização Markdown compartilhada; corrigidos de forma unificada a confirmação de exclusão, erros longos e a largura da gaveta no celular.
- A caixa de seleção de modelo de tarefa mostra e pesquisa pelo nome do modelo e envia pelo ID do modelo; restaurado o tamanho de fonte original do sistema, e a versão do aplicativo é exibida de forma unificada como `0.3.3`.
- O total da tarefa e o Token da sessão atual destacam apenas os valores de entrada, leitura de cache e saída; o botão de envio usa um ícone de seta para cima mais enxuto.
- O escopo de teste na visão geral da tarefa mostra o nome da empresa em vez do ID.

#### Correções

- Texto comum que contém ponto deixa de ser confundido com número de registro ICP.
- Corrigido o conflito de nomes entre o catálogo de variáveis e variáveis globais de runtime (como `{{.Now}}`), que fazia a lista de variáveis do editor de Agent renderizar chaves duplicadas.

#### Remoções

- Removido o botão independente "Entrar" do cartão da tarefa; clicar no título da tarefa para entrar nos detalhes passa a ser unificado.
- Removidos o botão "Nova tarefa" do painel, o campo de Logo URL ao adicionar empresa e o Badge de quantidade de vulnerabilidades no topo da página de achados e nos grupos de tarefas.
- Revertido o estilo de aumento global da fonte em 10%, e removidos da lista de sessões a caixa de seleção do Worker, o ícone de modelo e a estatística de Token por linha.

### Agent

#### Novidades

- A nova tarefa pode vincular diretamente várias tarefas existentes, herdando em tempo real e somente leitura os objetivos, fatos, vulnerabilidades, intenções concluídas, escopo de ativos e contexto do quadro negro das tarefas de origem diretas.
- As ferramentas de leitura do quadro negro suportam consultar sob demanda nós, fatos, vulnerabilidades e trilha de execução das tarefas de origem; nós herdados trazem marca de origem e todas as ferramentas de escrita recusam alterá-los.
- A ferramenta de escopo da empresa suporta domínio, URL, IP, CIDR, ICP e palavras-chave da empresa; as palavras-chave servem apenas como dica de escopo para o Agent e não participam da atribuição automática de ativos.
- A operação de aprofundamento de vulnerabilidade cria, na tarefa original, uma intenção humana de Worker de alta prioridade com âncora de ativos e aresta `derived_from`.
- A visão geral ganha o cartão "Gerenciamento de objetivos", permitindo ver, adicionar, alterar e excluir objetivos manualmente; adicionar ou alterar notifica o Planner e revive a tarefa, e excluir apaga fisicamente o nó do objetivo (em cascata arestas e âncoras) sem reviver.
- O Agent principal recebe por padrão a ferramenta `steer_work`, que injeta em tempo real instruções de correção em um Worker em execução, sem interromper nem perder progresso (continua verificando que a intenção pertence a esta tarefa).

#### Alterações

- As intenções da tarefa de origem não entram no frontier da nova tarefa, e a nova tarefa continua usando exploração, fila de execução, diretório de trabalho e histórico de conversa independentes.
- Excluir uma intenção em execução deixa de destruir dados: passa a parar em `stopped`, exigir um motivo de exclusão, pendurar o motivo como fato na intenção e gravá-lo no payload, e o planejador percebe isso como gatilho `cancelled` (preservando o conteúdo da intenção e o motivo).
- A conversa do Agent principal passa a ser conduzida pelo fluxo de atividades do servidor, com entrada recuperável em caso de falha de envio; ao abrir a sessão, cola automaticamente no fim e, depois do carregamento sob demanda dos detalhes, mantém a última resposta visível.
- Pausar a tarefa encerra as chamadas atuais do Main Agent, Planner e Worker, mas não impede que o usuário inicie, durante a pausa, uma nova sessão de orquestração do Main Agent.
- Cancelamento, encerramento e interrupção de streaming preservam de forma unificada o motivo real de término, o conteúdo já gerado, o número de rodadas executadas, o tempo, o Token e as chamadas de ferramenta sem retorno.

#### Remoções

- Removido o eco otimista de mensagens do cliente do Agent principal, evitando mensagens duplicadas e vazamento entre sessões sob pausa, falha ou atividade concorrente.

### Restrições

#### Novidades

- Na fase de decomposição de objetivos, primeiro extraem-se as restrições de operação (allow/deny) antes de dividir os objetivos, e o Agent principal pode complementá-las em tempo de execução; as restrições são injetadas com a mais alta prioridade nos prompts de sistema do Planner e do Worker, e depois do desvio, a diversidade e a exploração de ampliação de superfície obedecem explicitamente às restrições.
- A visão geral ganha o cartão "Gerenciamento de restrições" e interfaces de criar/alterar/excluir; o escopo de injeção das restrições pode ser ligado separadamente para Planner / Worker (ambos ligados por padrão, lido a cada rodada, com efeito imediato). A nova tabela `task_constraints` usa `CREATE TABLE IF NOT EXISTS`, e bancos antigos a recebem automaticamente.

### Interceptação

#### Novidades

- Além das regras de regex/string, a interceptação de comandos ganha aprovação de fallback pelo modelo: quando nenhuma regra bate, o modelo faz um julgamento semântico `ALLOW`/`ASK`/`DENY`, com a ação de fallback em caso de falha e a ação de timeout de aprovação configuráveis; a página de interceptação se divide nas duas abas "Regras de interceptação / Configuração do modelo", e os resultados de julgamento do modelo trazem o prefixo `[modelo]` e o motivo.

#### Correções

- Reforçada a interpretação da saída do juiz, evitando que um veredito `DENY` correto seja tomado por liberação.

### Tráfego

#### Alterações

- A gravação de tráfego passa a gravar a exchange inteira no SQLite (corpos grandes transbordam para um bucket de blobs deduplicado por hash), com índice de texto completo trigram para corpos de texto, suportando busca por qualquer substring e em chinês; a exclusão degenera para uma única transação SQL, caindo de horas para milissegundos e sem mais paralisar a gravação nesse intervalo. Adicionados o download em streaming de corpos grandes e o parâmetro de texto completo de `traffic_search`; dados da antiga árvore de arquivos não exigem migração e continuam legíveis, pesquisáveis e excluíveis.

### Tarefas e ativos

#### Novidades

- A criação de tarefa ganha o interruptor "Recurso de cobertura de ativos" (ligado por padrão): desligado, não calcula nem exibe cobertura, o grafo de situação mostra só ativos, não acumula `task_scope` automaticamente, e as ferramentas relacionadas são retiradas do Planner / Agent principal; a associação com a empresa não é afetada por esse interruptor.
- `insert_assets` ganha, em cada ativo, a marca `related` (padrão `true`): só vale quando a cobertura está ligada, e `false` entra só no banco compartilhado de ativos, sem contar na cobertura desta tarefa (como sites vizinhos ou ativos irrelevantes encontrados de passagem).
- O escopo da empresa e os ativos de teste da tarefa passam a suportar de forma unificada o reconhecimento de texto de domínio, URL, IP, CIDR, ICP e palavras-chave; domínio/IP podem criar ou reaproveitar ativos globais, e CIDR/ICP/palavras-chave são preservados como contexto de escopo da tarefa.

### Build

- `build.sh --release` suporta construir de uma vez Linux amd64/arm64, macOS amd64/arm64 e Windows amd64, e gera o pacote de release zip com `skills/`, exemplo de configuração e README.
- Usa o linker do Go para remover informações de depuração e gerar o pacote zip de release; o UPX passa a ser opcional explícito, evitando que seu ELF auto-extraível dê falha de segmentação na inicialização em alguns ambientes Linux.
- O Release Workflow executa um smoke test de inicialização no binário Linux amd64 e fornece `SHA256SUMS` junto com o pacote de release.

### Contribuidores

- [@neouks](https://github.com/neouks)

## [0.3.2] - 2026-08-20

### Added

- A nova tarefa suporta vincular várias tarefas existentes, herdando em tempo real e somente leitura os fatos, vulnerabilidades, intenções concluídas, escopo de ativos e contexto do quadro negro das tarefas de origem diretas; a nova tarefa continua usando fila de execução, diretório de trabalho e histórico de sessão independentes.
- As tarefas suportam cadeia ordenada de configurações de LLM. Ao identificar claramente cota insuficiente do fornecedor, troca automaticamente para a próxima configuração e persiste a configuração atual, o estado de esgotamento e a atividade de auditoria estruturada.
- Tarefas em execução e pausadas suportam editar a cadeia de configurações de LLM, ajustar a ordem e trocar manualmente a configuração atual; troca automática, troca manual e esgotamento da cadeia inteira são avisados na página de detalhes da tarefa.
- Workers em execução suportam pausa, retomada e cancelamento. A pausa preserva os dados do quadro negro, e o cancelamento limpa, de forma transacional, depois que o Worker para de escrever, a intenção e os fatos, vulnerabilidades e registros de execução produzidos diretamente por ela.
- Excluir tarefa pode optar por limpar também ativos, tráfego, vulnerabilidades e arquivos de landing da tarefa, e foram adicionados barreira de exclusão, proteção de concorrência e estatísticas de exclusão auditáveis.
- Adicionado limite opcional de concorrência de tarefas; novas tarefas que atingem o limite entram na fila em FIFO e iniciam automaticamente quando um slot de execução é liberado.
- Adicionados paginação de ativos da tarefa, paginação de ativos da empresa, estatísticas de categoria de tarefa e contrato Mock de intenções paginadas.
- Adicionado o script de build de binário único `build.sh`, com suporte a exportação estática do frontend, embutimento de recursos, alvos multiplataforma e injeção da versão de build.

### Changed

- A cadeia explícita de configuração de LLM em nível de tarefa coexiste com o LLM Pool global existente; a cadeia explícita continua com a semântica estrita de failover por cota, e sem cadeia explícita mantém-se a regra de vínculo do Agent e configuração global.
- A caixa de entrada do Agent principal suporta entrada de várias linhas com crescimento automático, envio com `Enter` e quebra de linha com `Shift+Enter`, evitando envio indevido no estado de composição de métodos de entrada em chinês.
- Planner, Worker e Agent principal compartilham o runtime de LLM em nível de tarefa; a cadeia da tarefa usa a menor janela de contexto entre os modelos candidatos como limiar seguro de compactação.
- A página de detalhes da tarefa mostra os estados de execução, ocioso e pausado conforme o estado real das chamadas de LLM; fatos, vulnerabilidades, intenções, referências de ativos e nós do grafo das tarefas de origem são marcados de forma unificada com a origem e mantidos somente leitura.
- A prévia do Agent, o relatório da tarefa e as visões de detalhes relacionadas passam a usar de forma unificada o componente de renderização Markdown compartilhado.
- A configuração de modelos de LLM adota interação por cartões e gaveta, e a lista de modelos suporta rolagem independente dentro da gaveta.
- Pausar a tarefa deixa de bloquear a conversa do Agent principal: a sessão de orquestração do Agent principal é independente da pausa da tarefa, e durante a pausa ainda é possível continuar enviando novas mensagens (a pausa encerra apenas a rodada em andamento).
- O tempo padrão de relógio de parede de uma única execução do Worker passou de 600 para 1200 segundos.

### Fixed

- Removido o eco otimista do console do Agent principal, passando a renderizar apenas dados do servidor, corrigindo mensagens embaralhadas e conteúdo de outras sessões infiltrado em cenários como pausa ou falha de envio.
- Abrir a sessão do Agent principal rola por padrão até o fim e exibe por completo a última resposta: depois de o conteúdo completo da última resposta ser expandido por carregamento sob demanda, cola automaticamente no fim, sem ser empurrado para fora da tela.
- Corrigido o problema de a sessão do Agent principal continuar rodando depois de pausar a tarefa e de o Agent de orquestração não parar o Agent principal ao pausar a tarefa.
- Corrigido o problema de o selo de estado da tarefa e o estado dos botões de operação ficarem dessincronizados quando a tarefa termina ou Planner, Worker ou Agent principal estão realmente em execução.
- Corrigido o problema de poderem ocorrer escritas tardias no quadro negro ou arquivos residuais entre cancelamento de Worker, exclusão de tarefa e escritas concorrentes.
- Corrigidos o preview de Markdown que falhava, o estouro de texto longo na confirmação de exclusão, a largura no celular e botões de detalhes da tarefa desalinhados.
- Adicionados motivos de término nomeados a todos os caminhos de cancelamento do Agent; os detalhes de atividade podem mostrar quem cancelou, o estado final, o número de rodadas executadas, o tempo, o uso de Token e as chamadas de ferramenta sem retorno.
- Corrigida a condição de corrida em que, no encerramento do backend, o context pai podia sobrescrever antes o motivo nomeado `shutdown`, e o problema de resumos de atividade em chinês truncados por byte virarem caracteres embaralhados.
- Corrigido o problema de eventos de cancelamento com saída parcial em streaming perderem o motivo real de término, preservando ao mesmo tempo o conteúdo já gerado antes do cancelamento.

### Contribuidores

- [@Autumn-27](https://github.com/Autumn-27)
- [@neouks](https://github.com/neouks)

[Unreleased]: https://github.com/Autumn-27/ARTEX/compare/v0.3.10...HEAD
[0.3.10]: https://github.com/Autumn-27/ARTEX/compare/v0.3.9...v0.3.10
[0.3.9]: https://github.com/Autumn-27/ARTEX/compare/v0.3.8...v0.3.9
[0.3.8]: https://github.com/Autumn-27/ARTEX/compare/v0.3.7...v0.3.8
[0.3.7]: https://github.com/Autumn-27/ARTEX/compare/v0.3.6...v0.3.7
[0.3.6]: https://github.com/Autumn-27/ARTEX/compare/v0.3.5...v0.3.6
[0.3.5]: https://github.com/Autumn-27/ARTEX/compare/v0.3.4...v0.3.5
[0.3.4]: https://github.com/Autumn-27/ARTEX/compare/v0.3.3...v0.3.4
[0.3.3]: https://github.com/Autumn-27/ARTEX/compare/v0.3.2...v0.3.3
[0.3.2]: https://github.com/Autumn-27/ARTEX/compare/v0.3.1...v0.3.2
