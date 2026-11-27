# api-recon — manual de referência

Receitas de grep, modelo de `config.json` e resolução de problemas. Todos os greps rodam contra o diretório `js/`. Quando o bundle vem em uma linha só, passe antes um `js-beautify` ou `sed 's/}/}\n/g'`; na maioria dos casos o grep cru com janela de contexto já basta.

## Sobre os scripts

Todos os arquivos em `scripts/` são **modelos de referência** e precisam ser ajustados ao site alvo antes de rodar. O que costuma mudar:

| Script | O que normalmente se ajusta |
|---|---|
| `harvest_static.py` | regex de endpoint, leitura do manifest do webpack/Vite, publicPath de microfrontend, retentativa e concorrência |
| `runtime_harvest.js` | nomes de campo e valor de sucesso do neutralize, regras de casamento e corpo dos stubs, origem das rotas, gravação de WS, `waitUntil`/`routeTimeout`/`proxy` |
| `preload.js` | `loginPathRe`, stubs de L1, `neutralize.fields`, `apiPattern`, ligar ou não o L3, `recordDetail`, `observe.*`, `neutralizeVueRouter` |
| `spider_mpa.py` | `--exclude` para links destrutivos, cookie, depth/max, filtro de mesmo domínio |
| `extract_route_map.py` | regex de `routeMap` / `routeLink`, padrão de nomenclatura das KEYs |
| `build_perm_tree.py` | leitura de `userRouteAuth`, heurística de hierarquia `ROOTS`/`PREFIX_PARENT`, nome do campo externo do stub |
| `config.json` | ponto único de entrada de todos os parâmetros específicos do site acima |

Guarde os arquivos ajustados no diretório de trabalho da tarefa (por exemplo `recon/`) e registre no relatório o que mudou em relação aos scripts de referência.

---

## A. As três portas da engenharia reversa

### A1. Porta de renderização — "como o front decide que estou logado?"

```bash
grep -rhoaE '.{0,40}(isLogin|isAuthenticated|loggedIn|hasLogin|requireAuth)\b.{0,80}' js | head
grep -rhoaE 'function (getUser|getToken|getAuth)[0-9]?\([^)]*\)\{.{0,200}' js | head
grep -rhoaE '(localStorage|sessionStorage)\.getItem\("[^"]+"\)' js | sort -u
grep -rhoaE '(Cookies?|cookie)\.(get|load)\("[^"]+"\)' js | sort -u
grep -rhoaE '\batob\(|JSON\.parse\(|jwt|decode' js | head
```

Procure a cadeia `isLogin = f(getUser())` → `getUser = decode(storage.read(KEY))` e determine a **chave de armazenamento**, o **container** (cookie ou localStorage) e a **codificação**:

| Codificação | Como montar no config |
|---|---|
| String em texto puro / `"1"` / token | `"value": "anything-truthy"` |
| `JSON.parse(x)` | `"value": "json:{\"id\":1,\"username\":\"admin\"}"` |
| `JSON.parse(atob(x))` | `"value": "b64json:{\"id\":1,\"username\":\"admin\"}"` |
| JWT | JWT sem assinatura / `alg:none`, ou assinado com a chave que estiver no bundle |
| Criptografado (SM2/AES/RSA) | procure a chave fixa no código; quando a porta de renderização só precisa de um blob decodificável, dá para montar; caso contrário, fique na análise estática |

→ Grave em `cookies` / `localStorage`.

### A2. Porta do interceptador — "o que dispara o redirecionamento para /login?"

```bash
grep -rhoaE '.{0,60}(interceptors\.response|axios|request\.use).{0,120}' js | head
grep -rhoaE '.{0,40}(response_code|errcode|errno|\bcode\b|\bret\b|\bstatus\b)\s*[=!]==?\s*[\-0-9]{1,4}.{0,60}' js | head -20
grep -rhoaE '.{0,40}(未登录|请重新登录|登录已过期|unauthorized|登录失效|授权|token.{0,10}invalid).{0,40}' js | head
grep -rhoaE '.{0,30}(location\.href|router\.(push|replace)|navigate)\([^)]*login[^)]*\)' js | head
```

> O terceiro grep casa com a mensagem que o **alvo** devolve, então os termos acompanham o idioma do site auditado. Acrescente os do seu alvo, por exemplo `não autenticado|sessão expirada|sem permissão`.

Determine o **nome do campo**, o **valor de sucesso** (em geral `0` ou `200`) e o **valor de falha que dispara o redirecionamento**. Valide com uma sessão inválida:

```bash
curl -sk -X POST -H 'Cookie: <chave>=junk' https://alvo/api/<protegido> -d '{}' -H 'Content-Type: application/json'
```

→ Grave em `neutralize.fields` + `neutralize.success`.

### A3. Porta de conteúdo — "de onde vêm o menu e as permissões?"

```bash
grep -rhoaE '"/api[^"]*(permission|perm|role|menu|acl|resource|nav)[^"]*"' js | sort -u
grep -rhoaE '.{0,30}(menus|permissions|menuList|routeList|authList|role_permissions)\b.{0,120}' js | head
grep -rhoaE 'userRouteAuth|getResultTree|routeMap|routeLink|hasPermission|checkAuth' js | head
grep -rhoaE '([A-Z_][A-Z0-9_]*):\{name:"[^"]*",link:"/[^"]+"\}' js | head
```

**Duas camadas de dado** (comum em painel corporativo):

| API | Payload típico | Quem consome |
|---|---|---|
| `.../role_permissions` | `{ permissions: string[], role_type }` | guarda de rota, ACL de botão |
| `.../permissions/all` | `tree[{ code, position, children }]` | renderização do menu lateral |
| `userRouteAuth` no bundle | `{ CODE: { url, name? } }` | code → path do frontend |
| `routeMap` no bundle | `{ KEY: { name, link } }` | resolução de apelidos (webpack `o.DASHBOARD`) |

Leia o código de quem consome para confirmar: como `getResultTree(tree, permissions)` filtra e qual campo o `v-if` / `hasAuth(code)` verifica.

**Montagem manual** (sites pequenos): construa um payload permissivo → `stubs`.

**Reconstrução completa da árvore de permissões** (sites grandes, com o menu lateral ou os submódulos ainda em branco): ver a **seção I**.

---

## B. Modelo de config.json

```json
{
  "baseUrl": "https://alvo/",
  "runtimeMode": "both",
  "chromium": "/usr/bin/chromium",

  "cookies": [
    { "name": "auth", "value": "b64json:{\"id\":1,\"username\":\"admin\",\"role\":\"admin\",\"func\":{},\"permissions\":[\"*\"]}" }
  ],
  "localStorage": { "token": "faketoken", "isLogin": "1" },

  "neutralize": {
    "fields": ["response_code", "code", "errno", "ret", "status"],
    "success": 0,
    "flags": { "success": true, "message": "ok" }
  },
  "forward": true,
  "loginUrlPattern": "/login",
  "apiPattern": "/api/|/rest/|/graphql",

  "mockTier": "L1+L2",
  "recordDetail": true,
  "observe": {
    "storageReads": false,
    "cookieReads": false,
    "xhrHeaders": true
  },
  "neutralizeVueRouter": true,
  "stubs": [
    {
      "match": "permissions/all|/menu|role_permissions",
      "body": {
        "response_code": 0, "code": 0,
        "data": {
          "permissions": ["*"],
          "menus": [
            { "name": "dashboard", "path": "/dashboard", "show": true, "children": [] },
            { "name": "alert", "path": "/alert", "show": true, "children": [] }
          ]
        }
      }
    }
  ],

  "explore": {
    "clickTabs": true,
    "clickTables": true,
    "pushStateFallback": true,
    "maxMenuItems": 50
  },

  "routes": ["/dashboard", "/alert", "/asset", "/device", "/report", "/config", "/system"],
  "waitMs": 1500, "perRouteMs": 900, "headless": true,
  "waitUntil": "domcontentloaded",
  "routeTimeout": 12000,
  "proxy": "",

  "captureResponses": true, "recordWs": true, "respMax": 600
}
```

Sobre os campos:
- `runtimeMode`: `depth` (Puppeteer), `coverage` (browser MCP) ou `both`
- Prefixos de `cookies[].value`: `b64json:` → base64(JSON); `json:` → JSON cru; sem prefixo → literal
- `forward: true` encaminha a requisição real e reescreve o campo de código; `false` usa stub totalmente offline
- `mockTier`: camadas que o preload liga no modo coverage, como `L1+L2` ou `L1+L2+L3`
- `routes` vem de `routes.txt`; depois de montar o menu, o harness acrescenta os `<a href>` sozinho
- `captureResponses` e `recordWs` só valem no modo depth
- `waitUntil`: em SPA grande use `domcontentloaded`, para o `networkidle2` não travar
- `routeTimeout`: tempo limite do `page.goto` por rota, em milissegundos
- `proxy`: o `--proxy-server` do Puppeteer; também dá para usar `HTTP_PROXY` / `HTTPS_PROXY`

### B1. Modelo de stub duplo (role_permissions + permissions/all)

```json
"stubs": [
  {
    "match": "role_permissions",
    "body": {
      "response_code": 0,
      "data": {
        "permissions": ["MONITOR", "MONITOR_ALERT", "THREAT", "ASSETS_RISK"],
        "role_type": "SUPER_ADMIN"
      }
    }
  },
  {
    "match": "permissions/all",
    "body": {
      "response_code": 0,
      "data": [
        {
          "code": "MONITOR",
          "position": 1,
          "children": [
            { "code": "MONITOR_ALERT", "position": 1, "children": [] }
          ]
        }
      ]
    }
  }
]
```

Os nomes dos campos externos (`response_code` / `code` / `data`) têm de bater com o que foi descoberto na porta do interceptador (A2), e `permissions` precisa cobrir todos os codes folha da árvore.

---

## C. Modo coverage: configuração do preload

Edite o objeto `CONFIG` no topo de `scripts/preload.js`, ou substitua antes de injetar via CDP:

```javascript
const CONFIG = {
  loginPathRe: /\/(login|signin)(\/|$|\?)/i,
  mockTier: 'L1+L2',
  forward: true,
  recordDetail: true,
  extractUrlsFromResponse: true,
  neutralizeVueRouter: true,
  observe: { storageReads: false, cookieReads: false, xhrHeaders: true },
  neutralize: { fields: ['response_code', 'code'], success: 0 },
  stubs: [ /* os mesmos stubs do config.json */ ],
  apiPattern: /\/(api|apis|v\d+|dev|internal|graphql)\//i,
};
```

Para validar: `window.__API_RECON_PRELOAD__ === true` e o pathname estável.

Para exportar o que foi gravado:

```javascript
JSON.stringify({
  apis: [...window.__API_RECON_LOG__],
  detail: window.__API_RECON_DETAIL__,
  routes: [...(window.__API_RECON_ROUTES__ || [])],
  observe: window.__API_RECON_OBSERVE__,
}, null, 2)
```

---

## D. Capacidades dos hooks do preload e do runtime

Hooks de navegador embutidos no preload (coverage) e no runtime_harvest (depth), e o quanto cada um cobre:

| Hook | Para que serve na descoberta de API | Cobertura |
|---|---|---|
| fetch / XHR.open | grava URL e método da requisição | ✅ `recordDetail` + `__API_RECON_LOG__` |
| XHR.setRequestHeader | revela cabeçalhos como Authorization | ✅ `observe.xhrHeaders` |
| Leitura de localStorage/cookie | confirma o nome da chave de sessão | ⚠️ opcional, via `observe.storageReads/cookieReads` |
| Rotas do Vue | completa frontendRoutes | ✅ `__API_RECON_ROUTES__` (rotas já carregadas) |
| Neutralizar a guarda de rota e o redirecionamento de login do Vue | abre os módulos para que chamem as APIs | ✅ `neutralizeVueRouter` + neutralização da navegação nativa |
| Rotas do React | completa as rotas | ⚠️ estático + clique; não há hook dedicado |
| Bloqueio de navegação (path de login) | mantém a página para análise | ⚠️ só bloqueia o path de login, para não atrapalhar a navegação normal |
| Bibliotecas de criptografia (CryptoJS, SM etc.) | parâmetros cifrados → corpo de API em texto claro | ❌ exige hook manual nos argumentos da função de cifra; registre a conclusão no config |
| Contornar antidebug | sem isso, o runtime não grava as APIs | ❌ exige tratamento manual; a análise estática continua valendo |

---

## E. Regex de extração de endpoints (quando o estático traz pouco)

Afrouxe o `extract_endpoints` de `harvest_static.py`, ou faça à mão:

```bash
grep -rhoaE '"/[a-z][A-Za-z0-9_/\-]{3,}"' js | sort -u
grep -rhoaE '/api/[a-zA-Z0-9_./-]+' js | sort -u
```

---

## F. Resolução de problemas

| Sintoma | Causa → o que fazer |
|---|---|
| Poucas APIs no estático | o dialeto de endpoint não casa → afrouxe a regex (seção E) |
| Muito menos chunks que no manifest | chunks só de CSS ou não publicados; os 404 já foram repetidos |
| O runtime ainda mostra a tela de login | erro na porta de renderização → revise A1: chave, container, codificação, domínio |
| Entra na casca, mas os módulos ficam em branco | porta de conteúdo → monte o menu (A3); os paths em `routes` podem estar errados |
| Só bootstrap/locale em cada rota | faltam códigos de permissão → reconstrua a árvore (seção I); confira o stub duplo `role_permissions` + `permissions/all` |
| O menu tem itens, mas as subpáginas ficam vazias | falta um nó intermediário na árvore, ou o code não bate com o `userRouteAuth` |
| Toda API redireciona para o login | porta do interceptador → confira o `neutralize`; campos aninhados exigem estender a lógica de varredura |
| Zero frames de WS | o subscribe só ocorre após interação do usuário; aumente o `perRouteMs` |
| Corpo da resposta vazio | só há resposta real com `forward: true` |
| Chromium ausente | instale o chromium ou defina `config.chromium` / `CHROMIUM` |
| Muitos mocks e ainda volta ao login | hook tarde demais ou falta o setter de `location.href` → use document-start + preload |
| Listas todas vazias | array vazio no L3 é normal; siga clicando em abas, configurações e detalhes |
| Action do Redux confundida com rota | descarte os paths internos com get/set/change/clear/toggle/upload |
| O Vue ainda redireciona para o login | o preload não entrou em document-start → mude o momento da injeção; ou limpe a guarda à mão quando `neutralizeVueRouter: false` |
| A resposta traz URLs que não entram no log | ligue `extractUrlsFromResponse`; ou extraia à mão de `__API_RECON_DETAIL__` |
| Nome do cabeçalho de Authorization desconhecido | ligue `observe.xhrHeaders` ou veja os cabeçalhos no DevTools |
| Runtime lentíssimo ou estourando o tempo | use `waitUntil: domcontentloaded`, reduza o `routeTimeout` e evite `networkidle2` |
| Falha de conexão pelo proxy | confira `proxy` e as variáveis de ambiente; a porta do proxy do Puppeteer e a do curl precisam coincidir |

---

## G. Alvos endurecidos

Quando o servidor valida a sessão de verdade a cada passo (cookie assinado que não dá para montar, menu renderizado no servidor e não substituível por stub), o runtime trava na casca da aplicação. O esperado nesse caso:

- **O estático basta para enumerar endpoints**: os paths dos módulos estão no código
- Se a autorização permitir, rode o mesmo harness com uma **sessão real**: `forward: true`, sem precisar de neutralize, capturando métodos, parâmetros e respostas reais

---

## H. Checklist de uma execução

1. Confirmar o escopo autorizado
2. **Ler** `scripts/harvest_static.py` → ajustar ao alvo → rodar → revisar `api_static.txt` e `routes.txt`
3. **Fase 1b**: janela em torno dos paths + camada de binding → `param_candidates.json` (seção J)
4. Reverter A1/A2/A3 → escrever o `config.json` específico do site
5. **Ler e ajustar** `runtime_harvest.js` / `preload.js` antes de executar
6. `runtimeMode=depth`: `npm install` → rodar o script de coleta ajustado
7. `runtimeMode=coverage/both`: injetar o preload ajustado em document-start → enumeração dinâmica pelo browser MCP + **matriz de disparo de parâmetros**
8. Módulos que não renderizam → **reconstruir a árvore de permissões (seção I)** → corrigir os stubs → rodar de novo
9. Diferença entre várias amostras de parâmetros + dedução a partir dos erros → `params_merged.json`
10. Consolidar em `site_map.json` + `api_merged.txt`, registrando com honestidade a cobertura, as lacunas e o que foi alterado nos scripts

---

## I. Reconstrução da árvore de permissões (aprofundamento da fase 4)

Use quando montar um `menus: [{ path, show: true }]` simples não resolve e os submódulos continuam sem montar.

### I1. Localizar o módulo de autorização

```bash
grep -l 'userRouteAuth' js/*.js
grep -l 'routeMap\|routeLink' js/*.js
grep -rhoaE 'getResultTree|role_permissions|permissions/all' js | head
```

Anote: o **path da API de permissões**, os **nomes dos campos da resposta** e o **nome do chunk que consome**.

### I2. Extrair o routeMap

```bash
python3 scripts/extract_route_map.py recon/js recon/
# gera recon/route_map.json
```

Se aparecer `[!] no routeMap pattern found`: afrouxe a regex em `extract_route_map.py`, ou faça o grep à mão:

```bash
grep -rhoaE '([A-Z_][A-Z0-9_]*):\{name:"[^"]*",link:"/[^"]+"\}' js | head -20
```

### I3. Construir a árvore de permissões e o stub

```bash
python3 scripts/build_perm_tree.py recon/js recon/ --config recon/config.json
```

O que o script faz:
1. Interpreta `userRouteAuth={MONITOR:{url:...},...}` (incluindo apelidos do webpack, como `He=o.DASHBOARD`)
2. Usa `route_map.json` para resolver apelido → path real
3. Deduz o pai pelo prefixo do code (`MONITOR_ALERT` → `MONITOR`)
4. Gera `permissions_tree.json`, `permissions_all_stub.json` e `role_permissions_stub.json`
5. Com `--config`, grava sozinho os `stubs` e amplia as `routes` do `config.json`

**Ajustes por alvo** (no topo do script):
- `DEFAULT_ROOTS`: lista de codes dos módulos de topo
- `DEFAULT_PREFIX_PARENT`: mapeamento `PREFIXO_` → pai
- `DEFAULT_EXTRA_PARENT`: nós órfãos que não seguem a relação por prefixo

### I4. Conferir a consistência do stub

```bash
# a quantidade de permissions deve ficar perto do número de entradas de userRouteAuth
wc -l recon/perm_codes_all.txt
# routes deve cobrir todos os links do route_map
python3 -c "import json; m=json.load(open('recon/route_map.json')); r=set(json.load(open('recon/config.json'))['routes']); print('faltando', [v['link'] for v in m.values() if v['link'] not in r])"
```

### I5. Rodar o runtime de novo e comparar

```bash
node recon/runtime_harvest.js recon/config.json
# compare a contagem de runtime_api.json antes e depois; veja se /attack, /asset etc. passaram a trazer APIs de módulo
```

| Antes | Depois (quando dá certo) |
|---|---|
| as mesmas 3 a 5 chamadas de bootstrap em toda rota | cada rota dispara APIs de módulos diferentes |
| só `/api/locale/language` | aparecem endpoints `/api/web/...` dos módulos |
| `routes.txt` com meia dúzia de rotas | 80 a 110+ rotas vindas do route_map |

### I6. Quando ainda assim não funciona

- **Modo coverage**: clique no menu lateral e nas abas; o gating de permissão pode só fazer a requisição depois da interação
- **Campos do stub**: compare o aninhamento da API real (curl com sessão real) com o do stub
- **Guardas adicionais**: faça grep de `hasPermission|checkRole|func.` atrás de verificações em nível de botão e amplie `role_permissions.permissions`
- **Apoio do estático**: os paths de API dos módulos continuam em `api_static.txt`; o runtime só completa método e corpo. Os parâmetros ficam em `param_candidates.json` e nas amostras já gravadas

---

## J. Engenharia reversa de parâmetros (fases 1b / 5b / 5c)

**É metodologia, não um script pronto.** Para achar paths, use regex; para achar parâmetros, use janela em torno da âncora, a cadeia de binding da interface, a diferença entre várias amostras e a dedução a partir dos erros.

### J1. Janela em torno da âncora — do path até o objeto que monta a requisição

```bash
# usando um path já conhecido da fase 1 como âncora
grep -n '"/api/user/list"' js/*.js
grep -rhoaE '.{0,120}("/api[^"]+").{0,200}' js | head
grep -rhoaE '(params|data|body|payload)\s*:\s*\{' js | head
grep -rhoaE '(get|post|put|delete|patch)\([^,]+,\s*\{' js | head
```

### J2. Camada de empacotamento e formato de transporte

```bash
# axios / request unificado
grep -rhoaE '(axios|request)\.(get|post|put|delete|patch)\(' js | head
grep -rhoaE 'interceptors\.(request|response)' js | head

# GraphQL
grep -rhoaE '(query|mutation)\s+\w+|gql`|graphql\(' js | head
grep -rhoaE '\$[a-zA-Z_]+\s*:\s*(Int|String|Boolean|\[)' js | head

# FormData / multipart
grep -rhoaE 'FormData|\.append\(' js | head

# parâmetros de caminho
grep -rhoaE 'path:\s*"/[^"]*:[^"]+"' js | head
grep -rhoaE 'useParams|route\.params|\$route\.params' js | head
```

### J3. Porta de validação — obrigatórios, formato e enumerações

```bash
grep -rhoaE '(required|message|pattern|enum|validator)\s*:' js | head
grep -rhoaE 'yup\.|zod\.|async-validator|Form\.Item|a-form-item|el-form-item' js | head
grep -rhoaE 'rules\s*:\s*\[|name:\s*["\'][a-zA-Z_]+["\']' js | head
grep -rhoaE 'label.*value|options\s*:\s*\[' js | head
```

### J4. Camada de binding — formulário → API

```bash
grep -rhoaE 'onFinish|handleSubmit|getFieldsValue|validateFields' js | head
grep -rhoaE '(pick|omit|transform|dayjs|moment)\(' js | head
```

Complemento em runtime: DevTools → Network → requisição → **Initiator** (pilha de chamadas), subindo de `fetch`/`send` até a função que monta a requisição.

### J5. Parâmetros criptografados

```bash
grep -rhoaE 'encrypt|decrypt|sign|CryptoJS|sm2|sm3|sm4|RSA|AES' js | head
```

**Não tente adivinhar campos olhando o texto cifrado.** Coloque o hook nos **argumentos** da função de criptografia e grave o payload em texto claro antes de cifrar; registre a conclusão em `config.json` / `param_candidates.json`.

### J6. Matriz de disparo de parâmetros (obrigatória na fase 3)

Para cada módulo, grave uma vez por operação e compare o corpo e a query das requisições:

| Operação | O que observar |
|---|---|
| Primeira tela da lista | valores padrão de paginação |
| Busca | keyword, filters |
| Filtro avançado | campos opcionais |
| Criar/editar | entidade completa |
| Lote/exportação | `ids[]`, `exportType` |
| Ordenar/paginar | `sortField`, `order` |

Produz `param_samples.json`: `[{ "path", "method", "action": "search", "body", "query", "headers" }]`

### J7. Regras de confiança

| Confiança | Critério |
|---|---|
| **Alta** | callsite estático + 2 ou mais amostras de runtime coincidentes |
| **Média** | só estático, ou só uma passagem de runtime |
| **Baixa** | deduzido da resposta ou do erro, sem segunda verificação |
| **A disparar** | campo conhecido no estático, mas a interface ou a permissão não chegaram lá |

### J8. Configuração rápida por cenário

| Cenário | Ordem |
|---|---|
| Página de lista REST | J1 objeto da requisição → J6 quatro comparações → J3 regras |
| Formulário de criar/editar | J3 name do Form → J4 cadeia do submit → enviar em runtime e deixar campos vazios de propósito para ver o 400 |
| GraphQL | J2 declaração das variables → gravar as variables de cada operation em runtime |
| Corpo criptografado | J5 hook nos argumentos → os campos antes da cifra são os parâmetros reais |

### J9. Correspondência com as fases do api-recon

| api-recon | Recon de parâmetros |
|---|---|
| Fase 1, estático | J1 janela em torno da âncora |
| Fase 2, A2 interceptador | campos injetados globalmente (tenantId, sign) |
| Fase 3, runtime | J6 matriz de disparo + `param_samples.json` |
| Fase 4, árvore de permissões | formulários diferentes por módulo: só com permissão suficiente todos os campos aparecem |
| Fase 5, consolidação | `params_merged.json` + confiança; não defina obrigatoriedade a partir de uma amostra só |

### J10. Resolução de problemas

| Sintoma | O que fazer |
|---|---|
| Campo aparece no estático e nunca em runtime | marque como "a disparar"; complete a árvore de permissões, abra o filtro avançado, percorra as opções dos selects encadeados |
| Mesmo path com corpos de formatos diferentes | é normal: registre por `action`, sem forçar um schema único |
| A resposta do stub é falsa, mas preciso ver os parâmetros | **olhe a requisição de saída** (corpo e cabeçalhos), não deduza da resposta do stub |
| O 400 reclama de um campo aninhado | atenção ao invólucro externo: `data`, `bizData`, `variables` |
| No GraphQL só aparece o nome da operation | abra o JSON de `variables`; no estático, procure `$var: Type` |

---
