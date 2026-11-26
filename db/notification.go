package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Autumn-27/artex/notify"
)

// Este arquivo é a camada de configuração de canais de push IM e de eventos. Para a reivindicação das tarefas de entrega e as transições de estado, ver
// db/notification_delivery.go.
//
// Duas invariantes que devem ser mantidas ao mexer neste arquivo:
//
//  1. A transação que grava a vulnerabilidade (RecordFindingTx) só chama InsertNotificationEventTx para um insert cego,
//     sem ler nenhuma tabela relacionada a notificações e sem fazer correspondência de filtros. Qualquer leitura introduzida aqui pode, por causa de
//     um filtro mal configurado pelo usuário, contaminar ou até abortar a transação de gravação da vulnerabilidade.
//  2. A correspondência de filtros nunca gera erro: configuração malformada é sempre tratada como "atingida" (ver notify.Match). Melhor
//     empurrar a mais do que deixar de empurrar.

// ErrNotificationChannelNotFound: o canal não existe.
var ErrNotificationChannelNotFound = errors.New("o canal de notificação não existe")

// Estados de entrega.
const (
	NotifyStatePending = "pending" // pendente de envio
	NotifyStateSending = "sending" // reivindicado por algum dispatcher, lease ainda não expirou
	NotifyStateSent    = "sent"    // entregue
	NotifyStateFailed  = "failed"  // tentativas esgotadas ou falha permanente, pode ser reenviado manualmente
	NotifyStateSkipped = "skipped" // canal desativado, não será mais enviado
)

// Modos de push.
const (
	NotifyModeRealtime = "realtime"
	NotifyModeDigest   = "digest"
)

// ValidNotifyMode valida o modo de push por lista de permissão (mesmo motivo de findings.status: sem CHECK no banco,
// para facilitar extensões futuras).
func ValidNotifyMode(m string) bool {
	return m == NotifyModeRealtime || m == NotifyModeDigest
}

// NotificationChannel é a configuração de uma instância de canal. Config e Filter mantêm o JSON bruto,
// e a interpretação fica com o pacote notify — a camada db não entende o significado dos seus campos.
type NotificationChannel struct {
	ID     int64           `json:"id"`
	Name   string          `json:"name"`
	Kind   string          `json:"kind"`
	Mode   string          `json:"mode"`
	Config json.RawMessage `json:"config"`
	Filter json.RawMessage `json:"filter"`
	// Enabled é um ponteiro para distinguir "o campo não foi enviado" de "false explícito" —
	// o controle de interruptor do frontend só envia os campos que foram alterados.
	Enabled    *bool     `json:"enabled,omitempty"`
	RatePerMin int       `json:"rate_per_min"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// IsEnabled retorna se o canal está habilitado; quando Enabled é nil (não carregado), é tratado como habilitado.
func (c *NotificationChannel) IsEnabled() bool { return c.Enabled == nil || *c.Enabled }

// NotificationEvent é um fato de evento.
type NotificationEvent struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	FindingID int64           `json:"finding_id"`
	Snapshot  json.RawMessage `json:"snapshot"`
	CreatedAt time.Time       `json:"created_at"`
}

const notificationChannelCols = `id, name, kind, enabled, config, mode, filter, rate_per_min, created_at, updated_at`

func scanNotificationChannel(sc interface{ Scan(...any) error }) (*NotificationChannel, error) {
	var c NotificationChannel
	var enabled bool
	if err := sc.Scan(&c.ID, &c.Name, &c.Kind, &enabled, &c.Config, &c.Mode, &c.Filter, &c.RatePerMin, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	c.Enabled = &enabled
	return &c, nil
}

// ListNotificationChannels retorna todas as instâncias de canal, habilitados primeiro e, no mesmo nível, por id.
// A ordenação fica no SQL para que a UI e o dispatcher vejam a mesma ordem estável.
func (d *DB) ListNotificationChannels(ctx context.Context) ([]*NotificationChannel, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+notificationChannelCols+` FROM notification_channels
ORDER BY enabled DESC, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*NotificationChannel{}
	for rows.Next() {
		c, err := scanNotificationChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// NotificationChannelByID obtém um único canal.
func (d *DB) NotificationChannelByID(ctx context.Context, id int64) (*NotificationChannel, error) {
	row := d.QueryRowContext(ctx, `SELECT `+notificationChannelCols+` FROM notification_channels WHERE id=$1`, id)
	c, err := scanNotificationChannel(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotificationChannelNotFound
	}
	return c, err
}

// SaveNotificationChannel cria ou atualiza um canal.
//
// Na atualização, só sobrescreve os campos explicitamente fornecidos pelo chamador (não nil / não vazios), assim o frontend pode enviar um formulário
// de gaveta com edição parcial, sem precisar devolver os campos de config que ele não exibiu — devolvê-los causaria
// o acidente de "o valor mascarado sobrescrever a chave real".
func (d *DB) SaveNotificationChannel(ctx context.Context, c *NotificationChannel) (int64, error) {
	if c.Mode == "" {
		c.Mode = NotifyModeRealtime
	}
	// Aqui deliberadamente **não** se processa o 0: 0 é uma configuração válida, com o significado de "sem limite de taxa".
	//
	// Já foi escrito como `if c.RatePerMin <= 0 { c.RatePerMin = valor padrão }`, com a intenção de "dar um padrão seguro quando não especificado",
	// mas isso engolia também o "0 definido explicitamente" — a documentação, as dicas da UI e
	// takeTokens interpretam 0 como sem limite, e só este ponto mudava silenciosamente para 20 (DingTalk/WeCom/Telegram)
	// ou 100 (Feishu); o operador achava que tinha liberado o limite, mas na prática ficava preso a 20/minuto sem nenhum aviso.
	//
	// A diferença entre "não especificado" e "0 explícito" só o chamador conhece (campo omitido no corpo da requisição vs 0 passado explicitamente),
	// então o valor padrão é preenchido pela camada server quando o campo é omitido; ver notifyCreateChannel.
	if c.RatePerMin < 0 {
		return 0, errors.New("o valor de limite de taxa não pode ser negativo")
	}
	if c.Config == nil {
		c.Config = json.RawMessage(`{}`)
	}
	if c.Filter == nil {
		c.Filter = json.RawMessage(`{}`)
	}
	enabled := c.IsEnabled()

	if c.ID == 0 {
		var id int64
		err := d.QueryRowContext(ctx, `INSERT INTO notification_channels(name,kind,enabled,config,mode,filter,rate_per_min)
VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
			c.Name, c.Kind, enabled, string(c.Config), c.Mode, string(c.Filter), c.RatePerMin).Scan(&id)
		return id, err
	}
	res, err := d.ExecContext(ctx, `UPDATE notification_channels
SET name=$2, kind=$3, enabled=$4, config=$5, mode=$6, filter=$7, rate_per_min=$8
WHERE id=$1`,
		c.ID, c.Name, c.Kind, enabled, string(c.Config), c.Mode, string(c.Filter), c.RatePerMin)
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, ErrNotificationChannelNotFound
	}
	return c.ID, nil
}

// SetNotificationChannelEnabled alterna entre ligado e desligado.
//
// Ao desativar um canal, as entregas dele ainda não enviadas também são marcadas como skipped: senão, ao reativar,
// chegaria de repente um lote de vulnerabilidades antigas "acumuladas durante a desativação", sem atualidade e fáceis de confundir com novas.
func (d *DB) SetNotificationChannelEnabled(ctx context.Context, id int64, enabled bool) error {
	return d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE notification_channels SET enabled=$2 WHERE id=$1`, id, enabled)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotificationChannelNotFound
		}
		if !enabled {
			if _, err := tx.ExecContext(ctx, `UPDATE notification_deliveries SET state=$2, last_error=$3
WHERE channel_id=$1 AND state IN ($4,$5)`,
				id, NotifyStateSkipped, "canal desativado", NotifyStatePending, NotifyStateSending); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteNotificationChannel exclui o canal. O histórico de entregas dele é excluído em cascata pela chave estrangeira
// (sem a configuração do canal, o histórico não tem como ser interpretado).
func (d *DB) DeleteNotificationChannel(ctx context.Context, id int64) error {
	res, err := d.ExecContext(ctx, `DELETE FROM notification_channels WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotificationChannelNotFound
	}
	return nil
}

// RecordNotificationEventTx grava um evento de push na transação do chamador, em **melhor esforço**.
//
// Esta é a única alteração relacionada a notificações no caminho de gravação de vulnerabilidades: um INSERT, sem ler nenhuma tabela, sem conhecer canais,
// sem executar filtros. O commit da transação garante que "vulnerabilidade gravada" e "tarefa de push existente" sejam atomicamente consistentes,
// sem a janela em que o commit tem sucesso mas nada é enfileirado e a mensagem se perde para sempre.
//
// Dois pontos-chave de projeto, nenhum escrito por acaso:
//
//  1. **Por que usar SAVEPOINT**: no PostgreSQL, o erro de qualquer instrução dentro da transação coloca a transação inteira no estado
//     aborted, e depois disso todas as instruções (inclusive COMMIT) falham. Logo, "ignorar o erro deste INSERT
//     e deixar o chamador dar commit" é impossível no PG — a menos que um savepoint isole o erro nesta única instrução.
//     Sem o savepoint, só resta a opção de "reverter tudo".
//
//  2. **Por que reverter tudo está errado**: o push é uma conveniência, o registro da vulnerabilidade é o próprio produto. Um problema na tabela de
//     notificações (banco antigo sem migrar, falha transitória de disco) não deve impedir que uma vulnerabilidade de alto risco seja gravada. Por isso aqui isolamos
//     o erro, registramos em log e retornamos false, deixando a gravação da vulnerabilidade fazer commit normalmente — o custo é perder este push.
//     Retornar bool e não error é deliberado: o chamador não deve tratá-lo como um erro que afeta o sucesso da gravação.
func RecordNotificationEventTx(ctx context.Context, tx *sql.Tx, kind string, findingID int64, snap notify.Snapshot) bool {
	raw, err := json.Marshal(snap)
	if err != nil {
		log.Printf("[notify] falha ao serializar o evento de push finding=%d: %v", findingID, err)
		return false
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT notify_event`); err != nil {
		log.Printf("[notify] falha ao criar o savepoint finding=%d: %v", findingID, err)
		return false
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO notification_events(kind,finding_id,snapshot) VALUES($1,$2,$3)`,
		kind, findingID, string(raw)); err != nil {
		log.Printf("[notify] falha ao gravar o evento de push finding=%d (o registro da vulnerabilidade não é afetado): %v", findingID, err)
		// Reverte ao savepoint, resgatando a transação do estado aborted.
		if _, rbErr := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT notify_event`); rbErr != nil {
			log.Printf("[notify] falha ao reverter ao savepoint finding=%d: %v", findingID, rbErr)
		}
		return false
	}
	// Libera o savepoint, evitando acumular savepoints inúteis em transações longas.
	_, _ = tx.ExecContext(ctx, `RELEASE SAVEPOINT notify_event`)
	return true
}

// AddNotificationEvent é a versão em transação independente de InsertNotificationEventTx, para pontos de chamada que não estão
// dentro de uma transação existente (como o "enviar mensagem de teste" do canal, que não tem finding real).
func (d *DB) AddNotificationEvent(ctx context.Context, kind string, findingID int64, snap notify.Snapshot) (int64, error) {
	raw, err := json.Marshal(snap)
	if err != nil {
		return 0, fmt.Errorf("falha ao serializar o snapshot do evento de notificação: %w", err)
	}
	var id int64
	err = d.QueryRowContext(ctx, `INSERT INTO notification_events(kind,finding_id,snapshot) VALUES($1,$2,$3) RETURNING id`,
		kind, findingID, string(raw)).Scan(&id)
	return id, err
}

// FanOutPendingEvents expande os eventos de vulnerabilidade ainda não despachados em tarefas de entrega, conforme os canais atualmente habilitados,
// e retorna o número de eventos processados e de entregas criadas nesta rodada.
//
// Toda a rodada acontece em uma transação: os eventos são reivindicados com FOR UPDATE SKIP LOCKED, e vários processos rodando ao mesmo tempo
// também pegam linhas diferentes (a reivindicação da fila de arquivamento do projeto usa a mesma técnica, ver
// completeNextArchiveJob em db/task_archives.go).
//
// A correspondência de filtros fica deliberadamente no lado Go e não no SQL: o filtro do canal é um JSONB de campos opcionais,
// e expressar em SQL a correspondência das seis combinações tornaria a consulta difícil de manter, enquanto o número de canais é "umas poucas configuradas à mão",
// carregar tudo e comparar um a um em memória é mais rápido e mais fácil de testar.
//
// Eventos que não atingem nenhum canal também são marcados como fanned_out — senão ficariam para sempre no conjunto de pendentes de despacho,
// sendo reescaneados a cada tick.
func (d *DB) FanOutPendingEvents(ctx context.Context, limit int) (eventCount, deliveryCount int, err error) {
	if limit <= 0 {
		limit = 200
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op após o commit bem-sucedido

	channels, err := listEnabledNotificationChannelsTx(ctx, tx)
	if err != nil {
		return 0, 0, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, kind, finding_id, snapshot FROM notification_events
WHERE NOT fanned_out ORDER BY id FOR UPDATE SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return 0, 0, err
	}
	var (
		events      []NotificationEvent
		parsedSnaps []notify.Snapshot
	)
	for rows.Next() {
		var ev NotificationEvent
		if err := rows.Scan(&ev.ID, &ev.Kind, &ev.FindingID, &ev.Snapshot); err != nil {
			rows.Close()
			return 0, 0, err
		}
		var snap notify.Snapshot
		// O snapshot é escrito por nós mesmos e em teoria sempre é interpretável; uma falha de parsing não interrompe o fluxo de entrega,
		// mas o evento será ignorado por todos os canais com condições de filtro, por ter todos os campos vazios — melhor empurrar um a menos
		// do que deixar uma linha ruim travar a fila inteira.
		_ = json.Unmarshal(ev.Snapshot, &snap)
		// kind vale o valor da linha: o do snapshot é uma cópia para renderização e pode ter sido gravado por versões antigas.
		snap.Kind = ev.Kind
		events = append(events, ev)
		parsedSnaps = append(parsedSnaps, snap)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(events) == 0 {
		return 0, 0, tx.Commit()
	}

	type pending struct {
		eventID   int64
		channelID int64
	}
	var toInsert []pending
	for i, snap := range parsedSnaps {
		for _, ch := range channels {
			if !notify.Match(notify.ParseFilter(ch.Filter), snap) {
				continue
			}
			toInsert = append(toInsert, pending{eventID: events[i].ID, channelID: ch.ID})
		}
	}
	if len(toInsert) > 0 {
		var (
			vals []string
			args []any
		)
		for _, p := range toInsert {
			vals = append(vals, fmt.Sprintf("($%d,$%d)", len(args)+1, len(args)+2))
			args = append(args, p.eventID, p.channelID)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO notification_deliveries(event_id,channel_id) VALUES `+strings.Join(vals, ","), args...); err != nil {
			return 0, 0, err
		}
	}

	// Marca os eventos desta rodada como despachados. Eventos que não atingiram nenhum canal também são marcados (ver o comentário da função).
	ids := make([]string, 0, len(events))
	markArgs := make([]any, 0, len(events))
	for _, ev := range events {
		markArgs = append(markArgs, ev.ID)
		ids = append(ids, fmt.Sprintf("$%d", len(markArgs)))
	}
	if _, err := tx.ExecContext(ctx, `UPDATE notification_events SET fanned_out=true WHERE id IN (`+strings.Join(ids, ",")+`)`, markArgs...); err != nil {
		return 0, 0, err
	}
	return len(events), len(toInsert), tx.Commit()
}

// listEnabledNotificationChannelsTx obtém, dentro da transação, os canais habilitados. São poucos,
// sem paginação nem cache — o cache traria um problema extra de temporização: "quando a mudança de configuração passa a valer".
func listEnabledNotificationChannelsTx(ctx context.Context, tx *sql.Tx) ([]*NotificationChannel, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, name, kind, config, mode, filter, rate_per_min
FROM notification_channels WHERE enabled ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*NotificationChannel{}
	for rows.Next() {
		var c NotificationChannel
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.Config, &c.Mode, &c.Filter, &c.RatePerMin); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// NotificationAssetNames resolve ids de ativos em nomes de exibição curtos, para uso nas mensagens de push.
//
// A ordem retornada é a mesma da entrada, e o tamanho pode ser menor que a entrada (ids inexistentes são ignorados). Manter a ordem de entrada serve
// para que, nas várias entregas da mesma vulnerabilidade, a ordem dos ativos na mensagem seja estável — senão, a ordem dos ativos
// na mensagem recebida após uma retentativa mudaria e seria lida como "os ativos mudaram".
func (d *DB) NotificationAssetNames(ctx context.Context, ids []int64) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	ph, args := placeholders(1, ids)
	rows, err := d.QueryContext(ctx, `SELECT id, type, domain, ip, url, app_name, bundle_id FROM assets WHERE id IN (`+ph+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	labels := map[int64]string{}
	for rows.Next() {
		var (
			id                int64
			typ               string
			domain, ip, url   sql.NullString
			appName, bundleID sql.NullString
		)
		if err := rows.Scan(&id, &typ, &domain, &ip, &url, &appName, &bundleID); err != nil {
			return nil, err
		}
		labels[id] = assetDisplayName(typ, domain.String, ip.String, url.String, appName.String, bundleID.String)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ids))
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if label, ok := labels[id]; ok && label != "" {
			out = append(out, label)
		}
	}
	return out, nil
}

// assetDisplayName escolhe o identificador mais reconhecível conforme o tipo do ativo.
// O fallback retorna string vazia, e o chamador decide como apresentar "um ativo cujo nome não foi obtido" — esta função não inventa placeholders,
// senão ruídos como "Ativo #42" se misturariam à mensagem de push e o leitor pensaria que é um domínio real.
func assetDisplayName(typ, domain, ip, url, appName, bundleID string) string {
	pick := func(vals ...string) string {
		for _, v := range vals {
			if strings.TrimSpace(v) != "" {
				return v
			}
		}
		return ""
	}
	switch typ {
	case "root_domain", "subdomain":
		return domain
	case "ip":
		return ip
	case "app":
		return pick(appName, bundleID)
	case "service", "endpoint":
		return pick(url, domain, ip)
	default:
		return pick(domain, ip, url, appName)
	}
}

// SetFindingStatusWithNotify atualiza o status de tratamento da vulnerabilidade e, na mesma transação, registra um evento de push de
// mudança de status.
//
// Retorna from = status antes da mudança; found = se a vulnerabilidade existe; notified = se o evento foi registrado com sucesso.
//
// Três comportamentos deliberados:
//   - Não registra evento quando o status não mudou de fato. O drawer do frontend reenviando o mesmo valor, ou um script de automação
//     repetindo de forma idempotente, não devem produzir ruído de push.
//   - Quando a vulnerabilidade não existe, retorna found=false sem gravar nada, e o chamador traduz isso em 404.
//   - Falha ao registrar o evento não afeta a atualização do status (ver a explicação do savepoint em RecordNotificationEventTx),
//     então com notified=false o status já foi alterado com sucesso e o chamador não deve devolver erro por isso.
func (d *DB) SetFindingStatusWithNotify(ctx context.Context, id int64, status string) (from string, found bool, notified bool, err error) {
	err = d.WithEvidenceTx(ctx, func(tx *sql.Tx) error {
		var txErr error
		from, found, _, notified, txErr = SetFindingStatusTx(ctx, tx, id, status)
		return txErr
	})
	return from, found, notified, err
}

// SetFindingStatusTx atualiza o status da vulnerabilidade **dentro da transação do chamador** e registra o evento de push de mudança de status.
//
// Foi extraída como função em nível de transação para que todos os caminhos que mudam o status compartilhem a mesma semântica — antes, só
// patchFinding usava a versão com notificação, e **quando a conclusão do reteste era "corrigido"** (aquele `UPDATE findings SET status=...`
// em finding_retests) gravava direto no banco, então os canais configurados com
// `on_status_change` não recebiam nenhum push desse tipo de transição: o status mudava em silêncio na interface,
// e a operação só descobria ao abrir a plataforma.
//
// Retorna from = status antes da mudança, found = se a vulnerabilidade existe, changed = se o status de fato mudou,
// notified = se o evento foi registrado com sucesso (falha no registro não afeta a atualização do status, ver RecordNotificationEventTx).
func SetFindingStatusTx(ctx context.Context, tx *sql.Tx, id int64, status string) (from string, found bool, changed bool, notified bool, err error) {
	var (
		vulnclass, name, severity, summary string
		taskID                             sql.NullInt64
		assetIDs                           []byte
	)
	scanErr := tx.QueryRowContext(ctx, `SELECT vulnclass, name, severity, summary, task_id, asset_ids, status
FROM findings WHERE id=$1 FOR UPDATE`, id).
		Scan(&vulnclass, &name, &severity, &summary, &taskID, &assetIDs, &from)
	if scanErr == sql.ErrNoRows {
		return "", false, false, false, nil
	}
	if scanErr != nil {
		return "", false, false, false, scanErr
	}
	found = true
	if from == status {
		// Se o status não mudou de fato, não registra o evento: reenviar o mesmo valor ou repetir de forma idempotente não deve
		// produzir ruído de push.
		return from, true, false, false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE findings SET status=$2 WHERE id=$1`, id, status); err != nil {
		return from, true, false, false, err
	}
	var assets []int64
	_ = json.Unmarshal(assetIDs, &assets)
	notified = RecordNotificationEventTx(ctx, tx, notify.EventFindingStatusChanged, id, notify.Snapshot{
		Kind:       notify.EventFindingStatusChanged,
		FindingID:  id,
		TaskID:     taskID.Int64,
		VulnClass:  vulnclass,
		Name:       name,
		Severity:   severity,
		Summary:    summary,
		AssetIDs:   assets,
		FromStatus: from,
		ToStatus:   status,
	})
	return from, true, true, notified, nil
}

// NotificationStats são as contagens de visão geral no topo da página de notificações.
type NotificationStats struct {
	Channels     int   `json:"channels"`
	ChannelsOn   int   `json:"channels_on"`
	Pending      int   `json:"pending"`
	Failed       int   `json:"failed"`
	SentToday    int   `json:"sent_today"`
	BacklogAgeMS int64 `json:"backlog_age_ms"` // idade em milissegundos da entrega pendente mais antiga
}

// NotificationStatsSnapshot resume a saúde do sistema de notificações.
// BacklogAgeMS é o indicador mais direto de "o push travou?" — muito mais útil que a contagem de pending,
// pois a diferença entre 3 itens acumulados e 3 itens acumulados pode ser de 3 segundos a 3 horas.
func (d *DB) NotificationStatsSnapshot(ctx context.Context) (*NotificationStats, error) {
	var s NotificationStats
	if err := d.QueryRowContext(ctx, `SELECT
    (SELECT count(*) FROM notification_channels),
    (SELECT count(*) FROM notification_channels WHERE enabled),
    (SELECT count(*) FROM notification_deliveries WHERE state IN ($1,$2)),
    (SELECT count(*) FROM notification_deliveries WHERE state=$3),
    (SELECT count(*) FROM notification_deliveries WHERE state=$4 AND sent_at >= date_trunc('day', now())),
    COALESCE((SELECT EXTRACT(EPOCH FROM (now() - min(created_at))) * 1000 FROM notification_deliveries WHERE state=$1), 0)::bigint`,
		NotifyStatePending, NotifyStateSending, NotifyStateFailed, NotifyStateSent).
		Scan(&s.Channels, &s.ChannelsOn, &s.Pending, &s.Failed, &s.SentToday, &s.BacklogAgeMS); err != nil {
		return nil, err
	}
	return &s, nil
}
