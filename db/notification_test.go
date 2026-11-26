package db

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Autumn-27/artex/notify"
)

// Os casos deste arquivo conectam de verdade ao PostgreSQL (são pulados quando não há banco). Estes SQLs usam
// FOR UPDATE SKIP LOCKED, make_interval, JSONB e montagem de placeholders IN(...) com várias linhas,
// construções que "compilam mas podem falhar em tempo de execução" e só contam como validadas se rodarem de fato.

func notifyTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// newTestChannel cria um canal, excluído automaticamente ao fim do teste.
func newTestChannel(t *testing.T, d *DB, kind, mode string, filter string) *NotificationChannel {
	t.Helper()
	if filter == "" {
		filter = `{}`
	}
	ch := &NotificationChannel{
		Name:       "Canal de teste-" + t.Name(),
		Kind:       kind,
		Mode:       mode,
		Config:     json.RawMessage(`{"webhook":"https://example.com/hook"}`),
		Filter:     json.RawMessage(filter),
		RatePerMin: 100,
	}
	id, err := d.SaveNotificationChannel(context.Background(), ch)
	if err != nil {
		t.Fatalf("falha ao criar o canal: %v", err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_channels WHERE id=$1`, id) })
	ch.ID = id
	return ch
}

// addTestEvent grava um evento diretamente (sem passar por finding), para testar despacho e entrega.
func addTestEvent(t *testing.T, d *DB, kind string, findingID int64, snap notify.Snapshot) int64 {
	t.Helper()
	snap.Kind = kind
	snap.FindingID = findingID
	id, err := d.AddNotificationEvent(context.Background(), kind, findingID, snap)
	if err != nil {
		t.Fatalf("falha ao gravar o evento: %v", err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_events WHERE id=$1`, id) })
	return id
}

func TestNotificationAssetNamesResolvesAndPreservesOrder(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	// Os três tipos de ativo têm cada um seu critério de exibição: domínio, IP, URL.
	insertAsset := func(query, value string) int64 {
		t.Helper()
		var id int64
		if err := d.QueryRow(query, value).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	domID := insertAsset(`INSERT INTO assets(type, domain) VALUES('subdomain',$1) RETURNING id`, "a.example.com")
	ipID := insertAsset(`INSERT INTO assets(type, ip) VALUES('ip',$1) RETURNING id`, "10.1.2.3")
	svcID := insertAsset(`INSERT INTO assets(type, url) VALUES('service',$1) RETURNING id`, "https://a.example.com/admin")
	t.Cleanup(func() {
		d.Exec(`DELETE FROM assets WHERE id IN ($1,$2,$3)`, domID, ipID, svcID)
	})

	// A ordem de entrada é deliberadamente embaralhada e inclui um id inexistente.
	got, err := d.NotificationAssetNames(ctx, []int64{svcID, 999999999, domID, ipID, svcID})
	if err != nil {
		t.Fatalf("falha ao resolver os nomes dos ativos: %v", err)
	}
	want := []string{"https://a.example.com/admin", "a.example.com", "10.1.2.3"}
	if len(got) != len(want) {
		t.Fatalf("quantidade de nomes de ativos divergente, esperado %v obtido %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ordem/valor divergente, esperado %v obtido %v", want, got)
		}
	}
}

// TestRecordNotificationEventTxUnwindsOnFailure é o caso central do mecanismo de savepoint:
// dentro da transação, faz primeiro a gravação em notification_events falhar necessariamente (adicionando uma restrição temporária sempre falsa),
// e afirma que (1) a função reporta false e (2) a transação não entrou no estado aborted, e as instruções seguintes ainda executam.
//
// Sem savepoint, o PostgreSQL invalida a transação inteira e qualquer instrução posterior falha com
// "current transaction is aborted" — exatamente o caminho de falha em que "um problema na tabela de notificações
// impede que a vulnerabilidade seja gravada".
//
// Aqui se termina deliberadamente com **ROLLBACK e não COMMIT**: ALTER TABLE é transacional no PG,
// e uma vez feito o commit, a restrição temporária ficaria para sempre no schema, derrubando todos os casos seguintes.
// O rollback desfaz o DDL automaticamente, sem limpeza manual. A asserção só precisa de "a transação continua viva",
// não precisa de um commit de verdade.
func TestRecordNotificationEventTxUnwindsOnFailure(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	// Limpeza defensiva: se execuções anteriores deixaram esta restrição, remove-a primeiro.
	if _, err := d.Exec(`ALTER TABLE notification_events DROP CONSTRAINT IF EXISTS notify_test_never`); err != nil {
		t.Fatal(err)
	}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback() //nolint:errcheck // desfaz a restrição temporária, ver o comentário da função

	// NOT VALID: restringe só as linhas gravadas daqui em diante, sem validar os eventos históricos já existentes no banco
	// (senão linhas existentes que violam a restrição impediriam de adicioná-la).
	if _, err := tx.ExecContext(ctx, `ALTER TABLE notification_events ADD CONSTRAINT notify_test_never CHECK (false) NOT VALID`); err != nil {
		t.Fatalf("falha ao adicionar a restrição temporária: %v", err)
	}
	if RecordNotificationEventTx(ctx, tx, notify.EventFindingCreated, 1, notify.Snapshot{Severity: "high"}) {
		t.Fatal("reportou gravação bem-sucedida mesmo sob uma restrição que necessariamente falha")
	}
	// Asserção-chave: a transação ainda pode ser usada.
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatalf("a transação foi contaminada (o savepoint não surtiu efeito): %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("falha no rollback: %v", err)
	}
	// Confirma que o DDL foi desfeito com o rollback, para não deixar minas para os casos seguintes.
	var exists bool
	if err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conname='notify_test_never')`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("a restrição temporária não foi desfeita pelo rollback e contaminaria os casos seguintes")
	}
}

func TestFanOutRoutesEventsByFilter(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	all := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	onlyCritical := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{"min_severity":"critical"}`)
	sqlOnly := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{"vulnclass_include":["SQL"]}`)

	highSQL := addTestEvent(t, d, notify.EventFindingCreated, 1001, notify.Snapshot{Severity: "high", VulnClass: "Injeção SQL"})
	lowXSS := addTestEvent(t, d, notify.EventFindingCreated, 1002, notify.Snapshot{Severity: "low", VulnClass: "XSS"})
	criticalXSS := addTestEvent(t, d, notify.EventFindingCreated, 1003, notify.Snapshot{Severity: "critical", VulnClass: "XSS"})

	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatalf("falha no despacho: %v", err)
	}

	cases := []struct {
		name    string
		eventID int64
		channel int64
		want    bool
	}{
		{"canal que recebe tudo recebe high", highSQL, all.ID, true},
		{"canal que recebe tudo recebe low", lowXSS, all.ID, true},
		{"canal só-crítico ignora high", highSQL, onlyCritical.ID, false},
		{"canal só-crítico recebe critical", criticalXSS, onlyCritical.ID, true},
		{"canal só-SQL recebe SQL", highSQL, sqlOnly.ID, true},
		{"canal só-SQL ignora XSS", lowXSS, sqlOnly.ID, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var exists bool
			if err := d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notification_deliveries WHERE event_id=$1 AND channel_id=$2)`,
				tc.eventID, tc.channel).Scan(&exists); err != nil {
				t.Fatal(err)
			}
			if exists != tc.want {
				t.Fatalf("existência da entrega: esperado %v obtido %v", tc.want, exists)
			}
		})
	}

	// Despachar de novo não deve produzir entregas duplicadas (idempotência de fanned_out).
	events, deliveries, err := d.FanOutPendingEvents(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if events != 0 || deliveries != 0 {
		t.Fatalf("eventos já despachados não devem ser processados de novo, obtido events=%d deliveries=%d", events, deliveries)
	}
}

// TestFanOutMarksEventsWithNoMatchingChannel cobre o caso de "evento que não atingiu nenhum canal".
// Esses eventos devem ser marcados como despachados do mesmo jeito, senão ficariam para sempre no conjunto de pendentes de despacho, reescaneados a cada tick.
func TestFanOutMarksEventsWithNoMatchingChannel(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	pick := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{"vulnclass_include":["Tipo que nunca casa"]}`)
	_ = pick

	ev := addTestEvent(t, d, notify.EventFindingCreated, 2001, notify.Snapshot{Severity: "high", VulnClass: "XSS"})
	_, deliveries, err := d.FanOutPendingEvents(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if deliveries != 0 {
		t.Fatalf("não deveria produzir entregas, obtido %d", deliveries)
	}
	var fanned bool
	if err := d.QueryRowContext(ctx, `SELECT fanned_out FROM notification_events WHERE id=$1`, ev).Scan(&fanned); err != nil {
		t.Fatal(err)
	}
	if !fanned {
		t.Fatal("eventos que não atingiram canal também devem ser marcados como despachados, senão serão reescaneados indefinidamente")
	}
}

func TestClaimRealtimeDeliveriesHonorsLeaseAndMode(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	realtime := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	digest := newTestChannel(t, d, notify.KindDingTalk, NotifyModeDigest, `{}`)

	addTestEvent(t, d, notify.EventFindingCreated, 3001, notify.Snapshot{Severity: "high", VulnClass: "XSS"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}

	// A reivindicação em tempo real só deve pegar a do canal realtime, sem mexer nas do canal digest.
	got, err := d.ClaimRealtimeDeliveries(ctx, realtime.ID, 10, time.Minute)
	if err != nil {
		t.Fatalf("falha na reivindicação: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("deveria reivindicar 1, obtido %d", len(got))
	}
	if got[0].State != NotifyStateSending || got[0].Attempts != 1 {
		t.Fatalf("após a reivindicação deveria ser sending com attempts=1, obtido state=%s attempts=%d", got[0].State, got[0].Attempts)
	}
	// O contexto de renderização carregado por join deve estar completo (configuração do canal + snapshot do evento + finding id).
	if got[0].Channel == nil || len(got[0].Channel.Config) == 0 {
		t.Fatal("o resultado da reivindicação não tem a configuração do canal, a renderização falharia")
	}
	if got[0].FindingID != 3001 {
		t.Fatalf("finding id não veio do evento, obtido %d", got[0].FindingID)
	}

	// Com o lease não expirado, a segunda reivindicação deve vir vazia — é a garantia de que "a mesma linha não é entregue
	// por dois dispatchers ao mesmo tempo".
	again, err := d.ClaimRealtimeDeliveries(ctx, realtime.ID, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("dentro do prazo do lease não deve reivindicar de novo, obtido %d", len(again))
	}

	// As entregas do canal digest não devem ser tocadas pela reivindicação em tempo real.
	left, err := d.ClaimRealtimeDeliveries(ctx, digest.ID, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("a reivindicação em tempo real não deve pegar entregas do canal digest, obtido %d", len(left))
	}
}

// TestClaimExpiredLeaseRecovers cobre a autorrecuperação de quedas: um processo que morre no meio da entrega deixa uma linha sending,
// e após o lease expirar ela precisa poder ser reivindicada de novo, senão essa entrega ficaria travada para sempre.
func TestClaimExpiredLeaseRecovers(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	addTestEvent(t, d, notify.EventFindingCreated, 4001, notify.Snapshot{Severity: "high"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	first, err := d.ClaimRealtimeDeliveries(ctx, ch.ID, 10, time.Minute)
	if err != nil || len(first) != 1 {
		t.Fatalf("falha na primeira reivindicação: %v (%d)", err, len(first))
	}
	// Empurra o lease manualmente para o passado, simulando "lease já expirado".
	if _, err := d.Exec(`UPDATE notification_deliveries SET next_attempt_at = now() - interval '1 minute' WHERE id=$1`, first[0].ID); err != nil {
		t.Fatal(err)
	}
	second, err := d.ClaimRealtimeDeliveries(ctx, ch.ID, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 {
		t.Fatalf("a linha sending com lease expirado deveria poder ser reivindicada de novo, obtido %d", len(second))
	}
	if second[0].Attempts != 2 {
		t.Fatalf("a nova reivindicação deveria acumular a contagem de tentativas, obtido %d", second[0].Attempts)
	}
}

func TestClaimSkipsDisabledChannel(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	addTestEvent(t, d, notify.EventFindingCreated, 5001, notify.Snapshot{Severity: "high"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	// Desativar também marca as entregas pendentes existentes como skipped.
	if err := d.SetNotificationChannelEnabled(ctx, ch.ID, false); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := d.QueryRow(`SELECT state FROM notification_deliveries WHERE channel_id=$1`, ch.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != NotifyStateSkipped {
		t.Fatalf("as entregas pendentes existentes do canal desativado deveriam ser marcadas como skipped, obtido %s", state)
	}
	got, err := d.ClaimRealtimeDeliveries(ctx, ch.ID, 10, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("canal desativado não deveria poder ser reivindicado, obtido %d", len(got))
	}
}

func TestDigestBatchDueAndStableBatchID(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeDigest, `{}`)
	for i := 0; i < 3; i++ {
		addTestEvent(t, d, notify.EventFindingCreated, int64(6000+i), notify.Snapshot{Severity: "high"})
	}
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}

	// O lote acabou de ser criado, com idade 0, e não deve estar vencido num ciclo de 30 minutos.
	due, err := d.DigestBatchDue(ctx, ch.ID, 30*time.Minute)
	if err != nil {
		t.Fatalf("falha ao verificar o vencimento do lote: %v", err)
	}
	if due {
		t.Fatal("um lote recém-criado não deveria vencer imediatamente")
	}

	// Envelhece juntas as datas de criação das três entregas, simulando um lote que acumulou um ciclo inteiro.
	if _, err := d.Exec(`UPDATE notification_deliveries SET created_at = now() - interval '40 minutes' WHERE channel_id=$1`, ch.ID); err != nil {
		t.Fatal(err)
	}
	due, err = d.DigestBatchDue(ctx, ch.ID, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !due {
		t.Fatal("um lote além do ciclo deveria ser considerado vencido")
	}

	batch, err := d.ClaimDigestBatch(ctx, ch.ID, MaxDigestBatchSize, time.Minute)
	if err != nil {
		t.Fatalf("falha ao reivindicar o lote de resumo: %v", err)
	}
	if len(batch) != 3 {
		t.Fatalf("o resumo deveria levar as 3 de uma vez, obtido %d", len(batch))
	}
	if batch[0].BatchID == nil {
		t.Fatal("o lote de resumo deve gravar batch_id, senão o histórico não mostra que estas foram enviadas juntas")
	}
	firstBatchID := *batch[0].BatchID
	for _, dl := range batch {
		if dl.BatchID == nil || *dl.BatchID != firstBatchID {
			t.Fatalf("o mesmo lote deveria compartilhar batch_id, obtido %v vs %d", dl.BatchID, firstBatchID)
		}
	}

	// Faz este lote falhar **como um todo**, reagenda e reivindica de novo: batch_id deve manter o valor original (função do COALESCE):
	// senão uma única retentativa apagaria o fato de que "este lote foi enviado junto".
	//
	// É preciso reagendar o lote inteiro e não uma só — é assim que o motor de entrega trata a mensagem de resumo
	// (uma mensagem representa o lote todo, com sucesso e falha compartilhados). Reagendando só uma, as demais continuam dentro do prazo do lease,
	// e a nova reivindicação naturalmente pega apenas aquela.
	allIDs := make([]int64, 0, len(batch))
	for _, dl := range batch {
		allIDs = append(allIDs, dl.ID)
	}
	if err := d.RescheduleDeliveries(ctx, allIDs, time.Second, "falha simulada"); err != nil {
		t.Fatal(err)
	}
	// Empurra o lease para o passado, simulando que o tempo de backoff chegou.
	if _, err := d.Exec(`UPDATE notification_deliveries SET next_attempt_at = now() - interval '1 minute' WHERE channel_id=$1`, ch.ID); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := d.ClaimDigestBatch(ctx, ch.ID, MaxDigestBatchSize, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(reclaimed) != 3 {
		t.Fatalf("a nova reivindicação deveria pegar as 3, obtido %d", len(reclaimed))
	}
	if reclaimed[0].BatchID == nil || *reclaimed[0].BatchID != firstBatchID {
		t.Fatalf("após a retentativa batch_id deveria manter o valor original %d, obtido %v", firstBatchID, reclaimed[0].BatchID)
	}
}

func TestDeliveryStateTransitions(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	addTestEvent(t, d, notify.EventFindingCreated, 7001, notify.Snapshot{Severity: "high"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	got, err := d.ClaimRealtimeDeliveries(ctx, ch.ID, 10, time.Minute)
	if err != nil || len(got) != 1 {
		t.Fatalf("falha na reivindicação: %v (%d)", err, len(got))
	}
	id := got[0].ID

	if err := d.RescheduleDeliveries(ctx, []int64{id}, time.Second, "oscilação de rede"); err != nil {
		t.Fatal(err)
	}
	var state, lastErr string
	if err := d.QueryRow(`SELECT state, last_error FROM notification_deliveries WHERE id=$1`, id).Scan(&state, &lastErr); err != nil {
		t.Fatal(err)
	}
	if state != NotifyStatePending || lastErr != "oscilação de rede" {
		t.Fatalf("após o reagendamento deveria ser pending com o motivo registrado, obtido state=%s err=%q", state, lastErr)
	}

	if err := d.FailDeliveries(ctx, []int64{id}, "tentativas esgotadas"); err != nil {
		t.Fatal(err)
	}
	if err := d.QueryRow(`SELECT state FROM notification_deliveries WHERE id=$1`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != NotifyStateFailed {
		t.Fatalf("deveria ser failed, obtido %s", state)
	}

	// O reenvio manual deve zerar a contagem de tentativas e vencer imediatamente, senão herdaria o orçamento de falhas antigo.
	if err := d.RetryNotificationDelivery(ctx, id); err != nil {
		t.Fatalf("falha no reenvio: %v", err)
	}
	var attempts int
	var next time.Time
	if err := d.QueryRow(`SELECT state, attempts, next_attempt_at FROM notification_deliveries WHERE id=$1`, id).Scan(&state, &attempts, &next); err != nil {
		t.Fatal(err)
	}
	if state != NotifyStatePending || attempts != 0 {
		t.Fatalf("após o reenvio deveria ser pending com attempts=0, obtido state=%s attempts=%d", state, attempts)
	}
	if next.After(time.Now().Add(time.Second)) {
		t.Fatal("o reenvio deveria ser reivindicável imediatamente")
	}

	// Uma entrega já entregue não deve poder ser reenviada.
	if err := d.MarkDeliveriesSent(ctx, []int64{id}); err != nil {
		t.Fatal(err)
	}
	if err := d.RetryNotificationDelivery(ctx, id); err == nil {
		t.Fatal("uma entrega já entregue não deveria permitir reenvio")
	}
}

func TestListNotificationDeliveriesPagingAndFilter(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	for i := 0; i < 5; i++ {
		addTestEvent(t, d, notify.EventFindingCreated, int64(8000+i), notify.Snapshot{Severity: "high", Name: "Teste de paginação"})
	}
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := d.ClaimRealtimeDeliveries(ctx, ch.ID, 10, time.Minute); err != nil {
		t.Fatal(err)
	}

	page1, total, err := d.ListNotificationDeliveries(ctx, NotificationDeliveryFilter{ChannelID: ch.ID, State: NotifyStateSending}, 1, 2)
	if err != nil {
		t.Fatalf("falha na consulta: %v", err)
	}
	if total != 5 {
		t.Fatalf("o total deveria ser 5, obtido %d", total)
	}
	if len(page1) != 2 {
		t.Fatalf("2 por página, obtido %d", len(page1))
	}
	// As mais novas primeiro: o id do primeiro item da primeira página deve ser maior que o da segunda.
	page2, _, err := d.ListNotificationDeliveries(ctx, NotificationDeliveryFilter{ChannelID: ch.ID, State: NotifyStateSending}, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 || page2[0].ID >= page1[0].ID {
		t.Fatalf("a ordem da paginação deveria ter as mais novas primeiro, obtido page1[0]=%d page2[0]=%d", page1[0].ID, page2[0].ID)
	}
	// O contexto de renderização deve voltar junto com o histórico, senão a lista não consegue mostrar "o que foi enviado".
	if page1[0].ChannelName == "" || page1[0].FindingID == 0 {
		t.Fatalf("item do histórico sem campos de exibição: %+v", page1[0])
	}

	// Filtro por status: nenhum pending.
	pending, totalPending, err := d.ListNotificationDeliveries(ctx, NotificationDeliveryFilter{ChannelID: ch.ID, State: NotifyStatePending}, 1, 50)
	if err != nil {
		t.Fatal(err)
	}
	if totalPending != 0 || len(pending) != 0 {
		t.Fatalf("não deveria haver entregas pending, obtido %d (total=%d)", len(pending), totalPending)
	}
}

func TestSetFindingStatusWithNotifyOnlyEmitsOnRealChange(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	tk, err := d.CreateTask("Teste de mudança de status de notificação", "Alvo", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(tk.ID)
	es := d.Exploration(tk.ExplorationID)
	f, err := es.RecordFinding(ctx, RecordFindingInput{
		TaskID: tk.ID, Worker: "test", VulnClass: "Injeção SQL", Name: "Caso de mudança de status",
		Severity: "high", Summary: "Resumo",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_events WHERE finding_id=$1`, f.FindingID) })

	// Ao ser gravada já foi registrado um evento finding_created; conta-o primeiro como linha de base.
	var base int
	if err := d.QueryRow(`SELECT count(*) FROM notification_events WHERE finding_id=$1`, f.FindingID).Scan(&base); err != nil {
		t.Fatal(err)
	}
	if base < 1 {
		t.Fatal("gravar a vulnerabilidade deveria registrar um evento de push na mesma transação")
	}

	// Mudar para o mesmo status: não deve produzir evento (evita ruído de push por reenvio duplicado).
	from, found, notified, err := d.SetFindingStatusWithNotify(ctx, f.FindingID, "pending")
	if err != nil || !found {
		t.Fatalf("falha ao definir o status: found=%v err=%v", found, err)
	}
	if notified {
		t.Fatal("sem mudança de status não deveria registrar evento de push")
	}
	if from != "pending" {
		t.Fatalf("deveria retornar o status anterior pending, obtido %q", from)
	}

	// Mudança real: deve registrar o evento e gravar from/to.
	from, found, notified, err = d.SetFindingStatusWithNotify(ctx, f.FindingID, "fixed")
	if err != nil || !found {
		t.Fatalf("falha ao definir o status: found=%v err=%v", found, err)
	}
	if !notified {
		t.Fatal("quando o status muda de fato, deveria registrar evento de push")
	}
	if from != "pending" {
		t.Fatalf("from deveria ser pending, obtido %q", from)
	}
	var snapshot []byte
	if err := d.QueryRow(`SELECT snapshot FROM notification_events WHERE finding_id=$1 AND kind=$2`,
		f.FindingID, notify.EventFindingStatusChanged).Scan(&snapshot); err != nil {
		t.Fatalf("evento de mudança de status não encontrado: %v", err)
	}
	var snap notify.Snapshot
	if err := json.Unmarshal(snapshot, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.FromStatus != "pending" || snap.ToStatus != "fixed" {
		t.Fatalf("transição de status incorreta no snapshot: %s → %s", snap.FromStatus, snap.ToStatus)
	}
	// O snapshot deve trazer os campos necessários para renderizar, senão a mensagem de mudança de status seria uma casca vazia.
	if snap.VulnClass != "Injeção SQL" || snap.Severity != "high" || snap.Name != "Caso de mudança de status" {
		t.Fatalf("snapshot sem campos de renderização: %+v", snap)
	}
	var status string
	if err := d.QueryRow(`SELECT status FROM findings WHERE id=$1`, f.FindingID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "fixed" {
		t.Fatalf("o status deveria ter sido atualizado para fixed, obtido %s", status)
	}

	// Vulnerabilidade inexistente: found=false, sem erro.
	if _, found, _, err := d.SetFindingStatusWithNotify(ctx, 999999999, "fixed"); err != nil || found {
		t.Fatalf("vulnerabilidade inexistente deveria retornar found=false sem erro, obtido found=%v err=%v", found, err)
	}
}

func TestNotificationStatsSnapshot(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	addTestEvent(t, d, notify.EventFindingCreated, 9001, notify.Snapshot{Severity: "high"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	stats, err := d.NotificationStatsSnapshot(ctx)
	if err != nil {
		t.Fatalf("falha nas estatísticas: %v", err)
	}
	if stats.Channels < 1 || stats.ChannelsOn < 1 {
		t.Fatalf("contagem de canais incorreta: %+v", stats)
	}
	if stats.Pending < 1 {
		t.Fatalf("deveria contabilizar entregas pendentes: %+v", stats)
	}
	// A idade do acúmulo de uma entrega recém-criada deve ser próxima de 0, e não negativa nem um valor enorme.
	if stats.BacklogAgeMS < 0 || stats.BacklogAgeMS > int64(time.Hour/time.Millisecond) {
		t.Fatalf("idade do acúmulo inválida: %d ms", stats.BacklogAgeMS)
	}
	_ = ch
}

func TestNotificationChannelCRUDRoundTrip(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	ch := &NotificationChannel{
		Name:       "Ida e volta de CRUD",
		Kind:       notify.KindEmail,
		Mode:       NotifyModeDigest,
		Config:     json.RawMessage(`{"host":"smtp.example.com","port":587,"from":"a@b.c","to":["x@y.z"]}`),
		Filter:     json.RawMessage(`{"min_severity":"medium","on_status_change":true}`),
		RatePerMin: 42,
	}
	id, err := d.SaveNotificationChannel(ctx, ch)
	if err != nil {
		t.Fatalf("falha ao criar: %v", err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_channels WHERE id=$1`, id) })

	got, err := d.NotificationChannelByID(ctx, id)
	if err != nil {
		t.Fatalf("falha ao ler: %v", err)
	}
	if got.Mode != NotifyModeDigest || got.RatePerMin != 42 || got.Name != "Ida e volta de CRUD" {
		t.Fatalf("campos da ida e volta divergentes: %+v", got)
	}
	if !got.IsEnabled() {
		t.Fatal("o padrão deveria ser habilitado")
	}
	var cfg map[string]any
	if err := json.Unmarshal(got.Config, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["host"] != "smtp.example.com" {
		t.Fatalf("a configuração não foi gravada corretamente: %v", cfg)
	}
	var filter notify.Filter
	if err := json.Unmarshal(got.Filter, &filter); err != nil {
		t.Fatal(err)
	}
	if filter.MinSeverity != "medium" || !filter.OnStatusChange {
		t.Fatalf("as condições de filtro não foram gravadas corretamente: %+v", filter)
	}

	// Depois de atualizar, lê de novo.
	got.Name = "Renomeado"
	off := false
	got.Enabled = &off
	if _, err := d.SaveNotificationChannel(ctx, got); err != nil {
		t.Fatal(err)
	}
	after, err := d.NotificationChannelByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "Renomeado" || after.IsEnabled() {
		t.Fatalf("a atualização não surtiu efeito: %+v", after)
	}

	// Depois de excluir, deve reportar "não existe" e não ter sucesso em silêncio.
	if err := d.DeleteNotificationChannel(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := d.NotificationChannelByID(ctx, id); err != ErrNotificationChannelNotFound {
		t.Fatalf("esperado ErrNotificationChannelNotFound, obtido %v", err)
	}
	if err := d.DeleteNotificationChannel(ctx, id); err != ErrNotificationChannelNotFound {
		t.Fatalf("excluir de novo deveria reportar inexistente, obtido %v", err)
	}
}

// TestSaveNotificationChannelKeepsExplicitZeroRate trava um ponto que já foi escrito errado:
// **0 é uma configuração válida, com o significado de "sem limite de taxa", e não pode ser tratado pela camada db como "não especificado" e sobrescrito com o padrão**.
//
// Bug histórico: SaveNotificationChannel tinha `if RatePerMin <= 0 { usar padrão }`,
// e assim a documentação, as dicas da UI e takeTokens interpretavam "0 = sem limite", e só a camada de gravação no banco mudava silenciosamente para
// 20 (DingTalk/WeCom/Telegram) ou 100 (Feishu) — o operador achava que tinha liberado o limite, mas ficava preso,
// sem nenhum aviso. A diferença entre "não especificado" e "0 explícito" só o corpo da requisição consegue expressar,
// então o valor padrão é preenchido na camada server (ver notifyCreateChannel), e a camada db só armazena.
func TestSaveNotificationChannelKeepsExplicitZeroRate(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	// 0 explícito (sem limite de taxa): deve ser gravado exatamente como está.
	unlimited := &NotificationChannel{
		Name: "Sem limite de taxa", Kind: notify.KindDingTalk, RatePerMin: 0,
		Config: json.RawMessage(`{"webhook":"https://example.com/h"}`),
	}
	id, err := d.SaveNotificationChannel(ctx, unlimited)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_channels WHERE id=$1`, id) })
	got, err := d.NotificationChannelByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.RatePerMin != 0 {
		t.Fatalf("0 explícito significa sem limite de taxa e deve ser salvo como está, obtido %d", got.RatePerMin)
	}
	if got.Mode != NotifyModeRealtime {
		t.Fatalf("o modo padrão deveria ser realtime, obtido %s", got.Mode)
	}

	// Valor negativo é entrada inválida e deve ser rejeitado, e não mudado em silêncio para outro valor.
	bad := &NotificationChannel{
		Name: "Limite de taxa negativo", Kind: notify.KindDingTalk, RatePerMin: -1,
		Config: json.RawMessage(`{"webhook":"https://example.com/h"}`),
	}
	if _, err := d.SaveNotificationChannel(ctx, bad); err == nil {
		t.Fatal("limite de taxa negativo deveria ser rejeitado")
	}
}

// TestDeleteChannelCascadesDeliveries trava o comportamento da chave estrangeira: ao excluir o canal, o histórico de entregas dele some junto
// (sem a configuração, o histórico não tem como ser interpretado), mas o evento em si deve ficar — pode ainda ser referenciado por outros canais.
func TestDeleteChannelCascadesDeliveries(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeRealtime, `{}`)
	ev := addTestEvent(t, d, notify.EventFindingCreated, 9101, notify.Snapshot{Severity: "high"})
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := d.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1`, ch.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before == 0 {
		t.Fatal("pré-condição não satisfeita: nenhuma entrega foi produzida")
	}
	if err := d.DeleteNotificationChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	var after int
	if err := d.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1`, ch.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != 0 {
		t.Fatalf("após excluir o canal, suas entregas deveriam ser excluídas em cascata, ainda restam %d", after)
	}
	var evExists bool
	if err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM notification_events WHERE id=$1)`, ev).Scan(&evExists); err != nil {
		t.Fatal(err)
	}
	if !evExists {
		t.Fatal("excluir o canal não deveria excluir o próprio evento")
	}
}

// TestClaimDigestBatchHonorsCallerLimit cobre uma brecha apontada pela auditoria:
// o canal de resumo antes contornava totalmente o token bucket — allow era descontado por takeTokens mas ninguém o usava,
// e rate_per_min não tinha efeito no modo digest. Agora limit também participa da restrição.
func TestClaimDigestBatchHonorsCallerLimit(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()
	ch := newTestChannel(t, d, notify.KindDingTalk, NotifyModeDigest, `{}`)
	for i := 0; i < 10; i++ {
		addTestEvent(t, d, notify.EventFindingCreated, int64(7000+i), notify.Snapshot{Severity: "high"})
	}
	if _, _, err := d.FanOutPendingEvents(ctx, 100); err != nil {
		t.Fatal(err)
	}
	// Com limit=3: só consegue reivindicar 3, o resto fica no banco.
	got, err := d.ClaimDigestBatch(ctx, ch.ID, 3, time.Minute)
	if err != nil {
		t.Fatalf("falha na reivindicação: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("deveria reivindicar só 3 conforme a cota de limite do chamador, obtido %d", len(got))
	}
	// limit=0 significa cota da rodada esgotada: não deve reivindicar nenhuma nem reportar erro.
	if got, err := d.ClaimDigestBatch(ctx, ch.ID, 0, time.Minute); err != nil || len(got) != 0 {
		t.Fatalf("com cota 0 deveria reivindicar 0 sem erro, obtido %d err=%v", len(got), err)
	}
}

// TestFinishFindingRetestEmitsStatusChange cobre uma lacuna de integridade apontada pela auditoria:
// quando a conclusão do reteste é "corrigido", o status de fato muda, mas aquele UPDATE gravava direto no banco,
// contornando a versão com notificação — então os canais configurados com on_status_change não recebiam
// nenhum push dessa transição de status, o status mudava em silêncio na interface, e a operação só descobria ao abrir a plataforma.
//
// Este caso trava "todos os caminhos que mudam o status devem registrar o evento de mudança de status".
func TestFinishFindingRetestEmitsStatusChange(t *testing.T) {
	d := notifyTestDB(t)
	ctx := context.Background()

	tk, err := d.CreateTask("Teste de push de reteste", "Alvo", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(tk.ID)
	es := d.Exploration(tk.ExplorationID)
	f, err := es.RecordFinding(ctx, RecordFindingInput{
		TaskID: tk.ID, Worker: "test", VulnClass: "Injeção SQL", Name: "Alvo do reteste",
		Severity: "high", Summary: "Resumo",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Exec(`DELETE FROM notification_events WHERE finding_id=$1`, f.FindingID) })

	// Cria um registro de reteste e o leva direto ao estado concluído.
	rt, _, _, err := d.CreateFindingRetest(ctx, f.FindingID, "Revisão")
	if err != nil {
		t.Fatal(err)
	}
	if rt.ConversationID == nil {
		t.Fatal("o reteste deveria estar associado a uma sessão")
	}
	// O reteste precisa entrar em running antes de poder gravar a conclusão (igual ao fluxo real).
	if ok, err := d.StartFindingRetest(ctx, rt.ID); err != nil || !ok {
		t.Fatalf("falha ao iniciar o reteste: ok=%v err=%v", ok, err)
	}
	if err := d.RecordFindingRetestResult(ctx, *rt.ConversationID, "fixed", "Corrigido", "Evidência"); err != nil {
		t.Fatal(err)
	}
	if err := d.FinishFindingRetest(rt.ID, "completed", ""); err != nil {
		t.Fatalf("falha ao encerrar o reteste: %v", err)
	}

	var status string
	if err := d.QueryRow(`SELECT status FROM findings WHERE id=$1`, f.FindingID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != FindingFixed {
		t.Fatalf("após o reteste julgar corrigido, o status deveria ser fixed, obtido %s", status)
	}

	// Asserção-chave: deve haver um evento de mudança de status, com from/to corretos.
	var snapshot []byte
	err = d.QueryRow(`SELECT snapshot FROM notification_events WHERE finding_id=$1 AND kind=$2 ORDER BY id DESC LIMIT 1`,
		f.FindingID, notify.EventFindingStatusChanged).Scan(&snapshot)
	if err != nil {
		t.Fatalf("o reteste julgar corrigido deveria registrar evento de push de mudança de status (senão os canais configurados com on_status_change não recebem): %v", err)
	}
	var snap notify.Snapshot
	if err := json.Unmarshal(snapshot, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.FromStatus != "pending" || snap.ToStatus != FindingFixed {
		t.Fatalf("transição de status incorreta no snapshot: %s → %s", snap.FromStatus, snap.ToStatus)
	}
	// O snapshot deve trazer os campos necessários para renderizar, senão o push sai como uma casca vazia.
	if snap.Name != "Alvo do reteste" || snap.Severity != "high" {
		t.Fatalf("snapshot sem campos de renderização: %+v", snap)
	}
}
