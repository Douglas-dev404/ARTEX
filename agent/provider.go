// Package agent wires real LLM-driven planner and work agents (on top of the
// agent-core SDK) to the dual SQLite graph. See docs/ARTEX-架构设计.md
// §4.3 (planner) and §4.4 (work agent).
//
// Provider configuration is read from the environment so the system runs with
// any Anthropic- or OpenAI-format endpoint. If no key is configured, FromEnv
// returns ok=false and the exploration engine stays idle (an LLM is required).
package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/Autumn-27/artex/llmrec"
	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/compaction"
	"github.com/Autumn-27/norma/llm"
	acperm "github.com/Autumn-27/norma/permission"
	"github.com/Autumn-27/norma/transcript"
)

// Config describes the LLM backend resolved from the environment.
type Config struct {
	Format  llm.Format
	BaseURL string
	APIKey  string
	Model   string
	// Proxy routes all LLM requests through the given proxy URL (http/https/socks5,
	// optionally with user:pass@ credentials). Empty means direct — it does NOT
	// fall back to the standard *_PROXY environment variables.
	Proxy string
	// RatePerSecond / RatePerMinute cap the shared request rate across ALL agents
	// using the provider (0 = that window unlimited).
	RatePerSecond float64
	RatePerMinute float64
	// ContextWindowK is the model's context window in K tokens (user-configured),
	// used to size compaction thresholds. 0 = default; see CompactionWindow.
	ContextWindowK int
	// ThinkingType controla de forma independente o campo "interruptor" de raciocínio (thinking.type):
	//   "" = não envia (padrão, compatível com modelos que não suportam o campo); "disabled" = desativa explicitamente;
	//   "enabled" = ativa. Totalmente desacoplado de ReasoningEffort — algumas interfaces não têm o campo thinking e
	//   ativam o raciocínio só pelo parâmetro de intensidade, por isso os dois podem ser definidos separadamente.
	ThinkingType string
	// ReasoningEffort controla de forma independente o campo de "intensidade" do raciocínio:
	//   "" = não envia (padrão); "low"/"medium"/"high"/"xhigh"/"max" = intensidade correspondente.
	//   Na OpenAI é mapeado para reasoning_effort de nível superior; na Anthropic, para output_config.effort.
	ReasoningEffort string
	// Stream controla se este profile usa a interface de streaming (SSE). true (padrão) = streaming; false = não
	// streaming de verdade (envia stream:false, obtém o JSON completo de uma vez, via Provider.Complete). O modo não streaming contorna
	// implementações ruins de SSE de certos gateways (frames vazios, perda de frames do campo de raciocínio), ao custo de perder o progresso em tempo real
	// e a contagem de tokens em tempo real durante a execução. Mapeado para agentcore.Options.NonStreaming = !Stream.
	Stream bool
	// MaxTokens é o limite de saída (token) de uma única resposta. 0 = não envia o campo, e o padrão do servidor decide
	// (comportamento histórico). Diferente de ContextWindowK: este é a capacidade total do modelo, usada só localmente para calcular o limiar de compressão,
	// e não aparece na requisição; já este valor é enviado a cada requisição. Mapeado para agentcore.Options.MaxTokens.
	MaxTokens int
	// MaxTokensField escolhe qual nome de campo da requisição o MaxTokens usa, válido apenas para format=openai:
	//   "" = max_tokens (padrão); "max_completion_tokens" = campo novo.
	// Os modelos de raciocínio da OpenAI (série o/GPT-5) só reconhecem o segundo e, ao receber max_tokens, retornam
	// unsupported_parameter; já a maioria dos gateways compatíveis só reconhece o primeiro, por isso não há inferência automática: o usuário escolhe conforme o endpoint.
	MaxTokensField string
	// SessionHeaderKey, quando não vazio, faz cada requisição LLM carregar um cabeçalho HTTP personalizado, cujo nome é este valor e
	// cujo valor é o [session id da sessão atual] (sessão de chat = conv-<id>, worker = exp<x>-worker-i<intent>
	// etc., ver WorkerSessionID). Usado em certos gateways que fazem cache de prompt/roteamento sticky pelo cabeçalho session-id.
	// Vazio = não envia. O valor é anexado ao context da requisição por transcript.WithSessionID e lido pelo RoundTripper
	// para preenchimento, de modo que o mesmo provider compartilhado também consegue emitir valores de cabeçalho diferentes por sessão.
	SessionHeaderKey string
	// Retry são os parâmetros de retentativa resolvidos desta configuração (override do profile → política global → padrão embutido, resolvidos
	// no lado do server). O significado das três camadas está em RetryConfig; valor zero = segue totalmente o padrão embutido.
	Retry RetryConfig
}

// RetryConfig são os parâmetros de retentativa que acompanham uma configuração LLM. Semântica uniforme do "número de tentativas" de cada camada:
// 0 = usa o número padrão embutido; negativo = desliga a retentativa dessa camada; >0 = usa esse valor. O "intervalo" de cada camada:
// 0 = usa o backoff exponencial original da camada; >0 = passa a usar este intervalo fixo.
type RetryConfig struct {
	// ConnectAttempts/ConnectInterval: retentativa de conexão do SDK (reset de conexão/timeout/429/5xx, antes do início do stream),
	// mapeado diretamente para llm.Config.MaxRetries / RetryInterval. Padrão: 3 vezes, exponencial a partir de 0.5s (teto de 8s).
	ConnectAttempts int
	ConnectInterval time.Duration
	// EmptyAttempts/EmptyInterval: retentativa de resposta vazia do SDK (concluída mas sem content block, apenas formato openai),
	// mapeado para llm.Config.EmptyResponseRetries / EmptyResponseInterval.
	// Padrão: 2 vezes, mesma escada exponencial.
	EmptyAttempts int
	EmptyInterval time.Duration
	// StreamAttempts/StreamInterval: retentativa na janela de segurança do mesmo provider — uma camada que este projeto
	// adicionou por cima do SDK, que só reenvia stream interrompido/sobrecarga/429 dentro do stream quando "nenhuma saída foi entregue ao chamador ainda". O SDK não a enxerga,
	// consumida por server/task_llm.go. Padrão: 2 vezes, exponencial a partir de 0.5s (teto de 4s).
	StreamAttempts int
	StreamInterval time.Duration
}

// compaction window resolution bounds (in K tokens). Below the floor the
// threshold math (window − summary reserve − buffer) would go non-positive and
// compaction would fire every turn; above the cap it would never fire.
const (
	defaultWindowK = 200  // unset → assume a 200K window (Claude default)
	minWindowK     = 32   // floor so effectiveWindow stays comfortably positive
	maxWindowK     = 1000 // cap at 1M tokens (user request)
)

// CompactionWindow returns the model context window in TOKENS for compaction
// thresholds, resolved from the user-configured size (ContextWindowK). 0/unset →
// a 200K default; otherwise clamped to [32K, 1M] so compaction stays effective.
func (c Config) CompactionWindow() int {
	k := c.ContextWindowK
	if k <= 0 {
		k = defaultWindowK
	}
	if k < minWindowK {
		k = minWindowK
	}
	if k > maxWindowK {
		k = maxWindowK
	}
	return k * 1000
}

// compactionConfig builds the agent-core compaction config for a context window
// in tokens. agentcore.NewSession wires the summarizer (same provider) when this
// is set on Options.Compaction.
func compactionConfig(windowTokens int) *compaction.Config {
	if windowTokens <= 0 {
		windowTokens = defaultWindowK * 1000
	}
	return &compaction.Config{ContextWindow: windowTokens}
}

// FromEnv resolves the LLM provider config:
//
//	ARTEX_LLM_PROVIDER = anthropic|openai (default: inferred from keys)
//	ARTEX_LLM_MODEL    = model id        (default: per provider)
//	ARTEX_LLM_BASE_URL = endpoint        (optional)
//	ARTEX_LLM_PROXY    = proxy URL        (optional; http/https/socks5)
//	ANTHROPIC_API_KEY / OPENAI_API_KEY         = credentials
func FromEnv() (Config, bool) {
	prov := os.Getenv("ARTEX_LLM_PROVIDER")
	anthKey := os.Getenv("ANTHROPIC_API_KEY")
	oaiKey := os.Getenv("OPENAI_API_KEY")

	if prov == "" {
		switch {
		case anthKey != "":
			prov = "anthropic"
		case oaiKey != "":
			prov = "openai"
		default:
			return Config{}, false
		}
	}

	c := Config{
		BaseURL: os.Getenv("ARTEX_LLM_BASE_URL"),
		Model:   os.Getenv("ARTEX_LLM_MODEL"),
		Proxy:   strings.TrimSpace(os.Getenv("ARTEX_LLM_PROXY")),
		// Padrão streaming; ARTEX_LLM_STREAM=false/0/off desliga explicitamente e usa não streaming.
		Stream: !isFalsy(os.Getenv("ARTEX_LLM_STREAM")),
	}
	switch prov {
	case "openai":
		c.Format = llm.FormatOpenAI
		c.APIKey = oaiKey
		if c.Model == "" {
			c.Model = "gpt-4o"
		}
	case "openai-responses":
		c.Format = llm.FormatOpenAIResponses
		c.APIKey = oaiKey
		if c.Model == "" {
			c.Model = "gpt-5"
		}
	default:
		c.Format = llm.FormatAnthropic
		c.APIKey = anthKey
		if c.Model == "" {
			c.Model = "claude-opus-4-8"
		}
	}
	if c.APIKey == "" {
		return Config{}, false
	}
	return c, true
}

// ConfigFrom builds a Config from UI-provided strings (provider defaults to
// anthropic; model defaults per provider). Inputs are trimmed and the base URL
// is normalized to the API base the provider expects (the provider appends the
// endpoint path itself), so a full endpoint URL is tolerated.
func ConfigFrom(provider, model, baseURL, apiKey, proxy string) Config {
	c := Config{
		Model:   strings.TrimSpace(model),
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:  strings.TrimSpace(apiKey),
		Proxy:   strings.TrimSpace(proxy),
		Stream:  true, // padrão streaming; o chamador sobrescreve por profile
	}
	switch strings.TrimSpace(provider) {
	case "openai":
		c.Format = llm.FormatOpenAI
		// provider appends "/chat/completions"; tolerate a full endpoint URL.
		c.BaseURL = strings.TrimRight(strings.TrimSuffix(c.BaseURL, "/chat/completions"), "/")
		if c.Model == "" {
			c.Model = "gpt-4o"
		}
	case "openai-responses":
		c.Format = llm.FormatOpenAIResponses
		// provider appends "/responses"; tolerate a full endpoint URL.
		c.BaseURL = strings.TrimRight(strings.TrimSuffix(c.BaseURL, "/responses"), "/")
		if c.Model == "" {
			c.Model = "gpt-5"
		}
	default:
		c.Format = llm.FormatAnthropic
		// provider appends "/v1/messages".
		c.BaseURL = strings.TrimRight(strings.TrimSuffix(c.BaseURL, "/v1/messages"), "/")
		if c.Model == "" {
			c.Model = "claude-opus-4-8"
		}
	}
	return c
}

// isFalsy reports whether an env-var string explicitly requests "off". Empty or
// unrecognized → false (so an unset var keeps the streaming default).
func isFalsy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "0", "false", "off", "no":
		return true
	}
	return false
}

// Provider returns the short provider name ("anthropic"/"openai").
func (c Config) Provider() string {
	switch c.Format {
	case llm.FormatOpenAI:
		return "openai"
	case llm.FormatOpenAIResponses:
		return "openai-responses"
	}
	return "anthropic"
}

// NewProvider builds an llm.Provider from the config. When a rate is set, the
// limiter lives on the single provider instance — so planner + all workers +
// main agent (which share this provider) are bounded by one shared rate limit.
func (c Config) NewProvider() (llm.Provider, error) {
	client, err := quotaAwareHTTPClient(c.Proxy, c.SessionHeaderKey)
	if err != nil {
		return nil, err
	}
	lc := llm.Config{
		Format:     c.Format,
		BaseURL:    c.BaseURL,
		APIKey:     c.APIKey,
		Model:      c.Model,
		HTTPClient: client,
	}
	// Os dois campos, interruptor e intensidade do raciocínio, são repassados cada um por conta própria (vazio = o campo não é enviado). Os dois são desacoplados:
	// pode-se enviar só thinking.type, só effort, ambos ou nenhum.
	lc.ThinkingType = c.ThinkingType
	lc.ReasoningEffort = c.ReasoningEffort
	// Escolha do nome do campo do limite de saída (vazio = usa max_tokens). O "valor" do limite não está aqui: ele segue a cada turno em
	// agentcore.Options.MaxTokens, e o provider apenas decide em qual chave colocá-lo.
	lc.MaxTokensField = c.MaxTokensField
	// Os parâmetros de retentativa têm a mesma semântica do SDK (número de tentativas 0=padrão/negativo=desligar, intervalo 0=backoff exponencial/>0=fixo), repassados sem alteração.
	lc.MaxRetries = c.Retry.ConnectAttempts
	lc.RetryInterval = c.Retry.ConnectInterval
	lc.EmptyResponseRetries = c.Retry.EmptyAttempts
	lc.EmptyResponseInterval = c.Retry.EmptyInterval
	if c.RatePerSecond > 0 || c.RatePerMinute > 0 {
		lc.RateLimit = &llm.RateLimit{PerSecond: c.RatePerSecond, PerMinute: c.RatePerMinute}
	}
	return llm.NewProvider(lc)
}

// IsQuotaExhaustedMessage deliberately recognizes only explicit balance,
// billing, credit, or quota-exhaustion signals. Generic 429/rate-limit text,
// authentication failures, network errors, and server failures are excluded.
var nonFailoverHTTPStatus = regexp.MustCompile(`(?:status(?:\s+code)?|http(?:\s+status)?)\s*[=:]?\s*(?:401|403|5\d\d)\b`)
var transientQuotaLimit = regexp.MustCompile(`(?i)(?:\b(?:rpm|tpm|rpd|qps)\b|quota[_\s-]*metric|rate[_\s-]*limit|too many requests|(?:requests?|tokens?)\s+(?:per|/)\s*(?:second|minute)|(?:per|/)\s*(?:second|minute)\s+(?:requests?|tokens?)|generate[_\s-]*requests[_\s-]*per[_\s-]*(?:minute|second)|tokens?[_\s-]*per[_\s-]*(?:minute|second))`)

func IsQuotaExhaustedMessage(message string) bool {
	message = strings.ToLower(message)
	// Authentication/authorization and provider-side 5xx failures never rotate,
	// even when a gateway happens to echo a quota-looking phrase in the body.
	if nonFailoverHTTPStatus.MatchString(message) {
		return false
	}
	// Provider APIs frequently describe an ordinary rate limit as "quota
	// exceeded", especially Google-style responses containing a quota metric.
	// These limits recover with time and must stay on the current provider.
	if transientQuotaLimit.MatchString(message) {
		return false
	}
	markers := []string{
		"insufficient_quota", "quota_exceeded", "quota exceeded", "quota exhausted",
		"exceeded your current quota", "billing_hard_limit_reached",
		"billing hard limit", "billing_not_active", "credit balance", "insufficient credit",
		"insufficient balance", "balance is too low", "payment required", "status 402",
		"余额不足", "额度不足", "额度已用尽", "欠费",
	}
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	// gRPC RESOURCE_EXHAUSTED is overloaded for both account quota and ordinary
	// request-rate limiting. Preserve it as an explicit exhaustion signal only
	// when the same error does not identify a transient rate limit.
	return strings.Contains(message, "resource_exhausted") &&
		!strings.Contains(message, "rate limit") &&
		!strings.Contains(message, "too many requests")
}

// quotaAwareTransport preserves Norma's normal retry behavior except for a 429
// whose body explicitly says the account quota/balance is exhausted. Norma's
// retry loop treats every 429 as transient; normalizing only that response to
// 402 lets a task router fail over immediately while retaining the original
// response body for provider-specific classification and audit logs.
type quotaAwareTransport struct {
	base http.RoundTripper
	// sessionHeaderKey, when non-empty, is the HTTP header name each request
	// carries; its value is the session id read from the request context. Empty
	// disables it. See Config.SessionHeaderKey.
	sessionHeaderKey string
}

func (t quotaAwareTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Custom session-id header: name is user-configured, value is THIS run's
	// session id (norma stashes it on the context via transcript.WithSessionID).
	// Stable across a session's turns and distinct across sessions — exactly what
	// a session-keyed prompt cache wants. Skipped when no session id is present.
	if t.sessionHeaderKey != "" {
		if sid := transcript.SessionIDFrom(req.Context()); sid != "" {
			req.Header.Set(t.sessionHeaderKey, sid)
		}
	}
	// When LLM recording is on, the Recorder puts a Capture on the context so the
	// raw wire bodies can be persisted. This is the only layer that still sees
	// them: norma builds the request body internally and decodes the SSE response
	// before either reaches the recorder.
	capt := llmrec.CaptureFrom(req.Context())
	capt.SetRequest(requestBodySnapshot(req))

	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	// Tee rather than read: a 200 is an SSE stream that must keep streaming. The
	// 429 branch below reads through this wrapper, so its body lands in the
	// capture before being replaced.
	resp.Body = capt.TeeResponse(resp.StatusCode, resp.Body)

	if resp.StatusCode != http.StatusTooManyRequests {
		return resp, nil
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	if readErr != nil {
		return resp, nil
	}
	if IsQuotaExhaustedMessage(string(body)) {
		resp.StatusCode = http.StatusPaymentRequired
		resp.Status = "402 Payment Required"
	}
	return resp, nil
}

// requestBodySnapshot copies an outgoing request body without consuming it.
// norma builds every model request from a *bytes.Reader, so net/http populates
// GetBody and the copy has no effect on what gets sent.
func requestBodySnapshot(req *http.Request) string {
	if req.GetBody == nil {
		return ""
	}
	rc, err := req.GetBody()
	if err != nil {
		return ""
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		return ""
	}
	return string(b)
}

func quotaAwareHTTPClient(proxy, sessionHeaderKey string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	proxy = strings.TrimSpace(proxy)
	if proxy == "" {
		transport.Proxy = nil // vazio = conexão direta, sem recorrer às variáveis de ambiente HTTP_PROXY/HTTPS_PROXY
	} else {
		proxyURL, err := url.Parse(proxy)
		if err != nil {
			return nil, fmt.Errorf("llm: invalid proxy %q: %w", proxy, err)
		}
		switch proxyURL.Scheme {
		case "http", "https", "socks5":
		case "":
			return nil, fmt.Errorf("llm: proxy %q missing scheme (use http://, https:// or socks5://)", proxy)
		default:
			return nil, fmt.Errorf("llm: unsupported proxy scheme %q (use http, https or socks5)", proxyURL.Scheme)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}
	return &http.Client{Transport: quotaAwareTransport{base: transport, sessionHeaderKey: strings.TrimSpace(sessionHeaderKey)}}, nil
}

// logTestConnection prints the raw HTTP status code(s) and response body of a
// connection test to the server log, so "clique em testar" leaves a diagnosable trail of
// exactly what the gateway returned — 401 bodies, quota text, empty frames — not
// just the collapsed ok/err the UI shows. Bodies are clipped to keep a chatty
// SSE stream from flooding the log.
func logTestConnection(c Config, capt *llmrec.Capture) {
	attempts := capt.Attempts()
	if len(attempts) == 0 {
		log.Printf("[llm-test] %s / %s @ %s — nenhuma requisição HTTP foi enviada (falha na resolução da configuração ou ao estabelecer a conexão)",
			c.Provider(), c.Model, c.BaseURL)
		return
	}
	for i, a := range attempts {
		log.Printf("[llm-test] %s / %s @ %s — tentativa %d/%d HTTP %d\nCorpo da resposta: %s",
			c.Provider(), c.Model, c.BaseURL, i+1, len(attempts), a.Status, clipBody(a.Body))
	}
}

// clipBody trims a wire body for logging. 4K is plenty to show an error JSON or
// the head of an SSE stream while bounding a runaway response.
func clipBody(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(vazio)"
	}
	const max = 4096
	if len(s) > max {
		return s[:max] + fmt.Sprintf("…(truncado, %d bytes no total)", len(s))
	}
	return s
}

// TestConnection makes a minimal real completion to verify the provider/model/
// endpoint/key actually work. Returns the round-trip latency and the model's
// reply text.
func TestConnection(ctx context.Context, c Config) (time.Duration, string, error) {
	prov, err := c.NewProvider()
	if err != nil {
		return 0, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Captura a mensagem wire bruta: o que o teste de conexão mais precisa ver é o que o gateway realmente respondeu (código de status + corpo da resposta),
	// e depois que o norma decodifica a resposta em StreamEvent isso se perde. O quotaAwareTransport encontra este Capture
	// no context e preenche o código de status e o body de cada tentativa HTTP.
	ctx, capt := llmrec.NewCapture(ctx)
	defer logTestConnection(c, capt)
	// O teste de conexão é um caminho de disparo único, que não passa pelo loop de sessão do agentcore, portanto ninguém anexa
	// o session id ao context. Para endpoints com SessionHeaderKey configurado (como o opencode zen, que exige o cabeçalho
	// x-opencode-session e devolve 400 MissingSessionID direto na falta dele), isso causaria a discrepância "a conversa funciona,
	// mas o clique em testar dá 400". Aqui se anexa um session id aleatório descartável, para que o teste e a conversa real usem a mesma
	// lógica de cabeçalho; endpoints sem SessionHeaderKey não o leem, sem efeito colateral.
	ctx = transcript.WithSessionID(ctx, "conntest-"+transcript.NewSessionID())
	start := time.Now()
	// MaxTokens precisa ser generoso: modelos de raciocínio (como deepseek-v4-pro) produzem antes uma longa passagem de
	// raciocínio antes de dar a resposta (medido: até para um simples "ping" pode queimar ~2900 token). Se só se der 32, o modelo fica preso na "fase de raciocínio"
	// e bate no limite de saída (finish=length), sendo truncado; o teste de conexão ainda conta como aprovado (err=nil), mas é exibido como
	// uma bagunça de "interrompido/length/resume". Dê orçamento suficiente para que ele emita o OK por inteiro (finish=stop).
	// EscalateMaxTokens permanece false: não eleva o limite para retentar após truncamento, evitando um loop de resume queimando recursos à toa.
	reply, err := agentcore.Run(ctx, agentcore.Options{
		Provider:       prov,
		SystemPrompt:   []string{"Você é um teste de conexão. Basta emitir os dois caracteres OK, sem pensar, sem explicar, sem mais nada."},
		PermissionMode: acperm.ModeBypass,
		MaxTurns:       1,
		MaxTokens:      8192,
		NonStreaming:   !c.Stream, // usa o modo real de envio/recebimento deste profile para o teste de conexão
	}, "ping")
	lat := time.Since(start)
	if err != nil {
		return lat, "", err
	}
	// err==nil não basta: existe de fato o caso em que a requisição passa mas o modelo não emite uma única palavra (o raciocínio queima o orçamento,
	// o corpo é engolido por política de segurança, a camada de compatibilidade perde o content). Uma configuração assim, na sessão, é "sem resposta",
	// mas o teste reporta sucesso — exatamente a discrepância que este item quer eliminar. Sem corpo visível, sempre se considera falha.
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return lat, "", fmt.Errorf("modelo sem conteúdo de resposta (a requisição passou, mas nenhum texto foi retornado)")
	}
	return lat, reply, nil
}
