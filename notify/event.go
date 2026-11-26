package notify

// Snapshot é o contrato da coluna JSONB notification_events.snapshot. Quem escreve é a transação de gravação
// de vulnerabilidades da camada db; quem lê é o motor de entrega e o filtro da camada server. A definição fica neste pacote por ser
// a carga útil do "domínio de notificação": o db apenas serializa, sem entender o significado dos campos.
//
// Por que armazenar os campos da vulnerabilidade de forma redundante em vez de consultá-los na renderização: depois do fato a vulnerabilidade pode ser renomeada, ter a severidade alterada ou o status mudado,
// e o conteúdo do push deve refletir a conclusão **do momento em que ocorreu** — consultar de novo levaria ao enganoso
// "alterada depois para low". Além disso, assim o fan-out e a renderização não precisam fazer JOIN nas três tabelas findings/tasks/assets.
type Snapshot struct {
	// Tipo do evento: finding_created / finding_status_changed
	Kind      string  `json:"kind"`
	FindingID int64   `json:"finding_id"`
	TaskID    int64   `json:"task_id"`
	VulnClass string  `json:"vulnclass"`
	Name      string  `json:"name"`
	Severity  string  `json:"severity"`
	Summary   string  `json:"summary"`
	AssetIDs  []int64 `json:"asset_ids"`
	// Não vazio apenas quando kind=finding_status_changed.
	FromStatus string `json:"from_status,omitempty"`
	ToStatus   string `json:"to_status,omitempty"`
}

// Item é uma vulnerabilidade a ser enviada, para os canais renderizarem.
type Item struct {
	FindingID int64
	Name      string
	VulnClass string
	Severity  string
	Summary   string
	// Assets são os nomes de exibição dos ativos já resolvidos (como domínio/IP). Preenchidos pela camada server —
	// este pacote não toca no banco e não consegue obter os nomes.
	Assets []string
	// DetailURL é o link de retorno para o detalhe da vulnerabilidade; vazio significa que public_base_url não foi configurado, e é omitido na renderização.
	DetailURL string
	// Exclusivo de eventos de mudança de status; quando ambos são não vazios, renderiza como "Pendente → Corrigido".
	FromStatus string
	ToStatus   string
}

// IsStatusChange informa se o item é um evento de mudança de status.
func (i Item) IsStatusChange() bool { return i.FromStatus != "" || i.ToStatus != "" }

// Title retorna o título de exibição do item: prefere o name nomeado manualmente, recorre ao tipo da vulnerabilidade vulnclass,
// e usa um placeholder quando ambos estão vazios — nunca produz um título vazio.
func (i Item) Title() string {
	if i.Name != "" {
		return i.Name
	}
	if i.VulnClass != "" {
		return i.VulnClass
	}
	return "(vulnerabilidade sem nome)"
}

// Message é o conteúdo completo de um envio por canal.
type Message struct {
	// Tem tamanho 1 em push único; em push consolidado (digest) é um lote inteiro.
	// Slice vazio é inválido; o chamador deve garantir pelo menos um item.
	Items []Item
	// Com Batch=true renderiza como mensagem consolidada (troca o título, inclui janela de tempo e contagem).
	Batch bool
	// WindowMinutes é o período do consolidado (em minutos), usado só com Batch=true no texto "últimos N minutos".
	// Deliberadamente passado de forma explícita pela configuração em vez de calcular time.Since na renderização: assim a renderização continua determinística e fácil de testar.
	WindowMinutes int
	// HomeURL é o endereço do painel da plataforma (public_base_url global); se vazio, não inclui o acesso ao painel.
	HomeURL string
}
