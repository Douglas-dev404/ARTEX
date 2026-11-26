package db

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Finding asset tree — the "Por ativo" view of the global findings list.
//
// A hierarquia tem a mesma origem de BuildCoverageGraph (company -> root_domain/ip/app -> subdomain ->
// service -> endpoint), mas aquele é um grafo de forças dos "ativos no escopo de uma tarefa", e este é uma árvore dos
// "ativos com achados em todo o banco": só entram ativos que têm achados e sua cadeia de ancestrais, e cada nó traz contagens agregadas da subárvore.
// As regras de prioridade pai/filho dos dois precisam ser consistentes; ao mudar um, confira e mude o outro em task_scope.go.
// ---------------------------------------------------------------------------

// FindingUnassignedAsset é a key do nó "ativos não vinculados" e também a sentinela de filtro da API de listagem:
// corresponde a achados com asset_ids vazio ou cujos ativos referenciados já foram excluídos.
const FindingUnassignedAsset = "__none__"

// findingAssetTreeMaxNodes é o limite de nós devolvidos ao frontend. Ao excedê-lo, camadas inteiras são descartadas de baixo para cima
// (primeiro endpoint, depois service): suas contagens já foram acumuladas nos nós pais, então descartam-se nós, não números.
const findingAssetTreeMaxNodes = 3000

// FindingAssetNode é um nó da árvore de ativos. A Key é isomórfica à do grafo de cobertura: linhas de ativo são "a:<id>",
// empresas são "c:<id>", domínios raiz sem linha de ativo são o sintético "r:<domain>", e o balde de não vinculados é "__none__".
type FindingAssetNode struct {
	Key       string `json:"key"`
	Parent    string `json:"parent,omitempty"`
	Kind      string `json:"kind"` // company|root_domain|subdomain|ip|service|app|endpoint|none
	Label     string `json:"label"`
	AssetID   int64  `json:"asset_id,omitempty"`
	CompanyID int64  `json:"company_id,omitempty"`
	// Self é o número de achados ligados diretamente a este ativo; Total inclui todos os descendentes e é deduplicado por finding
	// (quando um achado se liga a vários ativos, é contado só uma vez no ancestral comum).
	Self        int       `json:"self"`
	Total       int       `json:"total"`
	Critical    int       `json:"critical"`
	High        int       `json:"high"`
	Medium      int       `json:"medium"`
	Low         int       `json:"low"`
	LastFoundAt time.Time `json:"last_found_at"`
}

// FindingAssetTree é um snapshot único da árvore inteira. Nodes já está ordenado: sob o mesmo nó pai, por número de achados
// decrescente e rótulo crescente, e "ativos não vinculados" fica sempre por último.
type FindingAssetTree struct {
	Nodes        []FindingAssetNode `json:"nodes"`
	FindingTotal int                `json:"finding_total"`
	// Truncated=true indica que, para limitar o volume, as camadas listadas em DroppedKinds foram descartadas.
	Truncated    bool     `json:"truncated"`
	DroppedKinds []string `json:"dropped_kinds,omitempty"`
}

// assetRow é o subconjunto de campos de ativo necessário para montar a árvore.
type assetRow struct {
	id          int64
	kind        string
	companyID   int64
	domain      string
	rootDomain  string
	ip          string
	url         string
	port        int
	serviceType string
	appName     string
}

func (a *assetRow) coverageNode() CoverageGraphNode {
	return CoverageGraphNode{
		Kind: a.kind, Domain: a.domain, RootDomain: a.rootDomain, IP: a.ip,
		URL: a.url, Port: a.port, ServiceType: a.serviceType, AppName: a.appName,
	}
}

// label reutiliza a regra de rótulo do grafo de cobertura (URL > domain > ip > app_name > root_domain), mas serviços sem
// URL (SMB, portas não HTTP etc.) precisam da porta acrescentada: senão o rótulo ficaria
// idêntico ao da linha do IP/domínio host, e as linhas pai e filho na árvore pareceriam totalmente duplicadas.
func (a *assetRow) label() string {
	if a.kind == "service" && a.url == "" {
		if host, port := a.hostPort(); host != "" && port > 0 {
			return host + ":" + strconv.Itoa(port)
		}
	}
	n := a.coverageNode()
	n.Key = assetKey(a.id)
	return coverageNodeLabel(&n)
}

// hostPort é consistente com o grafo de cobertura: prioriza domain, depois o host dentro da URL e por fim ip.
func (a *assetRow) hostPort() (string, int) {
	n := a.coverageNode()
	return hostPortOf(&n)
}

const findingAssetSelectCols = `a.id, a.type, COALESCE(a.company_id,0),
       COALESCE(a.domain,''), COALESCE(a.root_domain,''), COALESCE(a.ip,''),
       COALESCE(a.url,''), COALESCE(a.port,0), COALESCE(a.service_type,''),
       COALESCE(a.app_name,'')`

func scanAssetRows(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
	Close() error
}) ([]*assetRow, error) {
	defer rows.Close()
	var out []*assetRow
	for rows.Next() {
		a := &assetRow{}
		if err := rows.Scan(&a.id, &a.kind, &a.companyID, &a.domain, &a.rootDomain,
			&a.ip, &a.url, &a.port, &a.serviceType, &a.appName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// findingAssetHit é a informação mínima de um achado necessária na fase de montagem da árvore.
type findingAssetHit struct {
	severity string
	ts       time.Time
	assetIDs []int64
}

// BuildFindingAssetTree monta a árvore de ativos conforme o filtro atual. O próprio AssetScope não participa (senão a árvore
// colapsaria numa cadeia única conforme o nó selecionado).
func (d *DB) BuildFindingAssetTree(f FindingFilter) (*FindingAssetTree, error) {
	return d.buildFindingAssetTree(f, findingAssetTreeMaxNodes)
}

// buildFindingAssetTree é a implementação interna com limite de nós. maxNodes<=0 significa sem truncamento — ao resolver
// AssetScope é preciso usar este modo, senão os endpoints descartados deixariam o conjunto de ids da subárvore incompleto.
func (d *DB) buildFindingAssetTree(f FindingFilter, maxNodes int) (*FindingAssetTree, error) {
	f.AssetScope = ""
	f.assetIDs, f.assetNone, f.assetMiss = nil, false, false
	where, args := f.where()

	rows, err := d.Query(`SELECT COALESCE(f.severity,''), f.created_at,
       COALESCE(f.asset_ids::text,'[]')
FROM findings f LEFT JOIN tasks t ON f.task_id = t.id`+where, args...)
	if err != nil {
		return nil, err
	}
	hits := []findingAssetHit{}
	assetIDs := map[int64]bool{}
	for rows.Next() {
		var h findingAssetHit
		var aidsJSON string
		if err := rows.Scan(&h.severity, &h.ts, &aidsJSON); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal([]byte(aidsJSON), &h.assetIDs)
		for _, id := range h.assetIDs {
			if id > 0 {
				assetIDs[id] = true
			}
		}
		hits = append(hits, h)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	tree := &FindingAssetTree{Nodes: []FindingAssetNode{}, FindingTotal: len(hits)}
	byID, err := d.loadFindingAssetRows(assetIDs)
	if err != nil {
		return nil, err
	}

	nodes, parentOf := d.assembleFindingAssetNodes(byID)
	if err := d.attachCompanyNodes(nodes, parentOf); err != nil {
		return nil, err
	}

	// Contagem: um achado sobe pela cadeia de ancestrais de cada um de seus ativos, coleta o conjunto de keys deduplicado e soma +1 em cada uma,
	// assim o nó pai não é contado em duplicidade por um achado ligado a vários ativos filhos.
	unassigned := &FindingAssetNode{Key: FindingUnassignedAsset, Kind: "none", Label: "Ativos não vinculados"}
	touched := map[string]bool{}
	for _, h := range hits {
		clear(touched)
		var direct []*FindingAssetNode
		for _, id := range h.assetIDs {
			node := nodes[assetKey(id)]
			if node == nil {
				continue
			}
			direct = append(direct, node)
			for key := node.Key; key != ""; key = parentOf[key] {
				touched[key] = true
			}
		}
		if len(direct) == 0 {
			countFinding(unassigned, h)
			unassigned.Self++
			continue
		}
		for _, node := range direct {
			node.Self++
		}
		for key := range touched {
			countFinding(nodes[key], h)
		}
	}

	for _, node := range nodes {
		if node.Total > 0 {
			tree.Nodes = append(tree.Nodes, *node)
		}
	}
	if unassigned.Total > 0 {
		tree.Nodes = append(tree.Nodes, *unassigned)
	}
	sortFindingAssetNodes(tree.Nodes)
	truncateFindingAssetTree(tree, maxNodes)
	return tree, nil
}

// countFinding acumula um achado no nó (total / balde de severidade / horário do achado mais recente).
func countFinding(n *FindingAssetNode, h findingAssetHit) {
	if n == nil {
		return
	}
	n.Total++
	switch h.severity {
	case "critical":
		n.Critical++
	case "high":
		n.High++
	case "medium":
		n.Medium++
	case "low":
		n.Low++
	}
	if h.ts.After(n.LastFoundAt) {
		n.LastFoundAt = h.ts
	}
}

// loadFindingAssetRows lê as linhas de ativos atingidos e, rodada a rodada, completa os ancestrais (domínio/IP host do service,
// domínio raiz do subdomínio). O ancestral pode não ter nenhum achado, mas a árvore precisa dele para tomar forma.
func (d *DB) loadFindingAssetRows(ids map[int64]bool) (map[int64]*assetRow, error) {
	byID := map[int64]*assetRow{}
	if len(ids) == 0 {
		return byID, nil
	}
	idList := make([]int64, 0, len(ids))
	for id := range ids {
		idList = append(idList, id)
	}
	rows, err := d.Query(`SELECT `+findingAssetSelectCols+` FROM assets a WHERE a.id = ANY($1::bigint[])`, idList)
	if err != nil {
		return nil, err
	}
	found, err := scanAssetRows(rows)
	if err != nil {
		return nil, err
	}
	for _, a := range found {
		byID[a.id] = a
	}

	// A cada rodada, descobre os identificadores de host que ainda não têm nó pai e completa uma camada em lote; o número de camadas é fixo (endpoint->service->
	// subdomain/ip->root_domain), 4 rodadas bastam para convergir.
	for range 4 {
		want := missingParents(byID)
		if want.empty() {
			break
		}
		added, err := d.loadAssetsByHost(want, byID)
		if err != nil {
			return nil, err
		}
		if added == 0 {
			break
		}
	}
	return byID, nil
}

// missingHosts são os identificadores de host a buscar no banco em uma rodada de completude, separados pelo tipo de ativo de destino.
type missingHosts struct {
	services []string // host do endpoint (busca linhas de service)
	domains  []string // domínio host de service/endpoint (busca linhas de subdomain)
	ips      []string // IP host de service/endpoint (busca linhas de ip)
	roots    []string // domínio raiz do subdomínio (busca linhas de root_domain)
}

func (m missingHosts) empty() bool {
	return len(m.services) == 0 && len(m.domains) == 0 && len(m.ips) == 0 && len(m.roots) == 0
}

// missingParents reúne os hosts ainda não carregados: service (para o endpoint se pendurar), subdomínio/IP (para
// service e endpoint se pendurarem) e domínio raiz (para o subdomínio se pendurar).
func missingParents(byID map[int64]*assetRow) missingHosts {
	haveService := map[string]bool{}
	haveDomain := map[string]bool{}
	haveIP := map[string]bool{}
	haveRoot := map[string]bool{}
	for _, a := range byID {
		switch a.kind {
		case "service":
			if host, _ := a.hostPort(); host != "" {
				haveService[host] = true
			}
		case "subdomain":
			haveDomain[a.domain] = true
		case "ip":
			haveIP[a.ip] = true
		case "root_domain":
			haveRoot[a.domain] = true
		}
	}
	wantService := map[string]bool{}
	wantDomain := map[string]bool{}
	wantIP := map[string]bool{}
	wantRoot := map[string]bool{}
	for _, a := range byID {
		switch a.kind {
		case "service", "endpoint":
			host, _ := a.hostPort()
			// O endpoint primeiro procura o service do mesmo host; um service com porta divergente é filtrado no final com Total=0
			// e não contamina a árvore.
			if a.kind == "endpoint" && host != "" && !haveService[host] {
				wantService[host] = true
			}
			if host != "" && !haveDomain[host] && !haveIP[host] && !haveRoot[host] {
				if isIPLiteral(host) {
					wantIP[host] = true
				} else {
					wantDomain[host] = true
				}
			}
			if a.ip != "" && !haveIP[a.ip] {
				wantIP[a.ip] = true
			}
		case "subdomain":
			if a.rootDomain != "" && !haveRoot[a.rootDomain] {
				wantRoot[a.rootDomain] = true
			}
		}
	}
	return missingHosts{
		services: keysOf(wantService),
		domains:  keysOf(wantDomain),
		ips:      keysOf(wantIP),
		roots:    keysOf(wantRoot),
	}
}

func keysOf(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// isIPLiteral faz um julgamento grosseiro se um host é literal de IP (para decidir se busca na tabela ip ou na subdomain).
func isIPLiteral(host string) bool {
	if strings.Contains(host, ":") {
		return true // IPv6
	}
	if host == "" {
		return false
	}
	for _, part := range strings.Split(host, ".") {
		if part == "" {
			return false
		}
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return strings.Count(host, ".") == 3
}

// loadAssetsByHost completa em lote as linhas de ativos por identificador de host e retorna o número de linhas novas nesta rodada.
func (d *DB) loadAssetsByHost(want missingHosts, byID map[int64]*assetRow) (int, error) {
	added := 0
	load := func(q string, arg []string) error {
		if len(arg) == 0 {
			return nil
		}
		rows, err := d.Query(q, arg)
		if err != nil {
			return err
		}
		found, err := scanAssetRows(rows)
		if err != nil {
			return err
		}
		for _, a := range found {
			if _, ok := byID[a.id]; ok {
				continue
			}
			byID[a.id] = a
			added++
		}
		return nil
	}
	if err := load(`SELECT `+findingAssetSelectCols+` FROM assets a
WHERE a.type='service' AND (a.domain = ANY($1::text[]) OR a.ip = ANY($1::text[]))`, want.services); err != nil {
		return 0, err
	}
	if err := load(`SELECT `+findingAssetSelectCols+` FROM assets a
WHERE a.type='subdomain' AND a.domain = ANY($1::text[])`, want.domains); err != nil {
		return 0, err
	}
	if err := load(`SELECT `+findingAssetSelectCols+` FROM assets a
WHERE a.type='ip' AND a.ip = ANY($1::text[])`, want.ips); err != nil {
		return 0, err
	}
	if err := load(`SELECT `+findingAssetSelectCols+` FROM assets a
WHERE a.type='root_domain' AND a.domain = ANY($1::text[])`, want.roots); err != nil {
		return 0, err
	}
	return added, nil
}

// assembleFindingAssetNodes transforma as linhas de ativos em nós e liga as relações pai/filho. Quando falta o nó pai (o banco
// não tem aquele ativo de domínio raiz), sintetiza o nó placeholder "r:<domain>", igual ao tratamento do grafo de cobertura.
func (d *DB) assembleFindingAssetNodes(byID map[int64]*assetRow) (map[string]*FindingAssetNode, map[string]string) {
	nodes := map[string]*FindingAssetNode{}
	parentOf := map[string]string{}
	rootByDomain := map[string]string{}
	subByDomain := map[string]string{}
	ipByAddr := map[string]string{}
	svcByHost := map[string]string{}
	svcByHostPort := map[string]string{}

	for _, a := range byID {
		key := assetKey(a.id)
		nodes[key] = &FindingAssetNode{
			Key: key, Kind: a.kind, Label: a.label(),
			AssetID: a.id, CompanyID: a.companyID,
		}
		switch a.kind {
		case "root_domain":
			if a.domain != "" {
				rootByDomain[a.domain] = key
			}
		case "subdomain":
			if a.domain != "" {
				subByDomain[a.domain] = key
			}
		case "ip":
			if a.ip != "" {
				ipByAddr[a.ip] = key
			}
		case "service":
			if host, port := a.hostPort(); host != "" {
				svcByHost[host] = key
				svcByHostPort[host+"|"+strconv.Itoa(port)] = key
			}
		}
	}

	// Quando o domínio raiz do subdomínio não tem linha de ativo no banco, sintetiza uma raiz placeholder, para que o subdomínio não se espalhe pelo topo.
	for _, a := range byID {
		if a.kind != "subdomain" || a.rootDomain == "" {
			continue
		}
		if _, ok := rootByDomain[a.rootDomain]; ok {
			continue
		}
		key := "r:" + a.rootDomain
		nodes[key] = &FindingAssetNode{Key: key, Kind: "root_domain", Label: a.rootDomain}
		rootByDomain[a.rootDomain] = key
	}

	firstOf := func(keys ...string) string {
		for _, k := range keys {
			if k != "" {
				if _, ok := nodes[k]; ok {
					return k
				}
			}
		}
		return ""
	}
	for _, a := range byID {
		key := assetKey(a.id)
		var parent string
		switch a.kind {
		case "subdomain":
			parent = firstOf(rootByDomain[a.rootDomain])
		case "service":
			host, _ := a.hostPort()
			parent = firstOf(subByDomain[a.domain], subByDomain[host],
				ipByAddr[a.ip], ipByAddr[host], rootByDomain[a.rootDomain], rootByDomain[host])
		case "endpoint":
			host, port := a.hostPort()
			parent = firstOf(svcByHostPort[host+"|"+strconv.Itoa(port)], svcByHost[host],
				subByDomain[host], subByDomain[a.domain], ipByAddr[host], ipByAddr[a.ip],
				rootByDomain[a.rootDomain], rootByDomain[host])
		}
		if parent != "" && parent != key {
			parentOf[key] = parent
			nodes[key].Parent = parent
		}
	}
	return nodes, parentOf
}

// attachCompanyNodes acrescenta o nó pai de empresa aos ativos de topo (domínio raiz / IP / app) — a camada de empresa só aparece se o ativo
// de fato pertence a uma empresa; ativos sem atribuição continuam sendo o próprio topo.
func (d *DB) attachCompanyNodes(nodes map[string]*FindingAssetNode, parentOf map[string]string) error {
	want := map[int64]bool{}
	for _, n := range nodes {
		if n.Parent != "" || n.CompanyID <= 0 {
			continue
		}
		switch n.Kind {
		case "root_domain", "ip", "app":
			want[n.CompanyID] = true
		}
	}
	if len(want) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	rows, err := d.Query(`SELECT id, COALESCE(name,'') FROM companies WHERE id = ANY($1::bigint[])`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	names := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		names[id] = name
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for id, name := range names {
		key := companyKey(id)
		if _, ok := nodes[key]; ok {
			continue
		}
		if name == "" {
			name = "Empresa #" + strconv.FormatInt(id, 10)
		}
		nodes[key] = &FindingAssetNode{Key: key, Kind: "company", Label: name, CompanyID: id}
	}
	for _, n := range nodes {
		if n.Parent != "" || n.CompanyID <= 0 || n.Kind == "company" {
			continue
		}
		switch n.Kind {
		case "root_domain", "ip", "app":
			key := companyKey(n.CompanyID)
			if _, ok := nodes[key]; !ok {
				continue
			}
			n.Parent = key
			parentOf[n.Key] = key
		}
	}
	return nil
}

// sortFindingAssetNodes ordena: mais achados primeiro, em empate por rótulo; "ativos não vinculados" sempre por último.
// O frontend pendura os filhos na ordem do array, então basta que a ordem relativa sob o mesmo nó pai esteja correta.
func sortFindingAssetNodes(nodes []FindingAssetNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if (a.Kind == "none") != (b.Kind == "none") {
			return b.Kind == "none"
		}
		if a.Total != b.Total {
			return a.Total > b.Total
		}
		return a.Label < b.Label
	})
}

// truncateFindingAssetTree descarta camadas inteiras quando há nós demais (primeiro endpoint, depois service). As contagens já foram
// acumuladas nos nós pais; o que se perde é só o nível de detalhe expansível.
func truncateFindingAssetTree(tree *FindingAssetTree, maxNodes int) {
	if maxNodes <= 0 || len(tree.Nodes) <= maxNodes {
		return
	}
	for _, kind := range []string{"endpoint", "service"} {
		kept := tree.Nodes[:0]
		for _, n := range tree.Nodes {
			if n.Kind == kind {
				continue
			}
			kept = append(kept, n)
		}
		tree.Nodes = kept
		tree.Truncated = true
		tree.DroppedKinds = append(tree.DroppedKinds, kind)
		if len(tree.Nodes) <= maxNodes {
			return
		}
	}
}

// applyAssetScope resolve AssetScope (key do nó) em um conjunto de ids de ativos utilizável em SQL. Selecionar
// um nó equivale a selecionar toda a sua subárvore, então é preciso montar a árvore antes e coletar os descendentes.
func (d *DB) applyAssetScope(f FindingFilter) (FindingFilter, error) {
	scope := strings.TrimSpace(f.AssetScope)
	f.assetIDs, f.assetNone, f.assetMiss = nil, false, false
	if scope == "" {
		return f, nil
	}
	if scope == FindingUnassignedAsset {
		f.assetNone = true
		return f, nil
	}
	// Sem truncamento: os endpoints descartados também precisam participar da coleta de ids, senão a lista perderia dados.
	tree, err := d.buildFindingAssetTree(f, 0)
	if err != nil {
		return f, err
	}
	children := map[string][]FindingAssetNode{}
	byKey := map[string]FindingAssetNode{}
	for _, n := range tree.Nodes {
		byKey[n.Key] = n
		children[n.Parent] = append(children[n.Parent], n)
	}
	if _, ok := byKey[scope]; !ok {
		// O nó selecionado não existe mais sob o filtro atual; o resultado deve ser vazio, e não degenerar em "sem filtro".
		f.assetMiss = true
		return f, nil
	}
	seen := map[string]bool{scope: true}
	queue := []string{scope}
	for len(queue) > 0 {
		key := queue[0]
		queue = queue[1:]
		if id := byKey[key].AssetID; id > 0 {
			f.assetIDs = append(f.assetIDs, id)
		}
		for _, child := range children[key] {
			if seen[child.Key] {
				continue
			}
			seen[child.Key] = true
			queue = append(queue, child.Key)
		}
	}
	if len(f.assetIDs) == 0 {
		f.assetMiss = true
	}
	return f, nil
}

// assetIDContainments transforma ids de ativos no conjunto de operandos direitos do teste de contenção jsonb, para uso com
// idx_findings_asset_ids (GIN jsonb_path_ops).
func assetIDContainments(ids []int64) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, "["+strconv.FormatInt(id, 10)+"]")
	}
	return out
}
