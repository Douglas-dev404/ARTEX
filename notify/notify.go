// Package notify implementa a camada de adaptação dos canais de push de descoberta de vulnerabilidades por IM / e-mail.
//
// Camadas: este pacote é um **pacote folha**, que depende apenas da biblioteca padrão. Ele não conhece o banco de dados nem o server. A configuração do canal
// entra como map[string]any (correspondente à coluna JSONB notification_channels.config),
// e o conteúdo a enviar entra como Message. A vantagem dessa separação é que as partes realmente propensas a erro — cálculo de assinatura, truncamento UTF-8
// e correspondência de filtros — podem ser testadas em unidade sem o PostgreSQL, e o hospedeiro só precisa fazer a orquestração no lado do server.
//
// Convenção de concorrência: a implementação de Channel deve ser **sem estado**. A mesma instância de Channel é reutilizada de forma concorrente
// por várias configurações de canal (e até por várias instâncias de robô do mesmo canal), e todas as credenciais sempre vêm do parâmetro cfg;
// não é permitido guardar coisas como a URL do webhook em campos da própria implementação.
package notify

// Identificador do tipo de canal. Os valores são também o conjunto válido de notification_channels.kind, validado por lista de permissão
// no lado do server (assim como findings.status, sem DB CHECK, para facilitar a adição de canais depois).
const (
	KindDingTalk = "dingtalk" // robô personalizado do DingTalk
	KindFeishu   = "feishu"   // robô personalizado do Feishu (incluindo Lark)
	KindWeCom    = "wecom"    // robô de grupo do WeCom (WeChat Work)
	KindWebhook  = "webhook"  // Webhook genérico: método/cabeçalhos/template JSON personalizados
	KindTelegram = "telegram" // Telegram Bot API
	KindEmail    = "email"    // e-mail SMTP
)

// Tipos de evento, correspondentes a notification_events.kind.
const (
	EventFindingCreated       = "finding_created"
	EventFindingStatusChanged = "finding_status_changed"
)

// InitKind é o valor de reserva para um kind vazio na config.
const InitKind = KindDingTalk

// severityRank mapeia a severidade da vulnerabilidade para um ordinal comparável. Severidade desconhecida retorna 0, de modo que qualquer
// configuração de min_severity deixa a severidade desconhecida de fora — na dúvida, não envia, evitando inundação de falsos positivos.
var severityRank = map[string]int{
	"low":      1,
	"medium":   2,
	"high":     3,
	"critical": 4,
}

// SeverityRank retorna o ordinal da severidade; severidade desconhecida retorna 0.
func SeverityRank(severity string) int { return severityRank[severity] }

// SeverityLabel retorna o nome da severidade em português com emoji, usado no título da mensagem e na cor do cartão.
// Severidade desconhecida é ecoada como está, sem inventar.
func SeverityLabel(severity string) string {
	switch severity {
	case "critical":
		return "🔴 Crítica"
	case "high":
		return "🟠 Alta"
	case "medium":
		return "🟡 Média"
	case "low":
		return "🔵 Baixa"
	default:
		return severity
	}
}

// StatusLabel traduz o status de tratamento para português, para uso na mensagem de mudança de status.
func StatusLabel(status string) string {
	switch status {
	case "pending":
		return "Pendente"
	case "in_progress":
		return "Em andamento"
	case "confirmed":
		return "Confirmada"
	case "resolved":
		return "Tratada"
	case "fixed":
		return "Corrigida"
	case "false_positive":
		return "Falso positivo"
	case "ignored":
		return "Ignorada"
	case "duplicate":
		return "Duplicada"
	case "risk_accepted":
		return "Risco aceito"
	default:
		return status
	}
}

// AtLeast verifica se severity atinge o limiar min. min vazio significa sem limiar, e sempre passa.
// Atenção: severity desconhecida tem ordinal 0 e é recusada por qualquer min não vazio (veja o comentário de severityRank).
func AtLeast(severity, min string) bool {
	if min == "" {
		return true
	}
	return SeverityRank(severity) >= SeverityRank(min)
}
