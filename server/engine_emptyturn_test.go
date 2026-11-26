package server

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/Autumn-27/norma/llm"
)

// Identificação e continuação de turnos ociosos (só raciocínio, sem texto nem ferramenta); ver steerHooks.Stop.

func assistantThinking(text string) llm.Message {
	return llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		{Type: llm.BlockThinking, Thinking: text, Signature: "sig"},
	}}
}

func TestIsThinkingOnlyTurn(t *testing.T) {
	toolUse := llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
		{Type: llm.BlockThinking, Thinking: "varrer as portas primeiro"},
		{Type: llm.BlockToolUse, ID: "t1", Name: "run_nuclei"},
	}}
	cases := []struct {
		name string
		msgs []llm.Message
		want bool
	}{
		{"só raciocínio", []llm.Message{llm.UserText("iniciar"), assistantThinking("pensando")}, true},
		{"raciocínio+ferramenta", []llm.Message{llm.UserText("iniciar"), toolUse}, false},
		{"raciocínio+texto", []llm.Message{assistantThinking("pensando"), {
			Role:    llm.RoleAssistant,
			Content: []llm.ContentBlock{{Type: llm.BlockThinking, Thinking: "x"}, llm.TextBlock("conclusão")},
		}}, false},
		{"texto só com espaços em branco", []llm.Message{{
			Role:    llm.RoleAssistant,
			Content: []llm.ContentBlock{{Type: llm.BlockThinking, Thinking: "x"}, llm.TextBlock("  \n ")},
		}}, true},
		{"turno de assistant totalmente vazio", []llm.Message{{Role: llm.RoleAssistant}}, true},
		// O resultado de ferramenta tem papel user; o julgamento precisa voltar até o assistant anterior a ele, e não julgar errado pelo mais próximo.
		{"a última é resultado de ferramenta", []llm.Message{toolUse, {
			Role:    llm.RoleUser,
			Content: []llm.ContentBlock{{Type: llm.BlockToolResult, ToolUseID: "t1"}},
		}}, false},
		{"sem mensagem de assistant", []llm.Message{llm.UserText("iniciar")}, false},
		{"histórico vazio", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isThinkingOnlyTurn(c.msgs); got != c.want {
				t.Fatalf("isThinkingOnlyTurn = %v, want %v", got, c.want)
			}
		})
	}
}

// fakeHooks é um inner HookRunner programável, usado para verificar que steerHooks respeita as decisões do inner.
type fakeHooks struct {
	prevent  bool
	blocking []string
	msg      string
}

func (f fakeHooks) PreToolUse(context.Context, string, []byte) (bool, string, []byte) {
	return false, "", nil
}
func (f fakeHooks) PostToolUse(context.Context, string, []byte, []byte, bool) {}
func (f fakeHooks) Stop(context.Context, []llm.Message) (bool, []string, string) {
	return f.prevent, f.blocking, f.msg
}

func TestSteerHooksStopNudgesEmptyTurn(t *testing.T) {
	empty := []llm.Message{assistantThinking("devo primeiro enumerar subdomínios")}

	t.Run("turno ocioso injeta instrução de continuação", func(t *testing.T) {
		h := steerHooks{nudges: &atomic.Int64{}, limit: defaultEmptyTurnNudges, label: "worker-1 · #1"}
		prevent, blocking, _ := h.Stop(context.Background(), empty)
		if prevent {
			t.Fatal("turno ocioso não deve parar de vez")
		}
		if len(blocking) != 1 || blocking[0] != emptyTurnNudge {
			t.Fatalf("blocking = %v, want [emptyTurnNudge]", blocking)
		}
	})

	t.Run("não intervém quando há texto ou ferramenta", func(t *testing.T) {
		h := steerHooks{nudges: &atomic.Int64{}, limit: defaultEmptyTurnNudges}
		normal := []llm.Message{{
			Role:    llm.RoleAssistant,
			Content: []llm.ContentBlock{llm.TextBlock("varredura concluída, nenhuma porta aberta encontrada")},
		}}
		if _, blocking, _ := h.Stop(context.Background(), normal); blocking != nil {
			t.Fatalf("encerramento normal foi julgado erroneamente como ocioso: %v", blocking)
		}
		if n := h.nudges.Load(); n != 0 {
			t.Fatalf("não deve contar quando não intervém, got %d", n)
		}
	})

	t.Run("libera o encerramento após atingir o teto", func(t *testing.T) {
		const limit = 5 // o usuário configurou o "nº de retries de resposta vazia" como 5
		h := steerHooks{nudges: &atomic.Int64{}, limit: limit}
		for i := 1; i <= limit; i++ {
			if _, blocking, _ := h.Stop(context.Background(), empty); len(blocking) != 1 {
				t.Fatalf("a %d-ésima vez ainda deveria estar dentro da cota, blocking = %v", i, blocking)
			}
		}
		if _, blocking, _ := h.Stop(context.Background(), empty); blocking != nil {
			t.Fatalf("continua injetando acima do teto: %v", blocking)
		}
	})

	// "nº de retries de resposta vazia" = -1 → desliga essa camada, emptyTurnNudgeLimit resolve para 0.
	t.Run("não intervém com a configuração desligada", func(t *testing.T) {
		h := steerHooks{nudges: &atomic.Int64{}, limit: 0}
		if _, blocking, _ := h.Stop(context.Background(), empty); blocking != nil {
			t.Fatalf("continua injetando mesmo desligado: %v", blocking)
		}
	})

	t.Run("não sobrepõe quando o inner decide parar de vez", func(t *testing.T) {
		h := steerHooks{inner: fakeHooks{prevent: true, msg: "guard recusa o encerramento"}, nudges: &atomic.Int64{}, limit: defaultEmptyTurnNudges}
		prevent, blocking, msg := h.Stop(context.Background(), empty)
		if !prevent || msg != "guard recusa o encerramento" || blocking != nil {
			t.Fatalf("a parada dura do inner foi reescrita: prevent=%v blocking=%v msg=%q", prevent, blocking, msg)
		}
		if n := h.nudges.Load(); n != 0 {
			t.Fatalf("ao ceder ao inner não deve consumir cota, got %d", n)
		}
	})

	t.Run("não sobrepõe quando o inner já quer continuar", func(t *testing.T) {
		h := steerHooks{inner: fakeHooks{blocking: []string{"motivo de continuação do guard"}}, nudges: &atomic.Int64{}, limit: defaultEmptyTurnNudges}
		_, blocking, _ := h.Stop(context.Background(), empty)
		if len(blocking) != 1 || blocking[0] != "motivo de continuação do guard" {
			t.Fatalf("a mensagem de continuação do inner foi reescrita: %v", blocking)
		}
	})

	t.Run("comportamento inalterado sem contador instalado", func(t *testing.T) {
		h := steerHooks{limit: defaultEmptyTurnNudges} // por exemplo, um futuro ponto de chamada esqueceu de passar nudges
		if _, blocking, _ := h.Stop(context.Background(), empty); blocking != nil {
			t.Fatalf("sem contador não deve injetar: %v", blocking)
		}
	})
}
