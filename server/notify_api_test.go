package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/notify"
)

// Este arquivo cobre o comportamento ponta a ponta da funcionalidade de notificações: vulnerabilidade gravada no banco → evento → despacho → HTTP real enviado.
//
// Um ponto de segurança: estes casos **não chamam o Notifier.step() global**, só chamam stepRealtime/stepDigest
// para os canais que eles mesmos criaram. O motivo é que step() percorre todos os canais habilitados no banco —
// rodando os testes num banco de desenvolvimento com robôs reais de DingTalk/WeCom configurados, o step global enviaria de verdade
// para esses grupos as vulnerabilidades geradas durante o teste. Chamar canal a canal limita o impacto estritamente aos receptores falsos criados pelo teste.
//
// Limpeza: ao fim do caso apaga os eventos gerados por ele (entregas caem em cascata) e o canal, sem deixar backlog em canais reais.
//
// Critério das asserções: stepRealtime/stepDigest não retornam valor e registram log internamente, então aqui se afirma
// o **comportamento externo observável** (o que o receptor falso recebeu, em que estado as linhas de entrega ficaram), e não o
// valor de retorno da função — isso está mais próximo do caminho de chamada real do que fazer stub do retorno.

// notifyFixture é o fixture comum dos casos deste arquivo.
type notifyFixture struct {
	s       *Server
	pg      *db.DB
	request func(method, path, body string) *httptest.ResponseRecorder
	n       *Notifier
	// task/exploration criados por conta própria: o caso registra a vulnerabilidade aqui, isolando os dados dos demais casos.
	taskID int64
	expID  int64
	// eventos gerados depois de cleanupMark são apagados juntos na limpeza.
	cleanupMark int64
}

func newNotifyFixture(t *testing.T) *notifyFixture {
	t.Helper()
	// Todos os receptores falsos deste arquivo rodam em 127.0.0.1, e a entrega por padrão recusa endereços de loopback
	// (evita SSRF contra serviços da mesma máquina e metadados de nuvem). O teste liga explicitamente este interruptor;
	// o comportamento de "recusar por padrão" da proteção é coberto pelo ssrf_test.go do pacote notify.
	t.Setenv(notify.AllowLocalTargetsEnv, "1")
	s, _, request := trafficEvidenceServer(t)
	pg := s.m.pg

	// Cria uma task própria: a task criada pelo fixture compartilhado trafficEvidenceServer não obtém exploration id,
	// e registrar vulnerabilidade exige um.
	task, err := s.m.CreateTask("Teste de notificação", "Verificação do comportamento de notificação", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := strconv.ParseInt(task.ID, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pg.Exec(`DELETE FROM tasks WHERE id=$1`, taskID) })

	var mark int64
	if err := pg.QueryRow(`SELECT COALESCE(max(id),0) FROM notification_events`).Scan(&mark); err != nil {
		t.Fatal(err)
	}
	// Faz o fixture se fechar sobre si mesmo: marca de uma vez como despachados os eventos que já existiam antes de criar o fixture.
	//
	// Por que é preciso: FanOutPendingEvents é **global**, expande todos os eventos não despachados do banco
	// em todos os canais correspondentes. E o fixture compartilhado trafficEvidenceServer registra por conta própria uma vulnerabilidade
	// (justamente o finding inicial que ele retorna), e outros casos também podem ter deixado resíduos. Sem isolamento,
	// esses eventos avulsos seriam despachados para os canais deste caso, fazendo asserções do tipo "deveria haver N entregas"
	// acertarem às vezes e errarem outras — e o erro depende da ordem de execução dos casos, mais difícil de investigar que uma falha direta.
	if _, err := pg.Exec(`UPDATE notification_events SET fanned_out = true WHERE id <= $1 AND NOT fanned_out`, mark); err != nil {
		t.Fatal(err)
	}

	f := &notifyFixture{s: s, pg: pg, request: request, n: newNotifier(s), taskID: taskID, expID: task.ExpID, cleanupMark: mark}
	t.Cleanup(func() {
		if _, err := pg.Exec(`DELETE FROM notification_events WHERE id > $1`, f.cleanupMark); err != nil {
			t.Logf("falha ao limpar eventos de notificação: %v", err)
		}
	})
	// O interruptor geral precisa estar ligado (outros casos podem tê-lo desligado).
	if err := pg.SetBool(settingNotifyEnabled, true); err != nil {
		t.Fatal(err)
	}
	return f
}

// record grava uma vulnerabilidade pelo caminho real de escrita de evidência e retorna o finding id.
// Esse caminho registra o evento de notificação na **mesma transação** — exatamente o ponto de encaixe desta funcionalidade.
func (f *notifyFixture) record(t *testing.T, vulnclass, severity string) int64 {
	t.Helper()
	out, err := f.s.evidenceStore().Record(context.Background(), db.RecordFindingInput{
		TaskID:        f.taskID,
		ExplorationID: f.expID,
		Worker:        "test",
		VulnClass:     vulnclass,
		Name:          vulnclass,
		Severity:      severity,
		Summary:       vulnclass + " 的摘要",
		Evidence:      "poc",
	}, nil)
	if err != nil {
		t.Fatalf("falha ao registrar vulnerabilidade: %v", err)
	}
	return out.FindingID
}

// channel relê a configuração do canal (para chamar stepX canal a canal).
func (f *notifyFixture) channel(t *testing.T, id int64) *db.NotificationChannel {
	t.Helper()
	ch, err := f.pg.NotificationChannelByID(context.Background(), id)
	if err != nil {
		t.Fatalf("falha ao ler canal: %v", err)
	}
	return ch
}

// deliver despacha eventos e roda uma rodada de entrega só para o canal indicado.
func (f *notifyFixture) deliver(t *testing.T, chID int64, baseURL string) {
	t.Helper()
	ctx := context.Background()
	if _, _, err := f.pg.FanOutPendingEvents(ctx, 500); err != nil {
		t.Fatalf("falha ao despachar: %v", err)
	}
	f.n.stepRealtime(ctx, f.channel(t, chID), 50, baseURL)
}

// createChannel cria o canal pela interface HTTP, cobrindo de quebra o caminho de validação da própria interface.
func (f *notifyFixture) createChannel(t *testing.T, payload map[string]any) int64 {
	t.Helper()
	raw, _ := json.Marshal(payload)
	r := f.request("POST", "/api/notify/channels", string(raw))
	if r.Code != 200 {
		t.Fatalf("falha ao criar canal %d: %s", r.Code, r.Body)
	}
	var res struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &res); err != nil || res.ID == 0 {
		t.Fatalf("resposta anômala ao criar canal: %s (%v)", r.Body, err)
	}
	t.Cleanup(func() { f.pg.Exec(`DELETE FROM notification_channels WHERE id=$1`, res.ID) })
	return res.ID
}

// fakeWebhook é um receptor falso que registra os corpos de requisição recebidos.
type fakeWebhook struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []map[string]any
}

func newFakeWebhook(t *testing.T) *fakeWebhook {
	t.Helper()
	f := &fakeWebhook{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.bodies = append(f.bodies, body)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"errcode":0,"errmsg":"ok"}`)
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeWebhook) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.bodies)
}

func (f *fakeWebhook) body(t *testing.T, i int) map[string]any {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.bodies) {
		t.Fatalf("o receptor falso recebeu só %d requisições, não há a %d-ésima", len(f.bodies), i)
	}
	return f.bodies[i]
}

func (f *fakeWebhook) last(t *testing.T) map[string]any {
	t.Helper()
	if f.count() == 0 {
		t.Fatal("o receptor falso não recebeu nenhuma requisição")
	}
	return f.body(t, f.count()-1)
}

// markdownText extrai o texto do corpo da requisição, compatível com as diferenças de nome de campo de cada fornecedor:
// o markdown do DingTalk usa `text`, o ActionCard usa `text`, o markdown do WeCom usa `content`.
func markdownText(t *testing.T, body map[string]any) string {
	t.Helper()
	for _, key := range []string{"markdown", "actionCard"} {
		section, ok := body[key].(map[string]any)
		if !ok {
			continue
		}
		for _, field := range []string{"text", "content"} {
			if s, ok := section[field].(string); ok && s != "" {
				return s
			}
		}
	}
	t.Fatalf("não há texto reconhecível no corpo da requisição: %v", body)
	return ""
}

// agePendingBatch envelhece as entregas pendentes do canal, usado para testar o vencimento do lote de resumo.
func (f *notifyFixture) agePendingBatch(t *testing.T, chID int64) {
	t.Helper()
	if _, err := f.pg.Exec(`UPDATE notification_deliveries SET created_at = now() - interval '2 hours'
WHERE channel_id=$1 AND state=$2`, chID, db.NotifyStatePending); err != nil {
		t.Fatal(err)
	}
}

func TestNotifyEndToEndRealtimeDelivery(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "Notificação em tempo real",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
	})
	f.record(t, "Injeção de SQL", "high")
	f.deliver(t, chID, "")

	if hook.count() != 1 {
		t.Fatalf("deveria enviar 1 mensagem, foram %d", hook.count())
	}
	text := markdownText(t, hook.last(t))
	for _, want := range []string{"Injeção de SQL", "高危", "摘要"} {
		if !strings.Contains(text, want) {
			t.Fatalf("o corpo da mensagem não contém %q:\n%s", want, text)
		}
	}
	// A entrega deve passar para sent.
	var pending int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1 AND state <> $2`,
		chID, db.NotifyStateSent).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 0 {
		t.Fatalf("após a entrega ainda há %d não marcadas como sent", pending)
	}
}

func TestNotifyChannelAPIMasksSecretsAndPreservesOnUpdate(t *testing.T) {
	f := newNotifyFixture(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "Caso de máscara",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": "https://oapi.dingtalk.com/robot/send?access_token=abc123456", "secret": "SECabcdef123456"},
	})

	r := f.request("GET", "/api/notify/channels", "")
	if r.Code != 200 {
		t.Fatalf("falha ao listar canais %d: %s", r.Code, r.Body)
	}
	if strings.Contains(r.Body.String(), "abc123456") || strings.Contains(r.Body.String(), "SECabcdef123456") {
		t.Fatalf("o eco da interface vazou credenciais: %s", r.Body)
	}
	var listed struct {
		Channels []struct {
			ID         int64          `json:"id"`
			Config     map[string]any `json:"config"`
			SecretKeys []string       `json:"secret_keys"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	var mine *struct {
		ID         int64          `json:"id"`
		Config     map[string]any `json:"config"`
		SecretKeys []string       `json:"secret_keys"`
	}
	for i := range listed.Channels {
		if listed.Channels[i].ID == chID {
			mine = &listed.Channels[i]
		}
	}
	if mine == nil {
		t.Fatal("o canal recém-criado não aparece na lista")
	}
	if !notify.IsMasked(fmt.Sprint(mine.Config["webhook"])) || !notify.IsMasked(fmt.Sprint(mine.Config["secret"])) {
		t.Fatalf("o campo de credencial deveria ser um valor mascarado: %v", mine.Config)
	}
	if len(mine.SecretKeys) == 0 {
		t.Fatal("a interface deveria informar ao frontend quais campos são credenciais")
	}

	// PATCH só renomeia + devolve a credencial mascarada: a credencial real deve ser preservada tal qual.
	body, _ := json.Marshal(map[string]any{
		"name":   "renomeado",
		"config": map[string]any{"webhook": fmt.Sprint(mine.Config["webhook"]), "secret": fmt.Sprint(mine.Config["secret"])},
	})
	if r := f.request("PATCH", fmt.Sprintf("/api/notify/channels/%d", chID), string(body)); r.Code != 200 {
		t.Fatalf("falha ao atualizar %d: %s", r.Code, r.Body)
	}
	cfg := f.channelConfig(t, chID)
	if cfg["webhook"] != "https://oapi.dingtalk.com/robot/send?access_token=abc123456" {
		t.Fatalf("a devolução mascarada sobrescreveu a credencial real: %v", cfg["webhook"])
	}
	if cfg["secret"] != "SECabcdef123456" {
		t.Fatalf("a devolução mascarada sobrescreveu o secret: %v", cfg["secret"])
	}
	if f.channel(t, chID).Name != "renomeado" {
		t.Fatal("o nome não foi atualizado")
	}

	// Limpar o secret explicitamente deve valer (diferente de "devolver máscara = manter inalterado").
	body, _ = json.Marshal(map[string]any{"config": map[string]any{"secret": ""}})
	if r := f.request("PATCH", fmt.Sprintf("/api/notify/channels/%d", chID), string(body)); r.Code != 200 {
		t.Fatalf("falha ao limpar o secret %d: %s", r.Code, r.Body)
	}
	if _, still := f.channelConfig(t, chID)["secret"]; still {
		t.Fatal("string vazia deveria limpar o secret")
	}
}

func (f *notifyFixture) channelConfig(t *testing.T, id int64) map[string]any {
	t.Helper()
	var cfg map[string]any
	if err := json.Unmarshal(f.channel(t, id).Config, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestNotifyChannelAPICreateValidation(t *testing.T) {
	f := newNotifyFixture(t)
	cases := []struct {
		name    string
		payload map[string]any
		wantSub string
	}{
		{"tipo inválido", map[string]any{"name": "x", "kind": "nope", "config": map[string]any{}}, "tipo de canal inválido"},
		{"sem nome", map[string]any{"kind": notify.KindDingTalk, "config": map[string]any{"webhook": "https://e.com/h"}}, "falta o nome do canal"},
		{"sem webhook", map[string]any{"name": "x", "kind": notify.KindDingTalk, "config": map[string]any{}}, "Webhook"},
		{"protocolo de webhook inválido", map[string]any{"name": "x", "kind": notify.KindDingTalk, "config": map[string]any{"webhook": "file:///etc/passwd"}}, "Webhook 地址无效"},
		{"modo inválido", map[string]any{"name": "x", "kind": notify.KindDingTalk, "mode": "sometimes", "config": map[string]any{"webhook": "https://e.com/h"}}, "modo de notificação inválido"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := json.Marshal(tc.payload)
			r := f.request("POST", "/api/notify/channels", string(raw))
			if r.Code != 400 {
				t.Fatalf("deveria retornar 400, obtido %d: %s", r.Code, r.Body)
			}
			if !strings.Contains(r.Body.String(), tc.wantSub) {
				t.Fatalf("a mensagem de erro deveria mencionar %q, obtido %s", tc.wantSub, r.Body)
			}
		})
	}
	if r := f.request("DELETE", "/api/notify/channels/99999999", ""); r.Code != 404 {
		t.Fatalf("excluir canal inexistente deveria dar 404, obtido %d", r.Code)
	}
}

func TestNotifyFilterBlocksBelowThreshold(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "Somente crítico",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
		"filter": map[string]any{"min_severity": "critical"},
	})
	f.record(t, "Problema de baixa gravidade", "low")
	if _, _, err := f.pg.FanOutPendingEvents(context.Background(), 500); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1`, chID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("vulnerabilidade abaixo do limiar não deveria gerar entrega, obtido %d", n)
	}
	f.n.stepRealtime(context.Background(), f.channel(t, chID), 50, "")
	if hook.count() != 0 {
		t.Fatal("vulnerabilidade filtrada não deveria enviar mensagem")
	}
}

func TestNotifyDigestBatchesMultipleFindingsIntoOneMessage(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "Notificação em resumo",
		"kind":   notify.KindDingTalk,
		"mode":   db.NotifyModeDigest,
		"config": map[string]any{"webhook": hook.URL},
	})
	for i := 0; i < 3; i++ {
		f.record(t, fmt.Sprintf("Vulnerabilidade do resumo %d", i+1), "high")
	}
	ctx := context.Background()
	if _, _, err := f.pg.FanOutPendingEvents(ctx, 500); err != nil {
		t.Fatal(err)
	}
	ch := f.channel(t, chID)

	// Não vencido: não envia.
	f.n.stepDigest(ctx, ch, 50, "")
	if hook.count() != 0 {
		t.Fatal("o lote de resumo foi enviado antes de vencer")
	}

	// Depois de envelhecer o lote: as três viram uma única mensagem.
	f.agePendingBatch(t, chID)
	f.n.stepDigest(ctx, ch, 50, "")
	if got := hook.count(); got != 1 {
		t.Fatalf("as três deveriam virar uma única mensagem, foram enviadas %d", got)
	}
	text := markdownText(t, hook.last(t))
	if !strings.Contains(text, "近") || !strings.Contains(text, "3 个漏洞") {
		t.Fatalf("a mensagem de resumo não contém o texto de quantidade/janela de tempo:\n%s", text)
	}
	for i := 1; i <= 3; i++ {
		if !strings.Contains(text, fmt.Sprintf("Vulnerabilidade do resumo %d", i)) {
			t.Fatalf("a mensagem de resumo não contém o item %d:\n%s", i, text)
		}
	}
	// O mesmo lote deve compartilhar o batch_id.
	var distinct, total int
	if err := f.pg.QueryRow(`SELECT count(DISTINCT batch_id), count(*) FROM notification_deliveries WHERE channel_id=$1`, chID).Scan(&distinct, &total); err != nil {
		t.Fatal(err)
	}
	if total != 3 || distinct != 1 {
		t.Fatalf("as três entregas deveriam compartilhar um batch_id, obtido distinct=%d total=%d", distinct, total)
	}
}

func TestNotifyDisabledChannelDoesNotSend(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":    "Canal desativado",
		"kind":    notify.KindDingTalk,
		"enabled": false,
		"config":  map[string]any{"webhook": hook.URL},
	})
	f.record(t, "Vulnerabilidade durante a desativação", "critical")
	if _, _, err := f.pg.FanOutPendingEvents(context.Background(), 500); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1`, chID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("canal desativado não deveria gerar entrega, obtido %d", n)
	}
}

func TestNotifyStatusChangeDelivery(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "Assinatura de mudança de estado",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
		"filter": map[string]any{"on_status_change": true},
	})
	finding := f.record(t, "Caso de mudança de estado", "high")
	r := f.request("PATCH", fmt.Sprintf("/api/exploration/findings/%d", finding), `{"status":"fixed"}`)
	if r.Code != 200 {
		t.Fatalf("falha ao mudar o estado %d: %s", r.Code, r.Body)
	}
	f.deliver(t, chID, "")

	// Deveria haver duas: a do fixed é a mudança de estado; a de finding_created também pode sair na mesma rodada.
	// A mudança de estado é criada de fato mais tarde, mas sem depender da ordem, procura-se em todas.
	found := false
	for i := 0; i < hook.count(); i++ {
		text := markdownText(t, hook.body(t, i))
		if strings.Contains(text, "状态变更") && strings.Contains(text, "已修复") {
			found = true
		}
	}
	if !found {
		t.Fatalf("não foi recebida mensagem contendo \"状态变更 → 已修复\" (%d no total)", hook.count())
	}
}

func TestNotifyStatusChangeSuppressedByDefault(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "Sem assinatura de mudança de estado",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
	})
	finding := f.record(t, "Mudança sem assinatura", "high")
	if r := f.request("PATCH", fmt.Sprintf("/api/exploration/findings/%d", finding), `{"status":"false_positive"}`); r.Code != 200 {
		t.Fatalf("falha ao mudar o estado %d: %s", r.Code, r.Body)
	}
	if _, _, err := f.pg.FanOutPendingEvents(context.Background(), 500); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries d
JOIN notification_events e ON e.id = d.event_id
WHERE d.channel_id=$1 AND e.kind=$2`, chID, notify.EventFindingStatusChanged).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("canal sem assinatura de mudança de estado não deveria receber entrega de mudança de estado, obtido %d", n)
	}
}

func TestNotifyTestMessageEndpoint(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "Envio de teste",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
	})
	if r := f.request("POST", fmt.Sprintf("/api/notify/channels/%d/test", chID), ""); r.Code != 200 {
		t.Fatalf("falha no envio de teste %d: %s", r.Code, r.Body)
	}
	if hook.count() != 1 {
		t.Fatalf("o receptor falso deveria receber 1 mensagem de teste, obtido %d", hook.count())
	}
	// A mensagem de teste precisa ser reconhecível como teste à primeira vista, não pode ser confundida com uma vulnerabilidade real.
	if text := markdownText(t, hook.last(t)); !strings.Contains(text, "teste") {
		t.Fatalf("a mensagem de teste deveria indicar que é um teste: %s", text)
	}
	// Com a configuração quebrada, o erro original do canal deve ser devolvido ao usuário tal qual.
	badID := f.createChannel(t, map[string]any{
		"name":   "Endereço ruim",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": "http://127.0.0.1:1/hook"},
	})
	if r := f.request("POST", fmt.Sprintf("/api/notify/channels/%d/test", badID), ""); r.Code != 502 {
		t.Fatalf("falha na entrega deveria responder 502, obtido %d: %s", r.Code, r.Body)
	}
}

func TestNotifyDeliveriesHistoryAndRetry(t *testing.T) {
	f := newNotifyFixture(t)
	// Aponta para um endereço que necessariamente falha, produzindo entregas failed.
	chID := f.createChannel(t, map[string]any{
		"name":   "Falha e retry",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": "http://127.0.0.1:1/hook"},
	})
	f.record(t, "Notificação que vai falhar", "high")
	ctx := context.Background()
	if _, _, err := f.pg.FanOutPendingEvents(ctx, 500); err != nil {
		t.Fatal(err)
	}
	ch := f.channel(t, chID)
	// Entrega repetidamente até esgotar o orçamento de retry.
	for i := 0; i < db.MaxNotifyAttempts; i++ {
		f.n.stepRealtime(ctx, ch, 50, "")
		if _, err := f.pg.Exec(`UPDATE notification_deliveries SET next_attempt_at = now() - interval '1 minute' WHERE channel_id=$1`, chID); err != nil {
			t.Fatal(err)
		}
	}
	var state string
	if err := f.pg.QueryRow(`SELECT state FROM notification_deliveries WHERE channel_id=$1`, chID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != db.NotifyStateFailed {
		t.Fatalf("após esgotar os retries deveria ser failed, obtido %s", state)
	}

	r := f.request("GET", fmt.Sprintf("/api/notify/deliveries?channel_id=%d&state=failed", chID), "")
	if r.Code != 200 {
		t.Fatalf("falha ao consultar o histórico %d: %s", r.Code, r.Body)
	}
	var hist struct {
		Deliveries []struct {
			ID        int64  `json:"id"`
			State     string `json:"state"`
			LastError string `json:"last_error"`
			Attempts  int    `json:"attempts"`
			Title     string `json:"title"`
		} `json:"deliveries"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &hist); err != nil {
		t.Fatal(err)
	}
	if hist.Total != 1 || len(hist.Deliveries) != 1 {
		t.Fatalf("deveria achar 1 entrega com falha, obtido total=%d len=%d", hist.Total, len(hist.Deliveries))
	}
	if hist.Deliveries[0].LastError == "" {
		t.Fatal("o histórico deveria trazer o motivo da falha, senão o usuário não consegue investigar")
	}
	if hist.Deliveries[0].Attempts < db.MaxNotifyAttempts {
		t.Fatalf("o nº de tentativas deveria ser registrado, obtido %d", hist.Deliveries[0].Attempts)
	}
	if hist.Deliveries[0].Title != "Notificação que vai falhar" {
		t.Fatalf("o histórico deveria trazer o título da vulnerabilidade, obtido %q", hist.Deliveries[0].Title)
	}

	// Reenvio manual: deve voltar a pending e zerar a contagem.
	if r := f.request("POST", fmt.Sprintf("/api/notify/deliveries/%d/retry", hist.Deliveries[0].ID), ""); r.Code != 200 {
		t.Fatalf("falha no reenvio %d: %s", r.Code, r.Body)
	}
	var attempts int
	if err := f.pg.QueryRow(`SELECT state, attempts FROM notification_deliveries WHERE id=$1`, hist.Deliveries[0].ID).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != db.NotifyStatePending || attempts != 0 {
		t.Fatalf("após o reenvio deveria ser pending com attempts=0, obtido %s/%d", state, attempts)
	}
}

func TestNotifyMetaAndSettingsRoundTrip(t *testing.T) {
	f := newNotifyFixture(t)
	r := f.request("GET", "/api/notify/meta", "")
	if r.Code != 200 {
		t.Fatalf("meta falhou: %s", r.Body)
	}
	var meta struct {
		Kinds []struct {
			Kind       string   `json:"kind"`
			SecretKeys []string `json:"secret_keys"`
		} `json:"kinds"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if len(meta.Kinds) != len(notify.Kinds()) {
		t.Fatalf("meta deveria listar todos os %d canais, obtido %d", len(notify.Kinds()), len(meta.Kinds))
	}
	for _, k := range meta.Kinds {
		if len(k.SecretKeys) == 0 {
			t.Errorf("o canal %s não informou campos de credencial", k.Kind)
		}
	}

	// Ida e volta das três configurações globais. A barra final deve ser normalizada, senão o link de volta montaria "//function/...".
	if r := f.request("PUT", "/api/settings", `{"notify_public_base_url":"https://artex.example.com/","notify_digest_interval_min":15,"notify_enabled":true}`); r.Code != 200 {
		t.Fatalf("falha ao gravar a configuração %d: %s", r.Code, r.Body)
	}
	t.Cleanup(func() {
		f.pg.Exec(`DELETE FROM settings WHERE key IN ($1,$2)`, settingNotifyPublicBaseURL, settingNotifyDigestMinutes)
	})
	payload := f.s.settingsPayload()
	if payload["notify_public_base_url"] != "https://artex.example.com" {
		t.Fatalf("o endereço do link de volta não foi normalizado: %v", payload["notify_public_base_url"])
	}
	if payload["notify_digest_interval_min"] != 15 {
		t.Fatalf("o período de resumo não entrou em vigor: %v", payload["notify_digest_interval_min"])
	}

	// Valores inválidos devem ser rejeitados.
	for _, body := range []string{
		`{"notify_public_base_url":"ftp://x"}`,
		`{"notify_digest_interval_min":0}`,
		`{"notify_digest_interval_min":99999}`,
	} {
		if r := f.request("PUT", "/api/settings", body); r.Code != 400 {
			t.Errorf("%s deveria retornar 400, obtido %d", body, r.Code)
		}
	}
}

// TestNotifyDeepLinkUsesPublicBaseURL cobre a montagem do link de volta: com public_base_url configurado,
// a mensagem individual deve usar um ActionCard com botão, e o link deve apontar para a página de detalhes da vulnerabilidade.
func TestNotifyDeepLinkUsesPublicBaseURL(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "Link de volta",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
	})
	finding := f.record(t, "Vulnerabilidade com link de volta", "high")
	f.deliver(t, chID, "https://artex.example.com")

	body := hook.last(t)
	card, _ := body["actionCard"].(map[string]any)
	if card == nil {
		t.Fatalf("com link de volta deveria usar ActionCard, obtido msgtype=%v", body["msgtype"])
	}
	want := fmt.Sprintf("https://artex.example.com/function/findings/detail?id=%d", finding)
	if card["singleURL"] != want {
		t.Fatalf("link de volta incorreto\nesperado %s\nobtido %v", want, card["singleURL"])
	}
}

// TestNotifyNoDeepLinkWithoutBaseURL cobre o inverso: sem endereço externo configurado não deve gerar link quebrado
// (por exemplo apontando para localhost ou caminho relativo), deve voltar a markdown puro.
func TestNotifyNoDeepLinkWithoutBaseURL(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	chID := f.createChannel(t, map[string]any{
		"name":   "Sem link de volta",
		"kind":   notify.KindDingTalk,
		"config": map[string]any{"webhook": hook.URL},
	})
	f.record(t, "Vulnerabilidade sem link de volta", "high")
	f.deliver(t, chID, "")

	body := hook.last(t)
	if body["msgtype"] != "markdown" {
		t.Fatalf("sem endereço externo configurado deveria enviar markdown, obtido %v", body["msgtype"])
	}
	if text := markdownText(t, body); strings.Contains(text, "Ver detalhes") {
		t.Fatalf("sem endereço externo configurado não deveria aparecer o link de detalhes:\n%s", text)
	}
}

// TestNotifyDigestSegmentsAndDefersRemainder é a evidência ponta a ponta da correção de "perda silenciosa".
//
// A mensagem de resumo é limitada pelo tamanho máximo do canal (WeCom 4096 bytes); quando um lote não cabe, deve ser cortado **por item inteiro**:
// os itens que couberam nesta mensagem são marcados como entregues, o resto volta à fila para a próxima. A implementação antiga marcava o lote
// todo como sucesso — os cortados não estavam na mensagem nem na lista de falhas, o histórico de entregas
// ainda mostrava sucesso, e a vulnerabilidade simplesmente sumia.
//
// Afirma quatro coisas: ① só se marcou o nº de itens que realmente couberam ② o resto continua pendente ③ os itens adiados
// **não consumiram tentativas de retry** ④ mais uma rodada consegue enviar o que sobrou (não trava).
func TestNotifyDigestSegmentsAndDefersRemainder(t *testing.T) {
	f := newNotifyFixture(t)
	hook := newFakeWebhook(t)
	// Usa WeCom: o limite do markdown é 4096 bytes, o mais apertado dos seis canais.
	chID := f.createChannel(t, map[string]any{
		"name":   "Resumo segmentado",
		"kind":   notify.KindWeCom,
		"mode":   db.NotifyModeDigest,
		"config": map[string]any{"webhook": hook.URL},
	})
	const total = 60
	// Título mais longo, garantindo que 60 itens excedam 4096 bytes com folga, forçando a segmentação.
	longName := strings.Repeat("超长漏洞名称", 6)
	for i := 0; i < total; i++ {
		f.record(t, longName+strconv.Itoa(i+1), "high")
	}
	ctx := context.Background()
	if _, _, err := f.pg.FanOutPendingEvents(ctx, 500); err != nil {
		t.Fatal(err)
	}
	f.agePendingBatch(t, chID)
	ch := f.channel(t, chID)

	f.n.stepDigest(ctx, ch, 50, "")
	if hook.count() != 1 {
		t.Fatalf("deveria enviar só uma mensagem, obtido %d", hook.count())
	}

	var sent, pending int
	if err := f.pg.QueryRow(`SELECT
    count(*) FILTER (WHERE state=$2),
    count(*) FILTER (WHERE state=$3)
  FROM notification_deliveries WHERE channel_id=$1`, chID, db.NotifyStateSent, db.NotifyStatePending).
		Scan(&sent, &pending); err != nil {
		t.Fatal(err)
	}
	if sent == 0 {
		t.Fatal("deveria haver itens marcados como entregues")
	}
	if pending == 0 {
		t.Fatalf("um lote de %d não poderia caber todo em 4096 bytes, deveria haver restante pendente; sent=%d", total, sent)
	}
	if sent+pending != total {
		t.Fatalf("a contagem de itens não bate: sent=%d pending=%d total=%d (nem entregue nem pendente = perdido)", sent, pending, total)
	}
	// O corpo da mensagem deve informar fielmente quantos itens não estão incluídos nesta.
	if text := markdownText(t, hook.last(t)); !strings.Contains(text, "其余") {
		t.Fatalf("a mensagem deveria indicar que há itens não incluídos nesta:\n%.400s", text)
	}

	// Os itens adiados não podem consumir orçamento de retry: ao pegar, attempts já foi +1 otimisticamente, ao adiar é preciso subtrair de volta.
	var maxAttempts int
	if err := f.pg.QueryRow(`SELECT COALESCE(max(attempts),0) FROM notification_deliveries
WHERE channel_id=$1 AND state=$2`, chID, db.NotifyStatePending).Scan(&maxAttempts); err != nil {
		t.Fatal(err)
	}
	if maxAttempts > 0 {
		t.Fatalf("itens adiados não deveriam consumir tentativas de retry (senão em poucos itens seriam dados como falha), obtido attempts=%d", maxAttempts)
	}

	// Roda repetidamente até convergir. Afirma-se **no fim tudo entregue** e que no meio houve de fato várias rodadas —
	// isso é mais forte que "a segunda rodada termina": prova que a segmentação não trava nem perde os itens restantes.
	rounds := 0
	for {
		var undelivered int
		if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries
WHERE channel_id=$1 AND state <> $2 AND state <> $3`, chID, db.NotifyStateSent, db.NotifyStateFailed).
			Scan(&undelivered); err != nil {
			t.Fatal(err)
		}
		if undelivered == 0 {
			break
		}
		rounds++
		if rounds > total+5 {
			t.Fatalf("a entrega segmentada não converge: rodou %d rodadas e ainda há %d em aberto", rounds, undelivered)
		}
		before := hook.count()
		f.n.stepDigest(ctx, ch, 50, "")
		if hook.count() == before {
			t.Fatalf("a %d-ésima rodada não teve nenhum progresso, os %d restantes ficarão presos para sempre", rounds, undelivered)
		}
	}
	if rounds < 2 {
		t.Fatalf("uma mensagem de 4096 bytes não comporta %d vulnerabilidades de título longo, deveria enviar em várias rodadas, mas usou só %d", total, rounds)
	}
	// Cada rodada depois da primeira deve ser **só continuação**, sem itens rejeitados pelo canal.
	var failed int
	if err := f.pg.QueryRow(`SELECT count(*) FROM notification_deliveries WHERE channel_id=$1 AND state=$2`,
		chID, db.NotifyStateFailed).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if failed != 0 {
		t.Fatalf("o receptor falso sempre retorna sucesso, não deveria haver itens com falha, obtido %d", failed)
	}
}

// TestNotifyBackoffTableMatchesAttemptBudget é uma asserção anti-deriva.
//
// O orçamento de retry (db.MaxNotifyAttempts) e a tabela da sequência de backoff (notifyBackoff) ficam em dois pacotes:
// o primeiro é a política da máquina de estados, o segundo o ritmo de execução do motor. Se mudar só um — por exemplo subir o orçamento para 5
// e esquecer de acrescentar faixas de backoff — o código não dá erro, só faz o 4º e 5º retries reusarem o último intervalo,
// aparecendo como "o ritmo de retry ficou lento sem motivo", e ao investigar é difícil associar a isto.
// Afirma que os dois têm o mesmo comprimento, expondo essa deriva já no CI.
func TestNotifyBackoffTableMatchesAttemptBudget(t *testing.T) {
	if len(notifyBackoff) != db.MaxNotifyAttempts {
		t.Fatalf("o nº de faixas de backoff (%d) difere do máximo de tentativas (%d) — mudar um exige mudar o outro",
			len(notifyBackoff), db.MaxNotifyAttempts)
	}
	// O intervalo de backoff deve ser monotônico não decrescente, senão os retries ficariam cada vez mais apressados, agravando o limite de taxa.
	for i := 1; i < len(notifyBackoff); i++ {
		if notifyBackoff[i] < notifyBackoff[i-1] {
			t.Fatalf("o intervalo de backoff deve ser monotônico não decrescente: faixa %d %v < faixa %d %v",
				i, notifyBackoff[i], i-1, notifyBackoff[i-1])
		}
	}
}

// TestNotifyRateLimitDoesNotConsumeRetryBudget trava a ordem "primeiro pegar o token, depois pegar a entrega".
// Se fosse invertida (pega e depois descarta), a entrega barrada pelo limite de taxa já teria contado um attempts,
// o orçamento seria consumido pela pura espera, e acabaria em failed.
func TestNotifyRateLimitDoesNotConsumeRetryBudget(t *testing.T) {
	// Testa só o token bucket em si, não precisa de Server (nem deve criar um para isso).
	n := &Notifier{buckets: map[int64]*notifyBucket{}}
	now := time.Now()
	// 1 por minuto: com o bucket cheio, no máximo 1.
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now); got != 1 {
		t.Fatalf("com o bucket cheio, 1 por minuto deveria retirar 1 token, obtido %d", got)
	}
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(time.Millisecond)); got != 0 {
		t.Fatalf("esgotados os tokens deveria retornar 0 imediatamente, obtido %d", got)
	}
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(30*time.Second)); got != 0 {
		t.Fatalf("na metade do período não deveria repor um token inteiro, obtido %d", got)
	}
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(time.Minute)); got != 1 {
		t.Fatalf("um período completo deveria repor 1 token, obtido %d", got)
	}
	// Canal sem limite de taxa usa um teto finito, evitando que uma rodada seja travada por backlog infinito.
	if got := n.takeTokens(2, 0, notifyUnlimitedBurstPerTick+10, now); got != notifyUnlimitedBurstPerTick {
		t.Fatalf("sem limite de taxa deveria retornar o teto por rodada %d, obtido %d", notifyUnlimitedBurstPerTick, got)
	}
	// Os token buckets dos canais são independentes entre si.
	if got := n.takeTokens(1, 1, notifyMaxSendsPerChannelPerTick, now.Add(time.Millisecond)); got != 0 {
		t.Fatalf("o bucket do canal 1 deveria continuar vazio, obtido %d", got)
	}
}

// TestNotifyTakeTokensKeepsUnusedTokens trava a semântica de "retirar só want".
//
// A implementação antiga esvaziava o bucket inteiro e só depois o chamador truncava, então um canal com rate=100/min enchia o bucket,
// usava só 5 por rodada, e os 95 tokens restantes eram descartados; quando o canal não tinha entrega pendente na rodada também cobrava igual.
// O resultado é que o comentário que afirmava "com backlog dá para despejar rate_per_min de uma vez" não funcionava em nenhum caso.
func TestNotifyTakeTokensKeepsUnusedTokens(t *testing.T) {
	n := &Notifier{buckets: map[int64]*notifyBucket{}}
	now := time.Now()
	// O bucket começa cheio (100), nesta rodada só se quer 5.
	if got := n.takeTokens(1, 100, 5, now); got != 5 {
		t.Fatalf("com want=5 deveria retirar exatamente 5 tokens, obtido %d", got)
	}
	// Asserção-chave: os 95 restantes devem continuar no bucket, e não ser esvaziados e descartados.
	// Sem avançar o tempo, garante-se que o obtido só pode vir do estoque e não de reposição.
	if got := n.takeTokens(1, 100, 95, now); got != 95 {
		t.Fatalf("os tokens restantes deveriam continuar disponíveis (esperado 95), obtido %d — o bucket foi esvaziado na rodada inteira", got)
	}
	if got := n.takeTokens(1, 100, 1, now); got != 0 {
		t.Fatalf("o bucket já foi esgotado, deveria retornar 0, obtido %d", got)
	}
	// want<=0 não deve debitar nenhum token (rodada vazia não é cobrada).
	n2 := &Notifier{buckets: map[int64]*notifyBucket{}}
	if got := n2.takeTokens(1, 20, 0, now); got != 0 {
		t.Fatalf("want=0 deveria retornar 0, obtido %d", got)
	}
	if got := n2.takeTokens(1, 20, 20, now); got != 20 {
		t.Fatalf("a chamada com want=0 não deveria consumir tokens, deveria ainda poder retirar 20 cheios, obtido %d", got)
	}
}

// TestDigestTickPlanDecouplesBatchSizeFromSendBudget crava as duas grandezas do modo resumo.
//
// Assim que o tamanho do lote de resumo é atrelado ao orçamento de requisições por rodada, um canal com rate_per_min=20 só consegue repor
// 1 token a cada tick de 3 segundos, então cada mensagem de resumo leva só 1 vulnerabilidade — funcionalmente equivale a não ter
// resumo, e o cabeçalho da mensagem ainda diz "1 nova vulnerabilidade nos últimos 30 minutos". Essa degradação não dá erro,
// e os casos ponta a ponta existentes também não a revelam (eles passam à mão para o stepDigest um limit grande o bastante,
// contornando o cálculo de cota em step), por isso aqui se afirma diretamente a própria decisão.
func TestDigestTickPlanDecouplesBatchSizeFromSendBudget(t *testing.T) {
	tokens, claimLimit := digestTickPlan()
	// Um lote = uma mensagem = uma requisição = um token. A unidade do token é a mensagem, não a vulnerabilidade.
	if tokens != 1 {
		t.Fatalf("um lote de resumo envia só uma mensagem, deveria consumir exatamente 1 token, obtido %d", tokens)
	}
	if claimLimit != db.MaxDigestBatchSize {
		t.Fatalf("o tamanho do lote de resumo deveria ser o teto de memória db.MaxDigestBatchSize=%d, obtido %d",
			db.MaxDigestBatchSize, claimLimit)
	}
	// Relação-chave: o tamanho do lote deve ser muito maior que o orçamento de requisições por rodada. Se os dois ficarem na mesma ordem de grandeza,
	// significa que de novo se misturou "quantas mensagens enviar" com "quantas vulnerabilidades cabem num lote" num só número.
	if claimLimit <= notifyMaxSendsPerChannelPerTick {
		t.Fatalf("o tamanho do lote de resumo %d não deveria ser limitado pelo orçamento de requisições por rodada %d — "+
			"o orçamento de requisições é o 'quantas requisições' derivado do lease, e é uma grandeza diferente de 'quantas vulnerabilidades cabem num lote'",
			claimLimit, notifyMaxSendsPerChannelPerTick)
	}
}

// TestNotifyTickBudgetFitsWithinLease é mais uma asserção anti-deriva.
//
// O teto de entregas por canal por rodada (notifyMaxSendsPerChannelPerTick) é derivado da duração do lease:
// o pior caso de entregas em série numa rodada precisa ser < lease, senão o lease das últimas expira antes de serem enviadas,
// e em implantação multi-instância o outro lado as repega e reenvia em duplicidade. Estas três constantes ficam em lugares diferentes,
// mudar qualquer uma pode quebrar a relação sem nenhum erro — por isso aqui se crava.
func TestNotifyTickBudgetFitsWithinLease(t *testing.T) {
	worst := time.Duration(notifyMaxSendsPerChannelPerTick) * notifySendTimeout
	if worst >= notifyLease {
		t.Fatalf("o pior caso de uma rodada de um canal %v não deveria atingir ou exceder o lease %v"+
			"(notifyMaxSendsPerChannelPerTick=%d × notifySendTimeout=%v) — "+
			"mudar qualquer uma destas três constantes exige verificar as outras duas",
			worst, notifyLease, notifyMaxSendsPerChannelPerTick, notifySendTimeout)
	}
}
