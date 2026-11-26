---
name: api-recon
description: Use este skill ao coletar as interfaces de API de um site.
---

# API Recon (reconhecimento de interfaces do front-end)

Sob a premissa de **autorização prévia**, descobrir da forma mais completa possível: **APIs de back-end** (caminho, método, parâmetros, corpo de resposta), **rotas de front-end** e **pontos de disparo de funcionalidades da UI** (abas, modais, ações de tabela etc.).

---

## Limites e proibições (Agent: leitura obrigatória · violar = extrapolar)

Este skill faz **somente reconhecimento da superfície de API / parâmetros**; não é a fase de caça a vulnerabilidades nem de exploração.

### Limites da tarefa

| Escopo | Permitido | Proibido |
|---|---|---|
| **Alvo** | Enumerar path, método, parâmetros, rotas, pontos de disparo da UI | SQLi/XSS/escalada de privilégio/força bruta/fuzz de vulnerabilidades, ataques por alteração de pacotes, operações destrutivas |
| **Autenticação** | Hook + stub/mock para contornar o portão de login **do lado do cliente** | Pedir ou adivinhar usuário e senha; tentar envio real do formulário de login |
| **Runtime** | Sem credenciais, fazer hook das interfaces e usar respostas mock para que a SPA entre no shell pós-login | Fluxos que dependam de uma sessão real no back-end para continuar |

### Análise dinâmica sem credenciais (padrão da Phase 3)

1. Por meio de `preload.js` / `runtime_harvest.js`, **interceptar e fazer stub** das interfaces de bootstrap de login, permissões, menus etc.;
2. Para as interfaces de consulta de negócio, devolver um corpo mock com **estrutura correta, código de negócio de sucesso e dados possivelmente vazios**;
3. Fazer o front-end continuar renderizando as páginas pós-login mesmo sem back-end ou em ambiente 401, disparando assim mais XHR/fetch/WebSocket;
4. **Dados vazios, tabelas em branco e UI de placeholder são esperados**: não mude para login real nem teste de vulnerabilidades por causa disso.

**Em uma frase**: use mock para sustentar as rotas e a montagem dos componentes do front-end, **registrando apenas as requisições outbound**; o que o back-end devolve não importa, o importante é **quais outras interfaces o front-end ainda vai chamar**.

### Proibições rígidas de processo

| Proibido | Alternativa |
|---|---|
| Antes de concluir a Phase 1, fazer grep/curl/Read no entry principal `index-*.js` para extrair paths de API | Rodar `OUTDIR/harvest_static.py` |
| Escrever à mão scripts como `extract_apis.py` para substituir o harvest | Alterar `OUTDIR/harvest_static.py` e rodar de novo |
| Repetir o mesmo grep/comando após ≥2 falhas | Mudar de estratégia: ler tool_logs, alterar o harvest, consultar o reference |
| Pular os portões A/B e rodar direto a versão original de `scripts/` | Copiar para OUTDIR e adaptar ao alvo |
| Autenticação real com usuário/senha, OTP, OAuth etc. | stub/mock (ver acima) |
| Pular o stub sob o pretexto de "obter dados reais" e fazer testes de escalada de privilégio/injeção | Registrar só outbound, que está dentro do limite do recon |
| Operações irreversíveis como excluir, exportar dados sensíveis, escrita em lote | Vale também para cliques de coverage |
| Declarar que obteve todas as páginas e interfaces sem concluir runtime + enumeração dinâmica | Ver "Definição de pronto" ou registrar as limitações |
| Declarar que domina todos os parâmetros sem concluir a matriz de disparo de parâmetros + diff | Matriz da Phase 3b + diff da Phase 5 |
| Inferir obrigatório/opcional a partir de uma única amostra de runtime | Diff de múltiplas amostras ou dedução a partir de regras de validação/erros |

---

## Modelo de duas camadas + modos de execução

| Camada | Produto | Limite |
|---|---|---|
| **Estática** (bundle JS) | Paths completos de endpoints, rascunho de rotas, candidatos a campos nos pontos de montagem do pacote | Sem método HTTP; parâmetros exigem a Phase 1b; perde URLs montadas em runtime |
| **Runtime** (sessão ativa) | Método + body + resposta + URLs dinâmicas + WS/SSE; diff de múltiplas amostras completa os parâmetros | A página precisa realmente renderizar para enviar requisições; uma amostra única não basta para definir obrigatório/opcional |

| Modo de execução | Motor | Quando usar |
|---|---|---|
| **depth** | `runtime_harvest.js` (Puppeteer) | Inventário de APIs, METHOD/params/corpo de resposta, WS/SSE, execuções em lote reproduzíveis |
| **coverage** | browser + `preload.js` | Clicar em abas/modais/tabelas, cobertura mais profunda dos pontos de funcionalidade |
| **both** | primeiro depth, depois coverage | Mais completo, mais demorado |

**Metodologia de parâmetros** (sem script genérico): path via harvest/regex; parâmetros via **expansão de janela a partir de âncora + cadeia de binding da UI + diff de múltiplas amostras + dedução por erros** (receitas de grep na seção J do [reference.md](reference.md)).

---

## Definição de pronto

Só é possível declarar o recon concluído quando todos os itens forem atendidos:

- [ ] **Estática**: o harvest da Phase 1 gerou `api_static.txt`, `routes.txt`, `js/`
- [ ] **Runtime**: pelo menos depth ou coverage; coverage/both exigem **Hook efetivo + ciclo de enumeração dinâmica**
- [ ] **Entrada no shell**: ao acessar um path de negócio, não cair em `/login` (atenção às rotas hash)
- [ ] **Parâmetros**: coverage/both concluem a matriz de disparo de parâmetros + `param_samples.json`; a Phase 5 consolida `params_merged.json`
- [ ] **Profundidade** (se a página do módulo estiver em branco): a Phase 4 reconstrói a árvore de permissões e roda de novo, até aparecerem APIs **em nível de módulo** (não apenas locale/bootstrap)
- [ ] **Entrega**: os produtos da Phase 5 estão completos (ver tabela de produtos da Phase 5); `insert_assets` grava os ativos de serviço e de endpoint

---

## Scripts e portões

`scripts/` são apenas modelos de referência; é **proibido** rodar a versão original e tratá-la como resultado final.

**Regra**: ler primeiro → adaptar ao alvo → gravar em `OUTDIR` (por exemplo `recon/`) → registrar em `CHANGES.md`; se não combinar, reescrever conforme a metodologia, aproveitando apenas a estrutura.

| Portão | Quando | Script de referência → cópia em OUTDIR | Alterações comumente necessárias |
|---|---|---|---|
| **A (estático)** | Depois da Phase 0, antes da **primeira** execução de harvest/spider | `harvest_static.py` / `spider_mpa.py` | **Na maioria dos sites o regex padrão roda direto**; só altere o regex de endpoint, o `publicPath` de webpack/Vite, o exclude/cookie do MPA se o manifest/dialeto não combinar |
| **B (runtime)** | Depois da Phase 2, antes de rodar depth/coverage | `runtime_harvest.js` / `preload.js` + `config.json` | Chaves de Cookie/localStorage, valores de sucesso do neutralize, stubs, regex de login, prefixo de api, hash/history |

**Ordem obrigatória para SPA** (não pode ser trocada; a numeração das Phases tem prioridade sobre "explorar primeiro, script depois"):

| Passo | Obrigatório | Proibido |
|---|---|---|
| Após concluir a Phase 0 | O próximo Bash = `python3 OUTDIR/harvest_static.py <URL> OUTDIR` | curl/grep/Read no entry principal `index-*.js` (geralmente >500KB) |
| Portão A | Copiar o script → pequenos ajustes se necessário → **rodar imediatamente** | Extrair APIs à mão primeiro e só depois decidir se roda o harvest |
| Antes de concluir a Phase 1 | Validar a saída com `wc -l`; em caso de 404, alterar o harvest e tentar de novo | Escrever script extract à mão; fazer grep repetido em URLs não baixadas |
| A partir da Phase 1b | grep apenas em `OUTDIR/js/*.js` | Usar o bundle principal no lugar do harvest |

- ✅ Copiar `harvest_static.py` → (opcional) alterar o regex → **rodar imediatamente**
- ❌ curl no bundle principal → vários greps → escrever extract temporário → só no fim o harvest
- **MPA**: após a Phase 0, o próximo Bash = `python3 OUTDIR/spider_mpa.py ...`

---

## Ferramentas e restrições de saída

| Restrição | Descrição |
|---|---|
| Arquivos grandes | Arquivos `index-*.js` >100KB: **proibido** Read/grep para o contexto; processar em lote com scripts do OUTDIR |
| Saída do grep | Obrigatório `\| head -20` ou `-m 5`; na conversa manter só o resumo dos paths, sem colar trechos do bundle |
| Validação | Usar `wc -l`, `ls \| wc -l`; não fazer Read de diretório inteiro |
| Exploração inicial por regex | Opcional, ≤1 vez, somente em chunks pequenos ≤50KB ou HTML; o estático oficial é o do harvest |
| reference | Receitas/modelos/solução de problemas em [reference.md](reference.md); não repetir o texto inteiro inline |

---

## Roteiro de execução

```
Phase 0 classificação + OUTDIR
  → Portão A → Phase 1 harvest (★ rodar imediatamente ★)
  → Phase 1b engenharia reversa de parâmetros
  → Phase 2 três portões de autenticação → config.json
  → Portão B → Phase 3 runtime + matriz de parâmetros
  → Phase 4 árvore de permissões (se necessário) → refazer a Phase 3
  → Phase 5 consolidar relatório + insert_assets inserir em lote todos os ativos de serviço e de endpoint API descobertos; em hipótese alguma é permitido deixar de fora ativos descobertos
```

Marcar em ordem; **sem concluir o item anterior, não avançar para a próxima Phase**.

1. [ ] **Phase 0**: sondagem inicial de SPA/MPA; criar `OUTDIR` → [Phase 0](#phase-0--classificação)
2. [ ] **Portão A + Phase 1**: copiar o script → harvest **imediato** → validar com `wc -l` → [Phase 1](#phase-1--estático)
3. [ ] **Phase 1b**: expansão de janela a partir de âncora + camada de binding → `param_candidates.json` → [Phase 1b](#phase-1b--engenharia-reversa-de-parâmetros)
4. [ ] **Phase 2**: três portões de autenticação → `config.json` → [Phase 2](#phase-2--três-portões-de-autenticação)
5. [ ] **Portão B**: ajustar os scripts de runtime → [Phase 3](#phase-3--runtime)
6. [ ] **Phase 3**: depth / coverage / both; confirmar entrada no shell; matriz de disparo de parâmetros → `param_samples.json`
7. [ ] **Phase 4** (se necessário): árvore de permissões → patch dos stubs → refazer a Phase 3 → [Phase 4](#phase-4--reconstrução-da-árvore-de-permissões)
8. [ ] **Phase 5**: consolidar produtos + relatório + `insert_assets` → [Phase 5](#phase-5--consolidação-e-relatório)

---

## Phase 0 — Classificação

Baixar o HTML de entrada e **criar `OUTDIR`** (não altere o `scripts/` dentro do skill):

- **SPA**: casca vazia + `<div id=app>` + chunks → Phase 1–5
- **MPA**: SSR + `<form>`, sem bundle de endpoints → depois do Portão A:

```bash
python3 recon/spider_mpa.py <BASE_URL> <OUTDIR> [--cookie "session=..."] [--max 300] [--depth 5] [--exclude "logout|delete|destroy"]
```

Produz `forms.txt`, `links.txt`, `api_inline.txt`. Se numa SPA forms ≈ 0 → mudar para a Phase 1.

---

## Phase 1 — Estático

Cumprir [Scripts e portões](#scripts-e-portões) · [Ferramentas e restrições de saída](#ferramentas-e-restrições-de-saída).

```bash
python3 recon/harvest_static.py <BASE_URL> <OUTDIR>
```

harvest: analisa os scripts do HTML → manifest webpack/Vite → baixa todos os chunks lazy → produz `js/`, `api_static.txt`, `routes.txt`, `chunkmap.txt`.

```bash
wc -l OUTDIR/api_static.txt OUTDIR/routes.txt
ls OUTDIR/js | wc -l
```

- Número de chunks vs manifest: em caso de 404, alterar o harvest e tentar de novo; não fazer curl manual chunk por chunk
- `api_static.txt` com poucas entradas → afrouxar o regex de endpoint dentro do OUTDIR e rodar de novo (ver reference)

### Phase 1b — Engenharia reversa de parâmetros

Os paths vêm da Phase 1; os campos de parâmetros precisam de recon separado. Regras de grep em [Ferramentas e restrições de saída](#ferramentas-e-restrições-de-saída).

**Critério de conclusão**: para as interfaces importantes, saber responder: nome do campo, local de transmissão, tipo inferido, se é obrigatório, valor de amostra, confiança.

#### 1b.0 — Formato de transmissão

| Formato | Onde ficam os parâmetros | O que olhar primeiro no estático |
|---|---|---|
| REST JSON | body + query | `(params\|data\|body)\s*:\s*\{` ao lado da âncora de path |
| GraphQL | `variables` | templates gql, `$page: Int` |
| Form tradicional | urlencoded | `<form>`, `FormData` |
| Upload de arquivo | multipart | `FormData.append` |
| Parâmetro de caminho | `/user/:id` | tabela de rotas + `useParams` / `$route.params` |
| Criptografado/assinado | embutido em `sign`/`data` | Hook nos argumentos da função de criptografia (reference seção D) |

Produto: marcar cada interface com `transport: query|json|form|graphql|encrypted`.

#### 1b.1 — Expansão de janela a partir da âncora

Tendo o path conhecido como âncora, expandir a janela para achar o objeto de montagem do pacote:

```bash
grep -n '"/api/user/list"' OUTDIR/js/*.js | head -20
grep -rhoaE '.{0,120}("/api[^"]+").{0,200}' OUTDIR/js/*.js | head -20
grep -rhoaE '(params|data|body|payload)\s*:\s*\{' OUTDIR/js/*.js | head -20
```

| Camada de encapsulamento | Pista de parâmetros |
|---|---|
| Instância axios | `data` / `params` |
| request unificado | interceptor injeta campos globais |
| Cliente OpenAPI | assinatura dos métodos gerados |
| React Query / SWR | segundo argumento do hook |
| Composable Vue | argumentos do composable |

Resíduos de tipos: `yup`/`zod`/rules, `Form.Item name=`, Swagger embutido.

→ `param_candidates.json`: `{ path, fields[], source: "static-callsite", confidence }`

#### 1b.2 — Camada de binding

```
Form field → onFinish/handleSubmit → transform → API payload
```

| Fonte de binding | Técnica |
|---|---|
| submit de formulário | Seguir submit → transform → API |
| Busca em tabela | `getFieldsValue()` → `params` |
| Rota | `:id` / `?tab=` |
| Interceptor | `tenantId` global, paginação, sign |
| select de enum | `options` → valores de enum da API |

Rastrear no call stack do DevTools, a partir de `fetch`/`XHR.send`, a função que monta o pacote.

#### 1b.3 — Três perguntas da montagem do pacote (≠ três portões de autenticação da Phase 2)

| Pergunta | O que responder |
|---|---|
| **Montagem** | Onde o payload é construído, vestígios de transform |
| **Validação** | required, pattern, enum |
| **Transmissão** | path / query / body / multipart / headers |

O portão do interceptor (Phase 2) também lê os campos globais injetados (Authorization, `X-Tenant-Id`, sign).

#### 1b.4 — Conexão com a Phase 3

Os campos candidatos vêm do estático/camada de binding; **obrigatório/opcional/dependência condicional** exigem a matriz de parâmetros da Phase 3 + diff + dedução por erros da Phase 5.

---

## Phase 2 — Três portões de autenticação

Fazer grep em `OUTDIR/js/` (com `head`) e gravar em `config.json` (receitas no reference):

| Portão | Pergunta | Palavras-chave |
|---|---|---|
| **Portão de renderização** | Como saber se já está logado? | `isLogin`, `getToken`, Cookie/localStorage |
| **Portão do interceptor** | O que dispara o redirecionamento para `/login`? | `response_code`, `errno`, axios interceptor |
| **Portão de conteúdo** | De onde vêm menus/permissões? | `menu`, `permission`, `role`, `acl`, `routes` |

Proibido tratar o nome de chave do localStorage como credencial; é preciso confirmar a partir do chunk/cadeia de requisições.

**Saída = Portão B**: a conclusão vai para `config.json` e altera `OUTDIR/runtime_harvest.js` / `preload.js`.

### Phase 2b — Observação de API (opcional)

Usar o `preload.js` dentro do OUTDIR para confirmar nomes de chave de sessão, Authorization e URLs de API aninhadas:

| Configuração | Produto |
|---|---|
| `recordDetail: true` | `__API_RECON_DETAIL__` |
| `observe.xhrHeaders: true` | observação de headers |
| `extractUrlsFromResponse: true` | sub-APIs dentro da resposta |
| `observe.storageReads/cookieReads: true` | preencher o config de volta |
| `neutralizeVueRouter: true` | `__API_RECON_ROUTES__` |

Exportar a cada rodada do coverage: `__API_RECON_LOG__`, `__API_RECON_DETAIL__`, `__API_RECON_ROUTES__`, `__API_RECON_OBSERVE__`.

---

## Phase 3 — Runtime

É preciso já ter passado pelo Portão B; cumprir [Limites e proibições](#limites-e-proibições-agent-leitura-obrigatória--violar--extrapolar) · estratégia de mock sem credenciais.

Definir em `config.json` `"runtimeMode": "depth" | "coverage" | "both"` (modelo no reference).

### Hook e stub (compartilhados por depth + coverage)

| Camada | Escopo | Objetivo |
|---|---|---|
| L1 exata | stub de auth/permissões/bootstrap | Passar a autenticação da primeira tela |
| L2 correção negativa | todas as respostas JSON | Código de não logado → sucesso |
| L3 fallback | `/api` etc. que não bateram em L1 | Corpo de sucesso vazio, para sustentar a UI |

- **depth**: fake auth + `forward` alterando o código de negócio + `stubs`; percorrer `routes` (hash/history); produz `runtime_api.json`
- **coverage**: injetar `preload.js` em **document-start** (CDP `addScriptToEvaluateOnNewDocument` ou Userscript)

Verificação: `window.__API_RECON_PRELOAD__` existe; o path de negócio não volta para `/login`.

```bash
cd recon && npm install
node runtime_harvest.js config.json
```

### 3b — Enumeração dinâmica do coverage (obrigatória)

1. Navegação principal/barra lateral — clicar em cada item, aguardar a rede 1–3s
2. Abas — `role=tab`, `.ant-tabs-tab`
3. Tabelas — primeira linha: visualizar/editar/detalhes
4. Barra de ferramentas — exportar, filtrar, novo (**evitar exclusão irreversível**)
5. A cada módulo acessado — consolidar APIs/rotas
6. SPA — `pushState` controlado para paths não cobertos em `routes.txt` (proibido em MPA)

**Matriz de disparo de parâmetros** (obrigatória): em cada módulo, registrar uma vez por tipo de operação e fazer **diff de múltiplas amostras**:

| Operação | Parâmetros normalmente adicionais |
|---|---|
| Primeira tela da lista | paginação + filtros padrão |
| Clicar em buscar | keyword, filter |
| Filtro avançado | mais opcionais |
| Novo/editar | entity completa |
| Lote/exportar/ordenar | `ids[]`, `exportType`, `sortField` |

**Sob stub, o body/headers outbound continuam reais**: vale o que está na requisição. Gravação → `scan_raw.json`, `param_samples.json`, `api_detail.json`.

- **Vue**: `neutralizeVueRouter: true` + preload em document-start
- **React**: `routes.txt` + cliques na barra lateral + `pushState`
- **both**: primeiro 3a depth, depois 3b coverage

---

## Phase 4 — Reconstrução da árvore de permissões

**Gatilho**: página do módulo em branco / cada rota só com bootstrap (como locale) → o portão de conteúdo não foi passado.

| Sintoma | Significado |
|---|---|
| Entrada no shell bem-sucedida | Portão de renderização + portão do interceptor passados |
| Itens faltando na barra lateral / clique em branco | Formato do stub ou códigos de permissão incompletos |
| APIs idênticas e muito poucas em cada rota | `v-if permission` não passou |
| `routes.txt` muito menor que o bundle | É preciso completar a partir do módulo de auth |

```bash
grep -rhoaE '"/api[^"]*(permission|perm|role|menu|acl)[^"]*"' OUTDIR/js/*.js | sort -u | head -30
grep -rhoaE 'userRouteAuth|getResultTree|routeMap|routeLink|menuList|authList' OUTDIR/js/*.js | head -20
```

Cadeia típica: `role_permissions` (flat codes) + `permissions/all` (tree) → `getResultTree` → `userRouteAuth[CODE].url`.

```bash
python3 recon/extract_route_map.py recon/js recon/
python3 recon/build_perm_tree.py recon/js recon/ --config recon/config.json
```

Produtos intermediários: `route_map.json`, `userRouteAuth.json`, `permissions_tree.json`, `*_stub.json`, `perm_codes_all.txt`.

Checagem dos stubs: o `response_code` externo coerente com o portão do interceptor; flat codes alinhados com a tree; `routes` cobrindo todos os links de `route_map`.

Depois de atualizar o `config.json`, **rodar de novo a Phase 3**. Em SPAs grandes é possível ajustar `waitUntil`, `routeTimeout`, `perRouteMs` (ver reference seções A3/I).

---

## Phase 5 — Consolidação e relatório

### Tabela de produtos

| Arquivo | Etapa | Conteúdo |
|---|---|---|
| `js/`, `api_static.txt`, `routes.txt`, `chunkmap.txt` | 1 | Bundles estáticos e paths |
| `param_candidates.json` | 1b | Candidatos estáticos a campos de parâmetros |
| `config.json` | 2 | Três portões + configuração de runtime |
| `runtime_api.json` | 3a | Gravação detalhada do depth (incluindo WS/SSE) |
| `param_samples.json`, `scan_raw.json`, `api_detail.json` | 3b | Múltiplas amostras, log de cliques, detail |
| `route_map.json` etc. | 4 | Arquivos intermediários da árvore de permissões (se executada) |
| `params_merged.json` | 5 | Campos de parâmetros consolidados + confiança |
| `api_merged.txt` | 5 | `METHOD /path [params] [static\|runtime\|both]` |
| `site_map.json` | 5 | Rotas, APIs, params, pontos de funcionalidade, limitações |
| **insert_assets** | 5 | Gravar todos os ativos de serviço e de endpoint na base de ativos |

### 5b — Consolidação de parâmetros

Fazer diff a partir de `param_samples.json`; **não há script genérico de consolidação**. Regras de confiança em reference J7 (alta/média/baixa/aguardando disparo).

### 5c — Dedução por erros

Dentro do escopo autorizado, é possível enviar requisições incompletas e ler o 400 (**isto é recon de parâmetros, não teste de vulnerabilidade**): `field 'x' is required`, erros de enum etc. Atenção ao encapsulamento `data`, a `variables`, ao `bizData` antes da criptografia.

O relatório deve registrar: runtimeMode, número de APIs estáticas/de runtime, confiança dos parâmetros, módulos não cobertos, resumo do `CHANGES.md` em relação aos scripts de referência.

Estrutura sugerida de `site_map.json`:

```json
{
  "site": "https://example.com",
  "runtimeMode": "both",
  "appType": "vue-spa",
  "routeGuardStrategy": ["nav-neutralize", "L1-auth", "L2-patch", "forward"],
  "apisFromStatic": [],
  "apisFromRuntime": [],
  "apis": [],
  "params": [{ "method": "POST", "path": "/api/user/list", "transport": "json", "fields": [] }],
  "frontendRoutes": [],
  "routesVerifiedByClick": [],
  "featuresTriggered": [],
  "limitations": ""
}
```

Mais campos e receitas de grep em [reference.md](reference.md).

---

## Notas gerais

- **Independente de framework**: webpack/Vite/Angular têm o mesmo método de lazy load
- **Transporte**: REST/JSON, GraphQL, WebSocket, SSE; gRPC-web fora do escopo
- **SSR**: fetch do cliente pode ser gravado; RSC/Server Actions não são totalmente enumeráveis
- **Pontos cegos**: JSVMP, WASM, validação forte HMAC/mTLS → estático + registrar a limitação
- **Pontos cegos de parâmetros**: dependências condicionais, hidden params, montagem de pacote em WASM → "aguardando disparo"/"inalcançável"
- **O estático é a rede de segurança**: mesmo com o runtime bloqueado, o estático ainda consegue enumerar endpoints

---

## Recursos adicionais

- Receitas de grep, modelo de `config.json`, solução de problemas, Hook, seção J de engenharia reversa de parâmetros, modelo de site_map: **[reference.md](reference.md)**
- Caminhos dos scripts de referência na tabela de [Scripts e portões](#scripts-e-portões)
