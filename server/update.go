package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/Autumn-27/artex/selfupdate"
)

// A interface HTTP da atualização com um clique na página. Toda a lógica real de download/verificação/troca está no pacote selfupdate,
// aqui só se cuida da fronteira de autenticação, exclusão mútua de concorrência, broadcast do progresso, e de avisar o main de que "é hora de sair".
//
// A reinicialização não é feita por este processo: depois de deixar a nova versão em staging o processo sai com selfupdate.ExitRestart,
// e o script supervisor (start.sh / start.bat, no Docker o ENTRYPOINT) o sobe de novo.

// restartCh é fechado quando a atualização fica pronta ou o rollback termina, e o main ao recebê-lo sai com ExitRestart.
var (
	restartOnce sync.Once
	restartCh   = make(chan struct{})
)

// RestartRequested retorna um channel que é fechado quando "o processo deve sair e deixar o supervisor me subir de novo".
func RestartRequested() <-chan struct{} { return restartCh }

func requestRestart() { restartOnce.Do(func() { close(restartCh) }) }

// bootState é a conclusão do selfupdate.Bootstrap desta inicialização (atualização bem-sucedida / acabou de fazer rollback /
// o artefato em staging foi descartado), injetada pelo main, para o /api/update/check informar ao frontend, com fidelidade, o destino da última atualização.
var (
	bootStateMu sync.Mutex
	bootState   selfupdate.State
)

// SetBootUpdateState é chamado uma vez pelo main na inicialização.
func SetBootUpdateState(st selfupdate.State) {
	bootStateMu.Lock()
	defer bootStateMu.Unlock()
	bootState = st
}

func bootUpdateState() selfupdate.State {
	bootStateMu.Lock()
	defer bootStateMu.Unlock()
	return bootState
}

// releaseCache guarda em cache o resultado da consulta da última versão no GitHub.
//
// O aviso "há nova versão" na barra superior consulta uma vez a cada carga completa da página, e a API do GitHub sem autenticação é de
// 60 vezes por hora por IP — sem cache, abrir várias abas ou recarregar algumas vezes esgota a cota,
// e depois, quando se quer mesmo atualizar, não dá para consultar. Quando o usuário clica explicitamente em "verificar atualização" pode-se usar force para contornar o cache.
type releaseCache struct {
	mu  sync.Mutex
	rel *selfupdate.Release
	err error
	at  time.Time
	// fetch é a função de busca, ponto de injeção deixado só para testes; quando nil, faz a consulta real ao GitHub.
	fetch func(context.Context, *http.Client) (*selfupdate.Release, error)
}

const (
	releaseTTL = 30 * time.Minute
	// O resultado de falha também é guardado em cache por um instante, senão, com o GitHub inacessível, cada carga de página teria de esperar um timeout;
	// mas o TTL deve ser curto, para que quando a rede voltar ela se cure sozinha logo.
	releaseErrTTL = 2 * time.Minute
	// Timeout da consulta. O timeout de 30 minutos do NewClient é para baixar o pacote inteiro, consultar versão não pode esperar tanto.
	releaseTimeout = 20 * time.Second
)

var relCache = &releaseCache{}

// get retorna o Release mais recente, se acertar o cache não acessa a rede.
//
// Mantém o lock durante a busca: requisições concorrentes ficam na fila esperando o resultado da mesma consulta, em vez de cada uma ir bater no GitHub
// (várias abas consultando ao mesmo tempo logo após carregar a página é justamente o momento em que mais se aciona o limite de taxa).
func (c *releaseCache) get(ctx context.Context, client *http.Client, force bool) (*selfupdate.Release, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !force {
		ttl := releaseTTL
		if c.err != nil {
			ttl = releaseErrTTL
		}
		if !c.at.IsZero() && time.Since(c.at) < ttl {
			return c.rel, c.err
		}
	}

	fetch := c.fetch
	if fetch == nil {
		fetch = selfupdate.FetchLatest
	}
	ctx, cancel := context.WithTimeout(ctx, releaseTimeout)
	defer cancel()
	rel, err := fetch(ctx, client)
	// Requisição cancelada (usuário fechou a aba) não significa que o GitHub tem problema, não a grave no cache,
	// senão o próximo visitante receberia um erro misterioso de "cancelado".
	if err != nil && ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
		return c.rel, err
	}
	c.rel, c.err, c.at = rel, err, time.Now()
	return rel, err
}

// updateProgress é um item de progresso enviado ao frontend.
type updateProgress struct {
	Phase   selfupdate.Phase `json:"phase"`
	Percent int              `json:"percent"` // só tem significado na fase de download; nas demais é -1
	Message string           `json:"message"`
	Version string           `json:"version,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// updateHub guarda o progresso de uma atualização e o transmite aos assinantes SSE.
//
// running também serve de exclusão mútua: durante a atualização, outro POST /api/update/apply recebe 409 direto,
// evitando que duas goroutines escrevam ao mesmo tempo no mesmo artex.new.
type updateHub struct {
	mu      sync.Mutex
	running bool
	cur     updateProgress
	subs    map[chan updateProgress]struct{}
}

var updHub = &updateHub{
	cur:  updateProgress{Phase: selfupdate.PhaseIdle, Percent: -1},
	subs: map[chan updateProgress]struct{}{},
}

// begin toma a permissão de atualização, se já estiver em andamento retorna false.
func (h *updateHub) begin(version string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running {
		return false
	}
	h.running = true
	h.cur = updateProgress{Phase: selfupdate.PhaseDownload, Percent: 0, Message: "Preparando…", Version: version}
	h.fanout(h.cur)
	return true
}

// finish encerra uma atualização. err nil significa que o staging deu certo, aguardando reinício.
func (h *updateHub) finish(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.running = false
	if err != nil {
		h.cur = updateProgress{Phase: selfupdate.PhaseFailed, Percent: -1, Message: "Falha na atualização", Error: err.Error(), Version: h.cur.Version}
	} else {
		h.cur = updateProgress{Phase: selfupdate.PhaseStaged, Percent: 100, Message: "Nova versão pronta, reiniciando…", Version: h.cur.Version}
	}
	h.fanout(h.cur)
}

func (h *updateHub) publish(ph selfupdate.Phase, pct int, msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cur = updateProgress{Phase: ph, Percent: pct, Message: msg, Version: h.cur.Version}
	h.fanout(h.cur)
}

// fanout deve ser chamado com h.mu em posse. O channel do assinante tem buffer, se encher descarta —
// o progresso é informação transitória descartável, jamais pode deixar uma conexão SSE travada bloquear a própria atualização.
func (h *updateHub) fanout(p updateProgress) {
	for ch := range h.subs {
		select {
		case ch <- p:
		default:
		}
	}
}

func (h *updateHub) snapshot() (updateProgress, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cur, h.running
}

func (h *updateHub) subscribe() (<-chan updateProgress, func()) {
	ch := make(chan updateProgress, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
			close(ch)
		})
	}
}

// updateCheck consulta no GitHub a última versão oficial e a compara com a versão atual.
//
// O frontend também conecta direto em api.github.com (o CORS do GitHub é *), mas **vale esta interface**:
// o download é feito pelo backend, só se pode falar em atualizar se o backend alcança o GitHub. É comum o navegador conectar e o servidor não
// (servidor na rede interna, ou proxy configurado só no navegador), e aí clicar em atualizar falha na certa,
// é melhor reportar o erro com fidelidade já no passo de verificação.
func (s *Server) updateCheck(w http.ResponseWriter, r *http.Request) {
	current := BuildVersion
	mode := "binary"
	if selfupdate.InDocker() {
		mode = "docker"
	}
	boot := bootUpdateState()
	out := map[string]any{
		"current":     current,
		"mode":        mode,
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
		"has_backup":  selfupdate.HasBackup(),
		"repo":        selfupdate.Repo,
		"boot_notice": boot.Detail,
		"rolled_back": boot.RolledBack,
	}

	// O aviso da barra superior usa o cache (padrão); quando o usuário clica em "verificar atualização" envia force=1 para forçar a ida à origem.
	force := r.URL.Query().Get("force") != ""
	client := selfupdate.NewClient(s.m.GlobalProxy())
	rel, err := relCache.get(r.Context(), client, force)
	if err != nil {
		out["error"] = err.Error()
		writeJSON(w, 200, out)
		return
	}

	latest := rel.TagName
	out["latest"] = latest
	out["notes"] = rel.Body
	out["html_url"] = rel.HTMLURL
	if !rel.PublishedAt.IsZero() {
		out["published_at"] = rel.PublishedAt.Format(time.RFC3339)
	}

	asset := selfupdate.AssetName(latest, runtime.GOOS, runtime.GOARCH)
	out["asset"] = asset
	if a, ok := rel.FindAsset(asset); ok {
		out["asset_available"] = true
		out["size"] = a.Size
	} else {
		out["asset_available"] = false
	}

	cmp, comparable := selfupdate.CompareVersions(current, latest)
	out["comparable"] = comparable
	out["has_update"] = comparable && cmp < 0
	if !comparable {
		// Build de desenvolvimento (dev / git describe com sufixo) não tem número de versão comparável. Liberar só
		// sobrescreveria com a versão oficial o binário que se está depurando localmente, então simplesmente não oferece atualização.
		out["reason"] = fmt.Sprintf("a versão atual %q não é uma versão de lançamento oficial, a atualização com um clique foi desativada", current)
	}
	writeJSON(w, 200, out)
}

// updateApply baixa e deixa em staging a nova versão, e ao terminar faz o processo sair para o script supervisor reiniciar.
//
// Retorna 202 imediatamente, o trabalho real roda numa goroutine em segundo plano: baixar o pacote inteiro pode levar minutos,
// pendurado na requisição seria cortado pelo timeout do proxy reverso. O progresso vai por /api/update/stream.
func (s *Server) updateApply(w http.ResponseWriter, r *http.Request) {
	current := BuildVersion

	// Usa o cache: garante que o que se instala é exatamente a versão que o usuário viu e confirmou na interface.
	client := selfupdate.NewClient(s.m.GlobalProxy())
	rel, err := relCache.get(r.Context(), client, false)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	cmp, comparable := selfupdate.CompareVersions(current, rel.TagName)
	if !comparable {
		writeErr(w, 400, fmt.Sprintf("a versão atual %q não é uma versão de lançamento oficial, a atualização com um clique foi desativada", current))
		return
	}
	if cmp >= 0 {
		writeErr(w, 400, fmt.Sprintf("a versão atual já é a mais recente %s", current))
		return
	}
	if !updHub.begin(rel.TagName) {
		writeErr(w, 409, "já há uma atualização em andamento")
		return
	}

	go func() {
		// Deliberadamente usa s.ctx e não o ctx da requisição: assim que a resposta HTTP volta a requisição termina,
		// baixar pendurado nele seria cancelado na hora.
		err := selfupdate.Stage(s.ctx, client, rel, current, func(ph selfupdate.Phase, pct int, msg string) {
			updHub.publish(ph, pct, msg)
		})
		updHub.finish(err)
		if err != nil {
			log.Printf("[update] falha na atualização: %v", err)
			return
		}
		log.Printf("[update] %s → %s em staging, saindo em breve para concluir a troca", current, rel.TagName)
		// Deixa um tempinho para empurrar o último item de progresso ao frontend, e só então dispara a saída.
		time.Sleep(1500 * time.Millisecond)
		requestRestart()
	}()

	writeJSON(w, 202, map[string]any{"ok": true, "target": rel.TagName})
}

// updateRollback volta ativamente à versão anterior (artex.old, backup feito antes da troca).
func (s *Server) updateRollback(w http.ResponseWriter, r *http.Request) {
	if _, running := updHub.snapshot(); running {
		writeErr(w, 409, "atualização em andamento, não é possível fazer rollback")
		return
	}
	if err := selfupdate.Rollback(); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	log.Printf("[update] rollback manual para a versão anterior concluído, saindo em breve para concluir a troca")
	writeJSON(w, 202, map[string]any{"ok": true})
	go func() {
		time.Sleep(500 * time.Millisecond)
		requestRestart()
	}()
}

// updateStream envia o progresso da atualização por SSE.
func (s *Server) updateStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, unsub := updHub.subscribe()
	defer unsub()

	send := func(p updateProgress) {
		b, _ := json.Marshal(p)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	// Primeiro envia o estado atual, para que após recarregar a página se veja de imediato a atualização em andamento.
	cur, _ := updHub.snapshot()
	send(cur)

	ctx := r.Context()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-ch:
			if !ok {
				return
			}
			send(p)
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}
