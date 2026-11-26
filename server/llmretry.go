package server

import (
	"time"

	"github.com/Autumn-27/artex/agent"
	"github.com/Autumn-27/artex/db"
)

// Resolução no servidor da política de retry, ver docs/llm-retry-design.md. Entre as cinco camadas:
//   - conexão / resposta vazia / janela de segurança do mesmo provider "seguem o endpoint", e cada configuração de LLM pode sobrescrever
//     o padrão global (item deixado em branco no profile herda o global; se o global também não estiver configurado, usa o padrão embutido);
//   - circuit breaker / reexecução de intenção são de nível de processo, só existe uma cópia global.
//
// A política global lê uma linha de settings no banco; os pontos de chamada estão todos em caminhos de baixa frequência (construir provider, encerramento de work,
// salvar configuração), não vale a pena acrescentar cache; os parâmetros do circuit breaker são exceção — são lidos a cada vez no caminho de falha, então
// são empurrados por applyRetryPolicy para o Registry guardar.

// retryPolicy reads the global policy; a nil DB yields the zero policy (all
// layers on their built-in defaults).
func (s *Server) retryPolicy() db.LLMRetryPolicy {
	if s.m == nil || s.m.pg == nil {
		return db.LLMRetryPolicy{}
	}
	return s.m.pg.LLMRetryPolicy()
}

// resolveRetry layers one profile's override on top of the global policy and
// converts the result into the form agent.Config carries. Rules combine field by
// field, so a profile that only pins an interval still inherits the global count.
func resolveRetry(o db.RetryOverride, pol db.LLMRetryPolicy) agent.RetryConfig {
	connect := o.Connect.Or(pol.Connect)
	empty := o.Empty.Or(pol.Empty)
	stream := o.Stream.Or(pol.Stream)
	return agent.RetryConfig{
		// A contagem mantém aqui a semântica original "0=padrão / negativo=desligado": o MaxRetries /
		// EmptyResponseRetries do SDK é totalmente isomórfico a isso, basta deixá-lo resolver por conta própria.
		ConnectAttempts: connect.Attempts, ConnectInterval: connect.Interval(),
		EmptyAttempts: empty.Attempts, EmptyInterval: empty.Interval(),
		StreamAttempts: stream.Attempts, StreamInterval: stream.Interval(),
	}
}

// applyProfileRetry fills cfg.Retry for a profile read from the DB.
func (s *Server) applyProfileRetry(cfg *agent.Config, p *db.LLMProfile) {
	if p == nil {
		return
	}
	cfg.Retry = resolveRetry(p.Retry, s.retryPolicy())
}

// Padrões do circuit breaker (cooldown do rodízio), iguais aos embutidos no llmpool — aqui só sobrescreve quando "o usuário configurou um valor".
// Os padrões da reexecução de intenção estão em modelErrorRetries / modelErrorRetryBackoff de engine.go.

// applyRetryPolicy pushes the process-wide layers of the policy into the objects
// that consume them on a hot path: the circuit-breaker registry. Called at
// startup and whenever the policy is saved.
func (s *Server) applyRetryPolicy() {
	pol := s.retryPolicy()
	if s.llmHealth != nil {
		s.llmHealth.SetPolicy(pol.Breaker.Attempts, pol.Breaker.Interval())
	}
}

// modelErrorRetryPolicy resolves the intent-level replay knobs (layer ⑤): how
// many times a model_error work is re-run and how long to back off between runs.
func (e *Engine) modelErrorRetryPolicy() (retries int, backoff time.Duration) {
	retries, backoff = modelErrorRetries, modelErrorRetryBackoff
	if e == nil || e.m == nil || e.m.pg == nil {
		return retries, backoff
	}
	rule := e.m.pg.LLMRetryPolicy().Intent
	if rule.Attempts != 0 {
		retries = max(rule.Attempts, 0)
	}
	if d := rule.Interval(); d > 0 {
		backoff = d
	}
	return retries, backoff
}

// emptyTurnNudgeLimit resolves how many empty-turn continuations one work may
// inject (see steerHooks.Stop). It deliberately reuses layer ②'s knob — "nº de
// retries de resposta vazia": os dois são duas formas da mesma coisa. A camada do SDK cuida de "nenhum bloco de conteúdo", com o recurso de reenviar
// a mesma requisição tal qual; aqui se cuida de "só raciocínio, sem texto nem ferramenta", com o recurso de acrescentar uma instrução para o
// modelo continuar com o raciocínio que já tem (reenviar tal qual não faz sentido para esse turno ocioso determinado pela forma do contexto). O critério de vazio
// difere porque o SDK se baseia em "se houve algum evento emitido", e o delta de raciocínio já é um evento — mas quando o usuário configura
// "quantos retries de resposta vazia" quer dizer "se o modelo não produziu conteúdo substancial, tente de novo"; as duas camadas compartilharem um só número
// é o que bate com esse modelo mental.
//
// Lê a política global e não a sobrescrita de algum profile: durante um run pode haver troca de profile por failover, e este
// é o limite total da intenção inteira, não deve mudar junto com o endpoint. Semântica isomórfica ao emptyRetries() do SDK:
// 0 = padrão defaultEmptyTurnNudges; -1 (negativo) = desliga a continuação de turno ocioso; >0 = usa esse valor.
func (e *Engine) emptyTurnNudgeLimit() int {
	if e == nil || e.m == nil || e.m.pg == nil {
		return defaultEmptyTurnNudges
	}
	switch n := e.m.pg.LLMRetryPolicy().Empty.Attempts; {
	case n == 0:
		return defaultEmptyTurnNudges
	case n < 0:
		return 0
	default:
		return n
	}
}
