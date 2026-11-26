package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Este arquivo trata da reivindicação das tarefas de entrega e das transições de estado.
//
// A reivindicação usa um "lease" e não uma transação longa: a linha vai para sending e next_attempt_at é empurrado para o futuro como
// horário de expiração do lease; só depois do commit da transação é feita a entrega pela rede. Assim nenhum lock do banco é mantido durante a entrega —
// uma requisição de rede pode levar alguns segundos (timeout do cliente de 15 segundos), e segurar o lock da linha prejudicaria as outras escritas no mesmo banco.
//
// O custo é que, se o processo cair no meio da entrega, a linha fica em sending. Isso é **autorrecuperável**: após o lease expirar,
// next_attempt_at cai no passado e a próxima rodada de reivindicação pega a mesma linha de novo (ver a condição de reivindicação
// state IN ('pending','sending')). A contagem de tentativas já foi +1 na reivindicação, então uma queda não causa
// retentativas infinitas — esgotadas as MaxNotifyAttempts chances, vai para failed à espera de tratamento manual.

// MaxNotifyAttempts é o número máximo de tentativas de uma entrega (incluindo a primeira).
// Definido aqui e não no motor de entrega: é a política da própria máquina de estados, e o motor é apenas o executor.
const MaxNotifyAttempts = 3

// MaxDigestBatchSize é o número máximo de entregas que um único lote de resumo pode mesclar de uma vez.
//
// Existe por razão de recursos: se um ciclo de resumo varrer dezenas de milhares de vulnerabilidades (perfeitamente possível — uma única varredura completa
// faz isso), sem limite superior a reivindicação leria todas as linhas na memória e as renderizaria em uma mensagem gigante,
// que o limite de tamanho do canal truncaria em grande parte — desperdício de memória e **perda silenciosa** das vulnerabilidades cortadas.
// Com o limite, o excedente fica no banco e vira o próximo lote, saindo naturalmente no ciclo seguinte, sem perda.
//
// Por que 500: é a ordem de grandeza que, depois de renderizada em mensagem, ainda "tem conteúdo legível" dentro do limite de 4096 bytes do WeCom;
// maior que isso só empurra o corte para uma posição mais adiante.
const MaxDigestBatchSize = 500

// NotificationDelivery é uma tarefa de entrega, com a configuração do canal e o snapshot do evento necessários para a renderização.
type NotificationDelivery struct {
	ID            int64           `json:"id"`
	EventID       int64           `json:"event_id"`
	ChannelID     int64           `json:"channel_id"`
	State         string          `json:"state"`
	Attempts      int             `json:"attempts"`
	NextAttemptAt time.Time       `json:"next_attempt_at"`
	LastError     string          `json:"last_error"`
	BatchID       *int64          `json:"batch_id,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	SentAt        *time.Time      `json:"sent_at,omitempty"`
	Snapshot      json.RawMessage `json:"snapshot,omitempty"`
	// Contexto de renderização carregado por join, não entra no JSON (o DTO é montado pela camada server).
	Channel *NotificationChannel `json:"-"`
	// FindingID/EventKind vêm do evento, para a lista de histórico saltar direto aos detalhes da vulnerabilidade.
	FindingID int64  `json:"finding_id,string"`
	EventKind string `json:"event_kind"`
	// ChannelName/ChannelKind são campos redundantes para exibição na lista, economizando uma segunda consulta do frontend.
	ChannelName string `json:"channel_name"`
	ChannelKind string `json:"channel_kind"`
}

const notificationDeliveryCols = `d.id, d.event_id, d.channel_id, d.state, d.attempts, d.next_attempt_at,
       d.last_error, d.batch_id, d.created_at, d.sent_at`

// joinedDeliveryQuery é a forma unificada de leitura das linhas de entrega: entrega + snapshot do evento + configuração do canal.
// Renderizar uma mensagem exige os três; consultar separadamente seriam três idas e voltas.
const joinedDeliveryQuery = `SELECT ` + notificationDeliveryCols + `,
       e.snapshot, e.kind, e.finding_id,
       c.id, c.name, c.kind, c.enabled, c.config, c.mode, c.filter, c.rate_per_min
FROM notification_deliveries d
JOIN notification_events e ON e.id = d.event_id
JOIN notification_channels c ON c.id = d.channel_id`

func scanNotificationDelivery(sc interface{ Scan(...any) error }) (*NotificationDelivery, error) {
	var (
		dl        NotificationDelivery
		lastErr   sql.NullString
		batchID   sql.NullInt64
		sentAt    sql.NullTime
		snapshot  []byte
		eventKind string
		channel   NotificationChannel
		chEnabled bool
	)
	if err := sc.Scan(&dl.ID, &dl.EventID, &dl.ChannelID, &dl.State, &dl.Attempts, &dl.NextAttemptAt,
		&lastErr, &batchID, &dl.CreatedAt, &sentAt,
		&snapshot, &eventKind, &dl.FindingID,
		&channel.ID, &channel.Name, &channel.Kind, &chEnabled, &channel.Config, &channel.Mode, &channel.Filter, &channel.RatePerMin); err != nil {
		return nil, err
	}
	dl.LastError = lastErr.String
	if batchID.Valid {
		dl.BatchID = &batchID.Int64
	}
	if sentAt.Valid {
		dl.SentAt = &sentAt.Time
	}
	dl.Snapshot = json.RawMessage(snapshot)
	dl.EventKind = eventKind
	dl.ChannelName = channel.Name
	dl.ChannelKind = channel.Kind
	channel.Enabled = &chEnabled
	dl.Channel = &channel
	return &dl, nil
}

// claimQuery descreve uma reivindicação: primeiro seleciona candidatos por sel e os trava, depois os põe em sending e
// estende o lease. A posição de lease dentro de sel é marcada pelo chamador com $n, e o chamador passa o argumento.
type claimQuery struct {
	sql  string
	args []any
}

// ClaimRealtimeDeliveries reivindica um lote de entregas em tempo real vencidas de um canal, no máximo limit.
//
// Reivindica deliberadamente por **canal individual**, e não "reivindicar um lote global e escolher o que enviar": o portão de limite de taxa
// é mantido por canal dentro do motor de entrega; só sabendo antes quantas mensagens o canal ainda pode enviar nesta rodada e reivindicando
// exatamente essa quantidade de linhas é que o limite de taxa não consome tentativas. Se fosse ao contrário (reivindicar e depois descartar), as linhas barradas pelo limite
// já teriam contado um attempts, e o orçamento de 3 tentativas seria gasto só com espera, indo parar em failed.
//
// A condição inclui "sending com lease expirado" — é o ponto de autorrecuperação de quedas. O lease precisa ser bem maior que o pior caso
// de uma única entrega (timeout do cliente HTTP do canal de 15 segundos), senão a mesma linha seria entregue
// por dois dispatchers ao mesmo tempo. Também filtra canais desativados: a operação de desativar já marcou as entregas existentes como skipped,
// e aqui há mais uma barreira, para evitar escapes quando desativação e reivindicação ocorrem concorrentemente.
func (d *DB) ClaimRealtimeDeliveries(ctx context.Context, channelID int64, limit int, lease time.Duration) ([]*NotificationDelivery, error) {
	if limit <= 0 {
		return nil, nil
	}
	return d.claimDeliveries(ctx, lease, claimQuery{
		sql: `SELECT dd.id FROM notification_deliveries dd
JOIN notification_channels c ON c.id = dd.channel_id
WHERE dd.channel_id = $1 AND dd.state IN ($2,$3) AND dd.next_attempt_at <= now()
  AND c.enabled AND c.mode = $4
ORDER BY dd.next_attempt_at, dd.id
FOR UPDATE OF dd SKIP LOCKED
LIMIT $5`,
		args: []any{channelID, NotifyStatePending, NotifyStateSending, NotifyModeRealtime, limit},
	}, nil)
}

// DigestBatchDue informa se o canal já acumulou um lote vencido: há entregas pendentes e a **mais antiga**
// já atingiu a idade do ciclo de resumo.
//
// O critério é a idade da entrega mais antiga e não o relógio de parede: assim um canal recém-criado não emite, por alinhar na hora cheia,
// um "resumo" de um único item, e um lote acumulado há muito tempo também não espera mais uma rodada à toa.
//
// É separada de ClaimDigestBatch porque a semântica é diferente: esta função só responde "deve enviar ou não",
// enquanto a reivindicação leva **todas** as linhas pendentes do canal (inclusive as que ainda não atingiram a idade) — senão um ciclo
// seria dividido em várias mensagens e o resumo perderia o sentido.
func (d *DB) DigestBatchDue(ctx context.Context, channelID int64, minAge time.Duration) (bool, error) {
	var due bool
	err := d.QueryRowContext(ctx, `SELECT EXISTS (
  SELECT 1 FROM notification_deliveries d
  JOIN notification_channels c ON c.id = d.channel_id
  WHERE d.channel_id = $1 AND d.state IN ($2,$3) AND c.enabled
  GROUP BY d.channel_id
  HAVING min(d.created_at) <= now() - make_interval(secs => $4)
)`, channelID, NotifyStatePending, NotifyStateSending, int64(minAge.Seconds())).Scan(&due)
	return due, err
}

// ClaimDigestBatch reivindica as entregas pendentes vencidas de um canal, como um lote de resumo,
// com no máximo MaxDigestBatchSize itens por lote.
//
// Todas as entregas do mesmo lote compartilham batch_id, usando o menor id do conjunto como número do lote (estável, legível,
// sem precisar de sequência extra). Na retentativa, COALESCE preserva o número original do lote, para que "estas N foram enviadas juntas"
// continue valendo após várias retentativas.
//
// Pega os N primeiros por id crescente e não aleatórios: a entrega gerada mais cedo sai primeiro, e em caso de acúmulo não ocorre a inanição de
// "vulnerabilidade nova sai primeiro, antiga fica sempre no fim".
func (d *DB) ClaimDigestBatch(ctx context.Context, channelID int64, limit int, lease time.Duration) ([]*NotificationDelivery, error) {
	if limit <= 0 {
		return nil, nil
	}
	// limit é o **teto de memória**, o chamador passa MaxDigestBatchSize; aqui há mais uma trava,
	// para evitar que o chamador passe um valor maior.
	//
	// Deliberadamente não aceita a "cota de limite de taxa" como tamanho do lote: a unidade do limite de taxa é o número de mensagens — um lote envia só
	// uma mensagem e consome um token, descontado por takeTokens na camada server — e é uma dimensão diferente de "quantas
	// vulnerabilidades cabem num lote". Já se passou o orçamento de requisições de cada rodada como tamanho do lote para fazer rate_per_min valer para digest,
	// e o resultado foi que um canal com rate=20/min colocava só 1 vulnerabilidade por lote,
	// e o digest degenerava em push em tempo real com texto de resumo. Para mudar o limite de taxa, altere o want de takeTokens,
	// não mexa aqui.
	if limit > MaxDigestBatchSize {
		limit = MaxDigestBatchSize
	}
	out, err := d.claimDeliveries(ctx, lease, claimQuery{
		sql: `SELECT dd.id FROM notification_deliveries dd
JOIN notification_channels c ON c.id = dd.channel_id
WHERE dd.channel_id = $1 AND dd.state IN ($2,$3) AND dd.next_attempt_at <= now() AND c.enabled
ORDER BY dd.id
FOR UPDATE OF dd SKIP LOCKED
LIMIT $4`,
		args: []any{channelID, NotifyStatePending, NotifyStateSending, limit},
	}, func(tx *sql.Tx, ids []int64) error {
		batchID := ids[0]
		for _, id := range ids {
			if id < batchID {
				batchID = id
			}
		}
		ph, idArgs := placeholders(2, ids)
		_, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET batch_id = COALESCE(batch_id, $1)
WHERE id IN (`+ph+`)`, append([]any{batchID}, idArgs...)...)
		return err
	})
	return out, err
}

// claimDeliveries executa "selecionar + pôr em sending estendendo o lease + ler a linha completa", tudo em uma transação.
// postClaim é um passo adicional opcional (o lote de resumo o usa para gravar batch_id).
func (d *DB) claimDeliveries(ctx context.Context, lease time.Duration, cq claimQuery, postClaim func(*sql.Tx, []int64) error) ([]*NotificationDelivery, error) {
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op após o commit bem-sucedido

	ids, err := selectForClaim(ctx, tx, cq.sql, cq.args...)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, tx.Commit()
	}
	// Põe em sending e empurra next_attempt_at para o futuro: esse instante futuro é o horário de expiração do lease,
	// então "lease não expirado" e "ainda não é hora de retentar" compartilham a mesma expressão de condição, sem precisar de nova coluna.
	ph, idArgs := placeholders(3, ids)
	if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries
SET state=$1, attempts=attempts+1, next_attempt_at=now()+make_interval(secs => $2)
WHERE id IN (`+ph+`)`,
		append([]any{NotifyStateSending, lease.Seconds()}, idArgs...)...); err != nil {
		return nil, err
	}
	if postClaim != nil {
		if err := postClaim(tx, ids); err != nil {
			return nil, err
		}
	}
	out, err := loadDeliveriesTx(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

func selectForClaim(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func loadDeliveriesTx(ctx context.Context, tx *sql.Tx, ids []int64) ([]*NotificationDelivery, error) {
	ph, args := placeholders(1, ids)
	rows, err := tx.QueryContext(ctx, joinedDeliveryQuery+` WHERE d.id IN (`+ph+`) ORDER BY d.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*NotificationDelivery{}
	for rows.Next() {
		dl, err := scanNotificationDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, dl)
	}
	return out, rows.Err()
}

// MarkDeliveriesSent marca um lote de entregas como entregue.
func (d *DB) MarkDeliveriesSent(ctx context.Context, ids []int64) error {
	ph, args := placeholders(2, ids)
	if len(args) == 0 {
		return nil
	}
	_, err := d.ExecContext(ctx, `UPDATE notification_deliveries
SET state=$1, sent_at=now(), last_error='' WHERE id IN (`+ph+`)`, append([]any{NotifyStateSent}, args...)...)
	return err
}

// RescheduleDeliveries devolve um lote de entregas a pending e adia o horário de retentativa.
//
// Devolve a pending em vez de introduzir um novo estado intermediário, para que "quantas chances restam" seja expresso em um único lugar
// (MaxNotifyAttempts), evitando que os ramos da máquina de estados inchem com a política de retentativa.
func (d *DB) RescheduleDeliveries(ctx context.Context, ids []int64, delay time.Duration, errMsg string) error {
	ph, args := placeholders(4, ids)
	if len(args) == 0 {
		return nil
	}
	_, err := d.ExecContext(ctx, `UPDATE notification_deliveries
SET state=$1, next_attempt_at=now()+make_interval(secs => $2), last_error=$3
WHERE id IN (`+ph+`)`,
		append([]any{NotifyStatePending, delay.Seconds(), truncateNotifyError(errMsg)}, args...)...)
	return err
}

// DeferDeliveries devolve um lote de entregas a pending, imediatamente reivindicáveis de novo, e **desfaz a tentativa contada na reivindicação**.
//
// Tem um único uso: quando uma mensagem de resumo é enviada em segmentos conforme o limite de tamanho do canal, os itens que não couberam nesta ficam para o próximo lote.
// Isso não é falha, então não deve consumir o orçamento de retentativas — na reivindicação attempts já foi otimisticamente +1,
// e aqui é preciso subtrair de volta. Senão, um acúmulo de 500 itens seria cortado em 25 segmentos de 20,
// e os itens do final seriam julgados failed por MaxNotifyAttempts já no 3º segmento, sem nunca terem tido erro algum.
//
// GREATEST(...,0) cobre o caso de "alguém reenviou manualmente, zerando attempts, e depois chegou aqui",
// para que a contagem não fique negativa.
func (d *DB) DeferDeliveries(ctx context.Context, ids []int64, reason string) error {
	ph, args := placeholders(3, ids)
	if len(args) == 0 {
		return nil
	}
	_, err := d.ExecContext(ctx, `UPDATE notification_deliveries
SET state=$1, attempts=GREATEST(attempts-1, 0), next_attempt_at=now(), last_error=$2
WHERE id IN (`+ph+`)`,
		append([]any{NotifyStatePending, truncateNotifyError(reason)}, args...)...)
	return err
}

// FailDeliveries marca um lote de entregas como falha final, à espera de reenvio manual no histórico de entregas.
func (d *DB) FailDeliveries(ctx context.Context, ids []int64, errMsg string) error {
	// Os placeholders começam em $3: $1 é state e $2 é last_error.
	ph, args := placeholders(3, ids)
	if len(args) == 0 {
		return nil
	}
	_, err := d.ExecContext(ctx, `UPDATE notification_deliveries SET state=$1, last_error=$2 WHERE id IN (`+ph+`)`,
		append([]any{NotifyStateFailed, truncateNotifyError(errMsg)}, args...)...)
	return err
}

// RetryNotificationDelivery reenvia manualmente uma entrega: redefine para pending, zera a contagem de tentativas e
// a torna vencida imediatamente. Zerar a contagem é deliberado — quando uma pessoa clica em "reenviar", subentende-se que a causa das falhas anteriores foi tratada,
// e limitá-la pela contagem antiga não faz sentido.
func (d *DB) RetryNotificationDelivery(ctx context.Context, id int64) error {
	res, err := d.ExecContext(ctx, `UPDATE notification_deliveries
SET state=$2, attempts=0, next_attempt_at=now(), last_error=''
WHERE id=$1 AND state IN ($3,$4)`, id, NotifyStatePending, NotifyStateFailed, NotifyStateSkipped)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("a entrega %d não existe ou o estado atual não permite reenvio", id)
	}
	return nil
}

// NotificationDeliveryFilter são as condições de consulta do histórico de entregas.
type NotificationDeliveryFilter struct {
	ChannelID int64
	State     string
	EventKind string
}

func (f NotificationDeliveryFilter) where() (string, []any) {
	var conds []string
	var args []any
	if f.ChannelID > 0 {
		args = append(args, f.ChannelID)
		conds = append(conds, fmt.Sprintf("d.channel_id=$%d", len(args)))
	}
	if f.State != "" {
		args = append(args, f.State)
		conds = append(conds, fmt.Sprintf("d.state=$%d", len(args)))
	}
	if f.EventKind != "" {
		args = append(args, f.EventKind)
		conds = append(conds, fmt.Sprintf("e.kind=$%d", len(args)))
	}
	if len(conds) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

// ListNotificationDeliveries retorna o histórico de entregas paginado, as mais novas primeiro.
func (d *DB) ListNotificationDeliveries(ctx context.Context, f NotificationDeliveryFilter, page, pageSize int) ([]*NotificationDelivery, int, error) {
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	where, args := f.where()

	var total int
	if err := d.QueryRowContext(ctx, `SELECT count(*) FROM notification_deliveries d
JOIN notification_events e ON e.id = d.event_id`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	q := fmt.Sprintf("%s%s ORDER BY d.id DESC LIMIT $%d OFFSET $%d",
		joinedDeliveryQuery, where, len(args)+1, len(args)+2)
	rows, err := d.QueryContext(ctx, q, append(args, pageSize, (page-1)*pageSize)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*NotificationDelivery{}
	for rows.Next() {
		dl, err := scanNotificationDelivery(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, dl)
	}
	return out, total, rows.Err()
}

// truncateNotifyError corta a mensagem de erro para um tamanho aceitável pela coluna. O corpo de resposta devolvido pelo canal pode ser muito longo
// (especialmente quando um Webhook genérico bate em um serviço próprio); sem truncar, o payload da lista de histórico incharia.
func truncateNotifyError(msg string) string {
	const max = 500
	if len(msg) <= max {
		return msg
	}
	// Recua no limite de caractere, para não deixar meio caractere UTF-8 e o frontend exibir texto corrompido.
	cut := max
	for cut > 0 && !isUTF8Start(msg[cut]) {
		cut--
	}
	return msg[:cut] + "…"
}

func isUTF8Start(b byte) bool { return b&0xC0 != 0x80 }

// placeholders gera a string de placeholders $n a partir de start e os argumentos correspondentes, para uso em IN (...).
// Por exemplo start=3, ids=[7,8] -> "$3,$4", [7,8].
func placeholders(start int, ids []int64) (string, []any) {
	ph := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids))
	for i, id := range ids {
		ph = append(ph, fmt.Sprintf("$%d", start+i))
		args = append(args, id)
	}
	return strings.Join(ph, ","), args
}
