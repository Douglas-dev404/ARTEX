package db

import (
	"encoding/json"
	"time"
)

// Política de retentativa do LLM: configuração global de "quantidade + intervalo" das cinco camadas de retentativa, ver docs/LLM重试设计.md.
// Guardada em um valor JSON da tabela settings — são parâmetros de execução únicos da máquina, não justificam uma tabela própria;
// a leitura usa o padrão embutido como fallback, então quando a chave não existe (banco novo / nunca configurado) o comportamento é idêntico ao da época das constantes fixas.

const settingLLMRetryPolicy = "llm_retry_policy"

// RetryRule is one layer's knob pair. The zero value means "unset":
//
//	Attempts   0 = usa a quantidade padrão embutida; -1 = desliga a retentativa dessa camada; >0 = usa esse valor
//	IntervalMS 0 = usa a estratégia de intervalo original da camada (em geral backoff exponencial); >0 = passa a usar um intervalo fixo em milissegundos
//
// -1 é "desligar explicitamente" e não "0 vezes", porque 0 já está ocupado por "não configurado".
type RetryRule struct {
	Attempts   int `json:"attempts"`
	IntervalMS int `json:"interval_ms"`
}

// Interval returns the configured fixed interval, or 0 when unset (caller keeps
// its own default ladder).
func (r RetryRule) Interval() time.Duration {
	if r.IntervalMS <= 0 {
		return 0
	}
	return time.Duration(r.IntervalMS) * time.Millisecond
}

// Or returns the rule with each unset field filled in from fallback. Used to
// layer a profile override on top of the global policy field by field, so a
// profile that only pins the interval still inherits the global count.
func (r RetryRule) Or(fallback RetryRule) RetryRule {
	if r.Attempts == 0 {
		r.Attempts = fallback.Attempts
	}
	if r.IntervalMS == 0 {
		r.IntervalMS = fallback.IntervalMS
	}
	return r
}

// retry knob bounds. A count above the cap turns a blip into a token bonfire;
// an interval above an hour outlives any transient failure worth waiting out.
const (
	maxRetryAttempts   = 20
	maxRetryIntervalMS = 3600_000 // 1h
)

// Clamped returns the rule with out-of-range values pulled back into the sane
// band (attempts within [-1, 20], interval within [0, 1h]).
func (r RetryRule) Clamped() RetryRule {
	if r.Attempts < -1 {
		r.Attempts = -1
	}
	if r.Attempts > maxRetryAttempts {
		r.Attempts = maxRetryAttempts
	}
	if r.IntervalMS < 0 {
		r.IntervalMS = 0
	}
	if r.IntervalMS > maxRetryIntervalMS {
		r.IntervalMS = maxRetryIntervalMS
	}
	return r
}

// Clamped bounds a profile's override the same way the global policy is bounded,
// so a hand-crafted API payload can't land a value the CHECK constraint rejects.
func (o RetryOverride) Clamped() RetryOverride {
	o.Connect, o.Empty, o.Stream = o.Connect.Clamped(), o.Empty.Clamped(), o.Stream.Clamped()
	return o
}

// LLMRetryPolicy holds the five-layer retry configuration. Connect/Empty/Stream are the
// per-request layers (a profile may override them, see LLMProfile.Retry);
// Breaker and Intent are process-wide by nature and live only here.
type LLMRetryPolicy struct {
	// Connect: retentativa de conexão do SDK (connection reset/timeout/429/5xx, antes de o stream começar). Padrão: 3 vezes, backoff exponencial.
	Connect RetryRule `json:"connect"`
	// Empty: retentativa de resposta vazia do SDK (concluída mas sem nenhum content block, só no formato openai). Padrão: 2 vezes, backoff exponencial.
	Empty RetryRule `json:"empty"`
	// Stream: retentativa na janela segura do mesmo provider (replay de stream interrompido antes de entregar saída). Padrão: 2 vezes, começando em 0,5s exponencial (teto de 4s).
	Stream RetryRule `json:"stream"`
	// Breaker: disjuntor de rodízio. Attempts = quantas falhas transitórias consecutivas disparam o disjuntor (padrão 3, -1 = falha transitória não abre o disjuntor,
	// falhas graves como saldo insuficiente / chave inválida ainda abrem o disjuntor imediatamente); IntervalMS = duração fixa do resfriamento (0 = gradiente padrão de 1/5/30min).
	Breaker RetryRule `json:"breaker"`
	// Intent: reexecução da intenção inteira depois que o worker termina com model_error. Padrão: 2 vezes, fixo em 3s.
	Intent RetryRule `json:"intent"`
}

// Clamped returns the policy with every rule clamped.
func (p LLMRetryPolicy) Clamped() LLMRetryPolicy {
	p.Connect, p.Empty, p.Stream = p.Connect.Clamped(), p.Empty.Clamped(), p.Stream.Clamped()
	p.Breaker, p.Intent = p.Breaker.Clamped(), p.Intent.Clamped()
	return p
}

// LLMRetryPolicy reads the global retry policy. A missing or unparseable value
// yields the zero policy — i.e. every layer on its built-in default.
func (d *DB) LLMRetryPolicy() LLMRetryPolicy {
	var p LLMRetryPolicy
	if d == nil {
		return p
	}
	raw, ok, err := d.GetSetting(settingLLMRetryPolicy)
	if err != nil || !ok || raw == "" {
		return p
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return LLMRetryPolicy{}
	}
	return p.Clamped()
}

// SetLLMRetryPolicy persists the global retry policy (values are clamped first).
func (d *DB) SetLLMRetryPolicy(p LLMRetryPolicy) error {
	raw, err := json.Marshal(p.Clamped())
	if err != nil {
		return err
	}
	return d.SetSetting(settingLLMRetryPolicy, string(raw))
}
