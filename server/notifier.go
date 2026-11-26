package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Autumn-27/artex/db"
	"github.com/Autumn-27/artex/notify"
)

// Chaves de configuração global (ficam na tabela chave-valor settings, sem precisar criar tabela).
const (
	// settingNotifyEnabled é o interruptor geral de notificações. Ligado por padrão: serve para estancar tudo de uma vez em manutenção,
	// e não é condição para habilitar a funcionalidade — a condição real é "há canal configurado ou não".
	settingNotifyEnabled = "notify_enabled"
	// settingNotifyPublicBaseURL é o endereço externo de acesso usado para gerar o link de volta
	// para os detalhes da vulnerabilidade (ex.: https://artex.example.com). Se vazio, a mensagem não leva botão de link de volta.
	// O projeto não tem configuração de endereço externo reutilizável, por isso este item é novo.
	settingNotifyPublicBaseURL = "notify_public_base_url"
	// settingNotifyDigestMinutes é o período do modo resumo (em minutos).
	settingNotifyDigestMinutes = "notify_digest_interval_min"
)

const (
	// notifyTick é o intervalo de polling do motor de entrega. 3 segundos é o teto de tempo real deste motor,
	// e também a principal fonte de atraso entre "vulnerabilidade gravada no banco" e "mensagem chegar ao IM".
	notifyTick = 3 * time.Second
	// notifyLease é a duração do lease ao pegar entregas. Precisa ser bem maior que o pior caso de uma única entrega
	// (timeout do cliente HTTP do pacote notify: 15 segundos), senão a mesma linha pode ser entregue por dois
	// dispatchers ao mesmo tempo.
	notifyLease = 3 * time.Minute
	// notifyFanOutPerTick limita o nº de eventos despachados por rodada, evitando que ao habilitar um canal pela primeira vez
	// todo o backlog histórico seja expandido de uma vez em tarefas de entrega.
	notifyFanOutPerTick = 200
	// notifyDefaultDigestMinutes é o valor padrão do período de resumo.
	notifyDefaultDigestMinutes = 30
	// notifyUnlimitedBurstPerTick é o teto de entregas por rodada quando o canal não tem limite de taxa.
	// Existe para impedir que "um canal configurado sem limite de taxa + uma varredura que acha milhares de vulnerabilidades de uma vez"
	// arraste o laço de uma rodada para um bloqueio longo.
	notifyUnlimitedBurstPerTick = 50
	// notifyMaxSendsPerChannelPerTick é o máximo de entregas por canal por rodada.
	//
	// Este teto é derivado da **duração do lease**: ao pegar as linhas, marca-se um lease (notifyLease = 3 minutos);
	// se numa rodada o nº de entregas em série for tão grande que o pior caso excede o lease, o lease das últimas expira antes de elas serem enviadas.
	// Dentro de um único processo não importa (Run é uma única goroutine em série, o tick não reentra), mas com **dois
	// processos no mesmo banco**, o outro lado repega as linhas com lease expirado e reenvia em duplicidade, ainda
	// incrementa attempts em dobro e dá a entrega como falha enquanto o processo original ainda está entregando.
	//
	// Valor: lease de 3 minutos / timeout de 30 segundos por envio = 6 usa **exatamente todo o lease**, zero folga,
	// não pode ser; com 5 o pior caso é 150 segundos, deixando 30 segundos de folga. Essa relação é
	// cravada por TestNotifyTickBudgetFitsWithinLease — mudar notifyLease,
	// notifySendTimeout ou este valor, qualquer um deles, faz essa asserção falhar.
	notifyMaxSendsPerChannelPerTick = 5
	// notifySendTimeout é o timeout de uma única entrega. Ele também determina o valor da constante anterior,
	// o produto dos dois não pode exceder notifyLease, ver TestNotifyTickBudgetFitsWithinLease.
	notifySendTimeout = 30 * time.Second
)

// notifyBackoff é a sequência de backoff dos retries após falha, o índice é o nº de tentativas já feitas.
// 3 chances (incluindo a primeira) correspondem a db.MaxNotifyAttempts, os dois devem ser alterados juntos.
var notifyBackoff = []time.Duration{
	time.Second,
	5 * time.Second,
	30 * time.Second,
}

// Notifier é o motor de entrega das notificações de vulnerabilidades.
//
// Roda como goroutine independente, ao lado do Scheduler (ver server.New). Deliberadamente não reutiliza
// o tick do Scheduler: a exigência de tempo real das notificações (3 segundos) difere do ritmo de negócio dos triggers,
// e as falhas de um não contaminam o outro — uma notificação travada não deve afetar os triggers de agent.
type Notifier struct {
	s  *Server
	pg *db.DB

	// mu protege buckets. Poucos canais e baixa contenção, um mutex basta,
	// não vale introduzir uma estrutura de granularidade mais fina para isso.
	mu      sync.Mutex
	buckets map[int64]*notifyBucket
}

// notifyBucket é o token bucket de um único canal.
//
// Usa-se token bucket e não uma janela deslizante "contar por minuto e zerar" porque o efeito de borda desta última é péssimo:
// enviar 20 no fim da janela e mais 20 logo em seguida são, para a plataforma, 40 em um segundo,
// e seria limitado; o token bucket repõe a taxa constante, evitando naturalmente esse tipo de rajada.
type notifyBucket struct {
	tokens   float64
	lastFill time.Time
}

func newNotifier(s *Server) *Notifier {
	return &Notifier{s: s, pg: s.m.pg, buckets: map[int64]*notifyBucket{}}
}

// Run executa o laço até o ctx terminar. Iniciado uma vez por server.New.
func (n *Notifier) Run(ctx context.Context) {
	if n.pg == nil {
		return
	}
	t := time.NewTicker(notifyTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n.step(ctx)
		}
	}
}

// step executa uma rodada: primeiro despacha eventos novos, depois entrega as tarefas vencidas.
//
// Qualquer passo que falhe só registra log e não interrompe o laço — uma falha do sistema de notificações jamais pode virar problema de nível de processo.
// Cada tick é independente, a próxima rodada naturalmente tenta de novo.
func (n *Notifier) step(ctx context.Context) {
	if !n.enabled() {
		return
	}
	if _, _, err := n.pg.FanOutPendingEvents(ctx, notifyFanOutPerTick); err != nil {
		log.Printf("[notify] falha ao despachar eventos: %v", err)
		return
	}
	channels, err := n.pg.ListNotificationChannels(ctx)
	if err != nil {
		log.Printf("[notify] falha ao ler canais: %v", err)
		return
	}
	baseURL := n.publicBaseURL()
	for _, ch := range channels {
		if !ch.IsEnabled() {
			continue
		}
		// A unidade de medida do token bucket é o **nº de mensagens** (equivale ao nº de requisições HTTP), não o nº de vulnerabilidades.
		// No modo tempo real os dois são iguais (uma vulnerabilidade, uma mensagem); no modo resumo um lote inteiro de vulnerabilidades é composto
		// em uma só mensagem, então consome apenas um token.
		//
		// Nos dois modos primeiro se consulta o token bucket e depois se pega conforme a cota — a ordem não pode ser invertida, senão as entregas
		// barradas pelo limite de taxa já teriam consumido tentativas de retry.
		now := time.Now()
		if ch.Mode == db.NotifyModeDigest {
			tokens, claimLimit := digestTickPlan()
			if n.takeTokens(ch.ID, ch.RatePerMin, tokens, now) <= 0 {
				continue
			}
			n.stepDigest(ctx, ch, claimLimit, baseURL)
			continue
		}
		allow := n.takeTokens(ch.ID, ch.RatePerMin, notifyMaxSendsPerChannelPerTick, now)
		if allow <= 0 {
			continue
		}
		n.stepRealtime(ctx, ch, allow, baseURL)
	}
}

// digestTickPlan retorna, para um canal em modo resumo nesta rodada, o consumo de tokens e o limite superior do tamanho do lote.
//
// Os dois valores retornados são **grandezas diferentes**, e essa é a razão de ser uma função independente:
//
//   - tokens é o nº de mensagens. Um lote de vulnerabilidades vira uma mensagem, uma requisição HTTP, então é sempre 1.
//     rate_per_min portanto continua valendo para o digest (no máximo este tanto de mensagens de resumo por minuto).
//   - claimLimit é quantas vulnerabilidades cabem no máximo neste lote. Só é limitado pelo teto de memória, sem relação com o orçamento de requisições.
//
// Já se passou diretamente, como tamanho do lote, o orçamento de requisições por rodada
// (notifyMaxSendsPerChannelPerTick, derivado do lease) só para fazer rate_per_min valer para o digest.
// A consequência foi que um canal com rate_per_min=20 repunha só 1 token num tick de 3 segundos, então cada mensagem de resumo
// levava só 1 vulnerabilidade — o digest degenerava em "notificação em tempo real com texto de resumo", o leitor recebia uma sequência de
// "1 nova vulnerabilidade nos últimos 30 minutos", e db.MaxDigestBatchSize nunca era alcançável.
//
// Esse sintoma não é fácil de perceber em testes de ponta a ponta (os casos existentes passam à mão um limit grande o bastante para
// stepDigest, contornando o cálculo de cota em step), por isso a decisão fica reunida aqui e é cravada diretamente por
// TestDigestTickPlanDecouplesBatchSizeFromSendBudget.
func digestTickPlan() (tokens, claimLimit int) {
	return 1, db.MaxDigestBatchSize
}

// stepRealtime pega e entrega as tarefas em tempo real de um canal, uma vulnerabilidade, uma mensagem.
func (n *Notifier) stepRealtime(ctx context.Context, ch *db.NotificationChannel, allow int, baseURL string) {
	deliveries, err := n.pg.ClaimRealtimeDeliveries(ctx, ch.ID, allow, notifyLease)
	if err != nil {
		log.Printf("[notify] falha ao pegar entregas em tempo real channel=%d: %v", ch.ID, err)
		return
	}
	if len(deliveries) == 0 {
		return
	}
	channel, cfg, ok := n.adapt(ch)
	if !ok {
		_ = n.pg.FailDeliveries(ctx, deliveryIDs(deliveries), fmt.Sprintf("tipo de canal %q não registrado", ch.Kind))
		return
	}
	for _, dl := range deliveries {
		msg, err := n.renderSingle(ctx, dl, baseURL)
		if err != nil {
			// Falha de renderização é problema de dados locais, retry não melhora.
			_ = n.pg.FailDeliveries(ctx, []int64{dl.ID}, err.Error())
			continue
		}
		n.send(ctx, channel, cfg, msg, []*db.NotificationDelivery{dl})
	}
}

// stepDigest, quando o lote vence, agrega as entregas pendentes de um canal em uma única mensagem e a envia.
func (n *Notifier) stepDigest(ctx context.Context, ch *db.NotificationChannel, allow int, baseURL string) {
	window := n.digestInterval()
	due, err := n.pg.DigestBatchDue(ctx, ch.ID, window)
	if err != nil {
		log.Printf("[notify] falha ao avaliar o lote de resumo channel=%d: %v", ch.ID, err)
		return
	}
	if !due {
		return
	}
	deliveries, err := n.pg.ClaimDigestBatch(ctx, ch.ID, allow, notifyLease)
	if err != nil {
		log.Printf("[notify] falha ao pegar o lote de resumo channel=%d: %v", ch.ID, err)
		return
	}
	if len(deliveries) == 0 {
		return
	}
	channel, cfg, ok := n.adapt(ch)
	if !ok {
		_ = n.pg.FailDeliveries(ctx, deliveryIDs(deliveries), fmt.Sprintf("tipo de canal %q não registrado", ch.Kind))
		return
	}
	msg, included, err := n.renderBatch(ctx, deliveries, baseURL, int(window.Minutes()))
	if err != nil {
		_ = n.pg.FailDeliveries(ctx, deliveryIDs(deliveries), err.Error())
		return
	}
	// As entregas com snapshot corrompido, que não entraram na mensagem, precisam ser dadas como falha explicitamente. Sem isso elas ficariam
	// fora de included, nem na mensagem nem na lista de falhas — quando o envio tem sucesso o estado delas escapa
	// da marcação em lote seguinte, e ficam para sempre em sending até o lease expirar e serem repegas repetidamente.
	if skipped := excludeDeliveries(deliveries, included); len(skipped) > 0 {
		reason := "não foi possível interpretar o snapshot do evento, esta vulnerabilidade não pôde ser renderizada em mensagem"
		if fErr := n.pg.FailDeliveries(ctx, deliveryIDs(skipped), reason); fErr != nil {
			log.Printf("[notify] falha ao marcar entregas com snapshot corrompido channel=%s ids=%v: %v", ch.Kind, deliveryIDs(skipped), fErr)
		}
		log.Printf("[notify] ignoradas %d entregas com snapshot não interpretável channel=%d", len(skipped), ch.ID)
	}
	// Só entrega a send as que entraram na mensagem: included[i] corresponde estritamente a msg.Items[i],
	// send depende dessa correspondência para aplicar nas linhas de entrega corretas o "canal informou que coube os primeiros K".
	n.send(ctx, channel, cfg, msg, included)
}

// send entrega e faz a transição de estado conforme o resultado.
//
// Um mesmo lote de entregas (no modo resumo pode ter dezenas) compartilha um único resultado de envio: ou entrega, ou o lote inteiro faz retry.
// Não se faz retry item a item — a mensagem de resumo é uma só, reenviar uma parte dela bagunçaria a semântica do lote.
//
// A única exceção é a **segmentação causada pelo limite de tamanho do canal**: o canal informou que só coube os primeiros K itens,
// então do item K+1 em diante precisa ficar para o próximo lote, e não ser marcado como sucesso junto. Do contrário as
// vulnerabilidades cortadas não estariam na mensagem nem na lista de falhas, sumindo por completo.
func (n *Notifier) send(ctx context.Context, channel notify.Channel, cfg map[string]any, msg notify.Message, deliveries []*db.NotificationDelivery) {
	// Limita-se o tempo de cada entrega, para que um canal travado não segure todos os demais canais desta rodada.
	sendCtx, cancel := context.WithTimeout(ctx, notifySendTimeout)
	defer cancel()
	delivered, err := channel.Send(sendCtx, cfg, msg)
	if err == nil && delivered > 0 {
		if delivered > len(deliveries) {
			// A quantidade informada pelo canal não pode exceder o nº de entregas; se acontecer, é que a camada de renderização calculou errado,
			// tratar como tudo entregue e registrar o problema é melhor que gravar registros bagunçados.
			log.Printf("[notify] a quantidade entregue informada pelo canal %d excede o nº de entregas %d channel=%s, tratando como tudo entregue",
				delivered, len(deliveries), channel.Kind())
			delivered = len(deliveries)
		}
		sent, rest := deliveries[:delivered], deliveries[delivered:]
		if err := n.pg.MarkDeliveriesSent(ctx, deliveryIDs(sent)); err != nil {
			log.Printf("[notify] falha ao marcar como entregue channel=%s ids=%v: %v", channel.Kind(), deliveryIDs(sent), err)
		}
		if len(rest) > 0 {
			// Esta mensagem atingiu o limite de tamanho do canal: o restante volta imediatamente para a fila, o próximo tick continua o envio.
			// Usa-se DeferDeliveries e não RescheduleDeliveries — isto não é falha,
			// não deve consumir orçamento de retry (ao pegar já se somou +1 otimisticamente, e ali ele é subtraído de volta).
			if err := n.pg.DeferDeliveries(ctx, deliveryIDs(rest),
				fmt.Sprintf("esta mensagem atingiu o limite de tamanho do canal, apenas os primeiros %d foram entregues, o restante fica para o próximo lote", delivered)); err != nil {
				log.Printf("[notify] falha ao enfileirar a continuação da segmentação channel=%s ids=%v: %v", channel.Kind(), deliveryIDs(rest), err)
			}
		}
		return
	}
	if err == nil {
		// O canal não reportou erro nem disse quantas foram entregues. Trata-se como falha (com backoff),
		// para que esta entrega não seja pega repetidamente e nunca consiga ser marcada.
		err = fmt.Errorf("o canal não informou a quantidade entregue (delivered=%d)", delivered)
	}

	// O tratamento de falha é decidido **item a item**, e não pelo maior nº de tentativas do lote inteiro.
	//
	// Antes era `if maxAttempts(deliveries) >= MaxNotifyAttempts` condenando o lote todo, mas as tentativas
	// de cada item do lote não são iguais: uma entrega antiga que já tentou duas vezes (attempts=2) arrasta junto para failed as entregas
	// novinhas do mesmo lote (attempts=1) — uma vulnerabilidade nova perdida para sempre sem usar um único retry,
	// exatamente o oposto da intenção original de "não deixar a linha velha arrastar a nova".
	permanent := notify.IsPermanent(err)
	var failIDs, exhaustedIDs []int64
	byDelay := map[time.Duration][]int64{}
	for _, dl := range deliveries {
		switch {
		case permanent:
			failIDs = append(failIDs, dl.ID)
		case dl.Attempts >= db.MaxNotifyAttempts:
			exhaustedIDs = append(exhaustedIDs, dl.ID)
		default:
			delay := notifyBackoff[min(dl.Attempts, len(notifyBackoff)-1)]
			byDelay[delay] = append(byDelay[delay], dl.ID)
		}
	}

	if len(failIDs) > 0 {
		if fErr := n.pg.FailDeliveries(ctx, failIDs, err.Error()); fErr != nil {
			log.Printf("[notify] erro ao marcar o estado de falha channel=%s ids=%v: %v", channel.Kind(), failIDs, fErr)
		}
	}
	if len(exhaustedIDs) > 0 {
		reason := fmt.Sprintf("ainda falhou após %d tentativas: %s", db.MaxNotifyAttempts, err)
		if fErr := n.pg.FailDeliveries(ctx, exhaustedIDs, reason); fErr != nil {
			log.Printf("[notify] erro ao marcar o estado de falha channel=%s ids=%v: %v", channel.Kind(), exhaustedIDs, fErr)
		}
	}
	// Reagenda agrupando por atraso: há só 3 faixas de backoff, o nº de grupos é naturalmente pequeno, não é preciso emitir um
	// UPDATE por item (isso faria um lote de 500 gerar 500 idas e vindas).
	for delay, group := range byDelay {
		if rErr := n.pg.RescheduleDeliveries(ctx, group, delay, err.Error()); rErr != nil {
			log.Printf("[notify] falha ao reagendar entregas channel=%s ids=%v: %v", channel.Kind(), group, rErr)
		}
	}
	if len(failIDs)+len(exhaustedIDs) > 0 {
		log.Printf("[notify] falha na entrega channel=%d kind=%s falha permanente=%d tentativas esgotadas=%d a retentar=%d: %s",
			deliveries[0].ChannelID, channel.Kind(), len(failIDs), len(exhaustedIDs), len(byDelay), err)
	}
}

// excludeDeliveries retorna os itens de all que não estão em keep (comparados por identidade de ponteiro).
// Usado para achar as entregas que "não conseguiram entrar na mensagem" — elas precisam ser tratadas explicitamente, não podem ficar numa zona cinzenta.
func excludeDeliveries(all, keep []*db.NotificationDelivery) []*db.NotificationDelivery {
	inKeep := make(map[*db.NotificationDelivery]bool, len(keep))
	for _, dl := range keep {
		inKeep[dl] = true
	}
	var out []*db.NotificationDelivery
	for _, dl := range all {
		if !inKeep[dl] {
			out = append(out, dl)
		}
	}
	return out
}

// adapt obtém a implementação do canal e interpreta sua configuração.
// Retorna ok=false se o tipo não estiver registrado; a entrega deve ser dada diretamente como falha, em vez de retry infinito.
func (n *Notifier) adapt(ch *db.NotificationChannel) (notify.Channel, map[string]any, bool) {
	channel, ok := notify.Get(ch.Kind)
	if !ok {
		return nil, nil, false
	}
	var cfg map[string]any
	if len(ch.Config) > 0 {
		// Se a interpretação da configuração falhar, dá-se um map vazio: o Validate do próprio canal apontará "qual campo falta",
		// esse erro orienta melhor o usuário na correção do que um erro de parse de JSON.
		_ = json.Unmarshal(ch.Config, &cfg)
	}
	if cfg == nil {
		cfg = map[string]any{}
	}
	return channel, cfg, true
}

// renderSingle renderiza a mensagem de uma única vulnerabilidade.
func (n *Notifier) renderSingle(ctx context.Context, dl *db.NotificationDelivery, baseURL string) (notify.Message, error) {
	snap, err := parseSnapshot(dl)
	if err != nil {
		return notify.Message{}, err
	}
	item, err := n.itemFor(ctx, snap, baseURL)
	if err != nil {
		return notify.Message{}, err
	}
	return notify.Message{Items: []notify.Item{item}, HomeURL: baseURL}, nil
}

// renderBatch renderiza a mensagem de resumo. Interpreta os snapshots um a um — se um estiver corrompido só ele é ignorado,
// sem deixar que derrube o resumo do lote inteiro.
//
// O valor de retorno included corresponde **estritamente um a um** a msg.Items (i-ésima entrega ↔ i-ésimo item).
// Essa correspondência é exigência rígida: o chamador decide, pelo "canal informou que coube os primeiros K", quais K primeiras entregas
// marcar como entregues. Se aqui se ignorar um snapshot corrompido sem remover a entrega ignorada de included,
// os índices desalinham — o item corrompido, que deveria falhar, seria marcado como entregue, e o item bom seria julgado como não entregue.
// Os corrompidos são marcados como falha explicitamente pelo chamador, ver stepDigest.
func (n *Notifier) renderBatch(ctx context.Context, deliveries []*db.NotificationDelivery, baseURL string, windowMinutes int) (notify.Message, []*db.NotificationDelivery, error) {
	items := make([]notify.Item, 0, len(deliveries))
	included := make([]*db.NotificationDelivery, 0, len(deliveries))
	for _, dl := range deliveries {
		snap, err := parseSnapshot(dl)
		if err != nil {
			// Snapshot corrompido não entra na mensagem nem em included — o tratamento dele é responsabilidade do chamador
			// (marcar explicitamente como falha, e não misturar em "entregue" para passar despercebido).
			log.Printf("[notify] snapshot não interpretável ignorado no lote de resumo delivery=%d: %v", dl.ID, err)
			continue
		}
		item, err := n.itemFor(ctx, snap, baseURL)
		if err != nil {
			return notify.Message{}, nil, err
		}
		items = append(items, item)
		included = append(included, dl)
	}
	if len(items) == 0 {
		return notify.Message{}, nil, fmt.Errorf("as %d entregas do lote de resumo não puderam ser interpretadas", len(deliveries))
	}
	return notify.Message{
		Items:         items,
		Batch:         true,
		WindowMinutes: windowMinutes,
		HomeURL:       baseURL,
	}, included, nil
}

// itemFor renderiza o snapshot do evento em um item a ser notificado, e de quebra resolve o nome do ativo e o link de volta aos detalhes.
func (n *Notifier) itemFor(ctx context.Context, snap notify.Snapshot, baseURL string) (notify.Item, error) {
	assets, err := n.pg.NotificationAssetNames(ctx, snap.AssetIDs)
	if err != nil {
		// Falha ao resolver o nome do ativo não deve impedir a notificação: não ler o nome é muito mais leve que não receber a notificação,
		// a mensagem só perde uma linha de ativo.
		log.Printf("[notify] falha ao resolver o nome do ativo finding=%d: %v", snap.FindingID, err)
	}
	item := notify.Item{
		FindingID:  snap.FindingID,
		Name:       snap.Name,
		VulnClass:  snap.VulnClass,
		Severity:   snap.Severity,
		Summary:    snap.Summary,
		Assets:     assets,
		FromStatus: snap.FromStatus,
		ToStatus:   snap.ToStatus,
	}
	if baseURL != "" {
		// A rota da página de detalhes está em web/src/app/(main)/function/findings/detail/page.tsx,
		// ela lê o id da vulnerabilidade do parâmetro de query id.
		item.DetailURL = fmt.Sprintf("%s/function/findings/detail?id=%d", baseURL, snap.FindingID)
	}
	return item, nil
}

// takeTokens retira do token bucket do canal **no máximo want** tokens e retorna a quantidade efetivamente obtida.
//
// Um token = uma mensagem (uma requisição HTTP). No modo tempo real o chamador passa quantas quiser;
// no modo resumo um lote inteiro de vulnerabilidades envia uma só mensagem, passa 1.
//
// A capacidade do bucket é o limite por minuto do canal, reposta a taxa constante. ratePerMin<=0 significa sem limite de taxa,
// retorna um valor finito mas grande o bastante, para evitar que o laço de uma rodada seja travado por um backlog infinito.
//
// O teto want é necessário: sem ele só se poderia esvaziar o bucket inteiro, e o chamador ainda tem seu próprio teto por rodada,
// os tokens a mais não servem para nada e somem do nada antes da próxima reposição — a capacidade de rajada acumulada nunca seria alcançável,
// até "esta rodada não tem nenhuma entrega pendente" seria cobrada do mesmo jeito.
func (n *Notifier) takeTokens(channelID int64, ratePerMin, want int, now time.Time) int {
	if want <= 0 {
		return 0
	}
	if ratePerMin <= 0 {
		return min(want, notifyUnlimitedBurstPerTick)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	b := n.buckets[channelID]
	if b == nil {
		b = &notifyBucket{tokens: float64(ratePerMin), lastFill: now}
		n.buckets[channelID] = b
	}
	// Repõe conforme o tempo real decorrido, a taxa é ratePerMin/60 por segundo.
	if elapsed := now.Sub(b.lastFill).Seconds(); elapsed > 0 {
		b.tokens = minF(float64(ratePerMin), b.tokens+elapsed*float64(ratePerMin)/60)
		b.lastFill = now
	}
	// Soma-se um epsilon minúsculo antes de arredondar: o nº de tokens é acumulado em ponto flutuante, ao repor em duas vezes até encher
	// 0.5 + 0.5 pode dar 0.9999999999, e int() direto truncaria para 0 —
	// um bucket matematicamente cheio sem conseguir retirar token. 1e-9 é muito menor que um token, não deixa passar um déficit real.
	take := min(int(b.tokens+1e-9), want)
	if take <= 0 {
		return 0
	}
	b.tokens -= float64(take)
	return take
}

// enabled lê o interruptor geral.
func (n *Notifier) enabled() bool {
	return n.pg.GetBool(settingNotifyEnabled, true)
}

// publicBaseURL retorna o endereço externo usado nos links de volta, sem a barra final.
func (n *Notifier) publicBaseURL() string {
	v, ok, err := n.pg.GetSetting(settingNotifyPublicBaseURL)
	if err != nil || !ok {
		return ""
	}
	return trimTrailingSlash(v)
}

// digestInterval retorna o período de resumo, voltando ao padrão se for inválido ou não configurado.
func (n *Notifier) digestInterval() time.Duration {
	v, ok, err := n.pg.GetSetting(settingNotifyDigestMinutes)
	if err != nil || !ok {
		return time.Duration(notifyDefaultDigestMinutes) * time.Minute
	}
	m := 0
	if _, err := fmt.Sscanf(v, "%d", &m); err != nil || m <= 0 {
		return time.Duration(notifyDefaultDigestMinutes) * time.Minute
	}
	return time.Duration(m) * time.Minute
}

// parseSnapshot interpreta o snapshot do evento correspondente à entrega.
func parseSnapshot(dl *db.NotificationDelivery) (notify.Snapshot, error) {
	var snap notify.Snapshot
	if len(dl.Snapshot) == 0 {
		return snap, fmt.Errorf("o snapshot do evento da entrega %d está vazio", dl.ID)
	}
	if err := json.Unmarshal(dl.Snapshot, &snap); err != nil {
		return snap, fmt.Errorf("falha ao interpretar o snapshot do evento da entrega %d: %w", dl.ID, err)
	}
	if snap.Kind == "" {
		// O tipo do evento vale o da linha do evento; o do snapshot pode ter sido gravado por uma versão antiga.
		snap.Kind = dl.EventKind
	}
	return snap, nil
}

func deliveryIDs(deliveries []*db.NotificationDelivery) []int64 {
	out := make([]int64, 0, len(deliveries))
	for _, dl := range deliveries {
		out = append(out, dl.ID)
	}
	return out
}

func trimTrailingSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
