package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Autumn-27/norma/harness"
)

// runTrace retains the latest tool call so an interrupted run can identify the
// operation that was still in flight.
type runTrace struct {
	startedAt time.Time
	id        string
	name      string
	input     string
	at        time.Time
	pending   bool
}

func (t *runTrace) start(id, name, input string) {
	t.id, t.name, t.input, t.at, t.pending = id, name, input, time.Now(), true
}

func (t *runTrace) done(id string) {
	if id == t.id {
		t.pending = false
	}
}

var reasonHint = map[harness.TerminalReason]string{
	harness.ReasonCompleted:         "O modelo encerrou normalmente esta rodada, mas não deixou resumo em texto; fatos e ativos valem conforme o registro de chamadas de ferramenta desta rodada",
	harness.ReasonMaxTurns:          "Limite de passos atingido (MaxTurns): o SDK executou o encerramento e gravou fatos e ativos; a intenção é marcada como exhausted para que o planejador mude de direção e continue, em vez de tratá-la como falha",
	harness.ReasonTimeout:           "Orçamento de tempo de relógio de uma única execução atingido (MaxDuration): no prazo, as ferramentas em execução são interrompidas e entra-se no encerramento ali mesmo, gravando os fatos e ativos já identificados; a intenção é marcada como exhausted",
	harness.ReasonModelError:        "Falha na chamada ao modelo ou à API (rede, autenticação, limite de taxa, 5xx do provedor etc.); esgotadas as tentativas, a intenção é marcada como blocked — uma falha na camada de transporte faz com que a intenção praticamente não tenha sido de fato explorada; consulte o processo de execução (get_worker_trace) antes de decidir reenviar ou mudar a abordagem",
	harness.ReasonBlockingLimit:     "O comprimento do contexto atingiu o limite rígido e a requisição foi bloqueada antes de ser enviada; reduza a granularidade da intenção ou comprima o retorno das ferramentas",
	harness.ReasonPromptTooLong:     "O prompt é longo demais e as tentativas de compressão de contexto se esgotaram, impossibilitando continuar a execução",
	harness.ReasonImageError:        "O modelo atual não suporta o conteúdo multimodal desta rodada; troque para um modelo com visão ou evite que ferramentas retornem imagens",
	harness.ReasonStopHookPrevented: "O hook Stop impediu o término desta rodada e depois não foi possível continuar; verifique se as regras de Guard da tarefa não estão rígidas demais",
	harness.ReasonHookStopped:       "Uma ferramenta ou hook interrompeu ativamente a continuação, por exemplo alvo fora do escopo ou comando proibido; verifique a explicação de bloqueio no último tool_result",
	harness.ReasonAbortedStreaming:  "A execução foi cancelada durante a geração em streaming da saída do modelo",
	harness.ReasonAbortedTools:      "A execução foi cancelada durante a execução de ferramentas",
}

// terminalText renders a terminal event with no final text into a compact summary
// and a Markdown detail block.
func terminalText(ctx context.Context, term *harness.Terminal, tr *runTrace) (string, string) {
	reason := term.Reason
	aborted := reason == harness.ReasonAbortedStreaming || reason == harness.ReasonAbortedTools
	// Prompt may return ctx.Err directly without a terminal event. Preserve the
	// cancellation cause instead of falling back to an empty/unknown terminal reason.
	if reason == "" && ctx.Err() != nil {
		aborted = true
	}

	var sum string
	if aborted {
		_, short, _, ok := AbortReason(ctx)
		if !ok {
			short = "Não foi possível obter a causa do cancelamento"
		}
		stage := "durante a execução"
		switch reason {
		case harness.ReasonAbortedStreaming:
			stage = "durante a saída do modelo"
		case harness.ReasonAbortedTools:
			stage = "durante a execução de ferramentas"
		}
		sum = "(Execução interrompida: " + short + "; parou " + stage + progressSuffix(term, tr) + ", não concluída)"
	} else if reason == harness.ReasonMaxTurns || reason == harness.ReasonTimeout {
		sum = "(Limite do orçamento de execução atingido (" + string(reason) + "), encerrado com fatos gravados" + progressSuffix(term, tr) + "; esta execução não tem resumo em texto)"
	} else {
		hint := terminalReasonHint(reason)
		sum = "(Sem resumo em texto, estado terminal " + terminalReasonLabel(reason) + ": " + firstLine(hint, 80) + ")"
	}

	var b strings.Builder
	b.WriteString(sum)
	b.WriteString("\n\n")
	displayReason := terminalReasonLabel(reason)
	fmt.Fprintf(&b, "- **Estado terminal**: `%s` - %s\n", displayReason, terminalReasonHint(reason))
	if aborted {
		code, _, why, ok := AbortReason(ctx)
		if ok {
			fmt.Fprintf(&b, "- **Causa da interrupção** (`%s`): %s\n", code, why)
		} else {
			b.WriteString("- **Causa da interrupção**: não foi possível obtê-la; quem cancelou pode não ter anexado uma causa nomeada via context.WithCancelCause\n")
		}
	}
	if term.Err != nil {
		fmt.Fprintf(&b, "- **Erro subjacente**: `%v`\n", term.Err)
	}
	if aborted && strings.TrimSpace(term.Text) != "" {
		b.WriteString("- **Saída parcial gerada antes do cancelamento**:\n\n")
		b.WriteString(term.Text)
		b.WriteString("\n\n")
	}
	if term.Turns > 0 {
		fmt.Fprintf(&b, "- **Executado**: %d turnos de modelo\n", term.Turns)
	}
	if !tr.startedAt.IsZero() {
		fmt.Fprintf(&b, "- **Duração desta execução**: %s\n", roundDur(time.Since(tr.startedAt)))
	}
	if u := term.Usage; u.InputTokens+u.OutputTokens+u.CacheReadTokens+u.CacheWriteTokens > 0 {
		fmt.Fprintf(&b, "- **Tokens acumulados**: entrada %d / saída %d / leitura de cache %d / escrita de cache %d\n",
			u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens)
	}
	if tr.name == "" {
		b.WriteString("- **Chamadas de ferramenta**: esta execução terminou sem ter emitido nenhuma chamada de ferramenta\n")
	} else if tr.pending {
		fmt.Fprintf(&b, "- **Ferramenta em execução no momento da interrupção**: `%s` (em execução há %s, **sem resultado retornado**)\n\n  ```json\n  %s\n  ```\n",
			tr.name, roundDur(time.Since(tr.at)), firstLine(tr.input, 300))
	} else {
		fmt.Fprintf(&b, "- **Última ferramenta antes da interrupção**: `%s` (retornou normalmente)\n", tr.name)
	}
	return sum, b.String()
}

func terminalReasonLabel(reason harness.TerminalReason) string {
	if reason == "" {
		return "context_canceled"
	}
	return string(reason)
}

func terminalReasonHint(reason harness.TerminalReason) string {
	if hint := reasonHint[reason]; hint != "" {
		return hint
	}
	if reason == "" {
		return "O context da execução foi cancelado, mas a camada subjacente não produziu evento Terminal"
	}
	return "Estado terminal desconhecido; o harness pode ter adicionado um novo TerminalReason, complemente reasonHint"
}

func progressSuffix(term *harness.Terminal, tr *runTrace) string {
	var parts []string
	if term.Turns > 0 {
		parts = append(parts, fmt.Sprintf("%d turnos", term.Turns))
	}
	if !tr.startedAt.IsZero() {
		parts = append(parts, roundDur(time.Since(tr.startedAt)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "; em execução há " + strings.Join(parts, " / ")
}

func roundDur(d time.Duration) string {
	switch {
	case d < time.Minute:
		return d.Round(100 * time.Millisecond).String()
	case d < time.Hour:
		return d.Round(time.Second).String()
	default:
		return d.Round(time.Minute).String()
	}
}
