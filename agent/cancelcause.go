package agent

import (
	"context"
	"errors"
	"fmt"
)

// AbortCause names why an agent run's context was cancelled. Every cancellation
// site should attach one so the activity trace can report the real initiator.
type AbortCause struct {
	Code  string
	Short string
	Text  string
}

func (c *AbortCause) Error() string { return c.Text }

func cause(code, short, text string) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: text}
}

// Causef builds a cause that includes runtime-specific detail.
func Causef(code, short, format string, args ...any) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: fmt.Sprintf(format, args...)}
}

var (
	// Task-level execution context.
	AbortPausedByUser = cause("paused_by_user", "O usuário pausou a tarefa",
		"O usuário pausou a tarefa pela interface de controle de tarefas (POST /api/tasks/{id}/control, action=pause). Esta execução do Planner/Worker foi cancelada ativamente; as intenções em execução voltam ao frontier(open) e, ao retomar a tarefa, são reivindicadas novamente e executadas desde o início")
	AbortPausedByOrchestrator = cause("paused_by_orchestrator", "O Agent orquestrador pausou a tarefa",
		"O Agent orquestrador chamou a ferramenta pause_task para pausar esta tarefa. Esta execução do Planner/Worker foi cancelada ativamente; as intenções em execução voltam ao frontier(open) e são reexecutadas após a retomada")
	AbortTaskDeleted = cause("task_deleted", "A tarefa foi excluída",
		"A tarefa está sendo excluída (DELETE /api/tasks/{id}); a barreira de exclusão cancelou o Planner, os Workers e o Agent principal que estavam em execução nesta tarefa; o resultado desta execução não será mais utilizado")
	AbortPausedOnReload = cause("paused_on_reload", "Pausa restaurada ao iniciar o backend",
		"Ao iniciar, o backend restaurou a pausa da tarefa com base no estado persistido no banco de dados. Esta execução foi cancelada; normalmente não há Agent em execução na fase de restauração")
	AbortGoalMet = cause("goal_met", "O planejador deu o objetivo por atingido",
		"O planejador julgou que o objetivo da tarefa foi atingido e marcou a tarefa como done, cancelando em seguida os Workers ainda em execução; essas intenções são marcadas como stopped, e não como falha")
	AbortSettleDrainTimeout = cause("settle_drain_timeout", "Drain do timeout da tarefa esgotado",
		"Ao atingir o timeout, a tarefa aguardou os Workers em execução encerrarem de forma graciosa, mas a tolerância de drain de 90 segundos não foi suficiente, então foi feito o cancelamento forçado; as intenções são marcadas como exhausted, e os fatos e ativos já gravados na fase de encerramento são preservados")

	// Per-work context.
	AbortKilledByPlanner = cause("killed_by_planner", "O planejador encerrou esta intenção",
		"O planejador chamou kill_work para encerrar esta intenção ativamente, o que geralmente indica que a direção se desviou ou que não há mais valor em continuar; a intenção é marcada como stopped e não será reivindicada automaticamente de novo")
	AbortWorkPausedByUser = cause("work_paused_by_user", "O usuário pausou esta intenção do Worker",
		"O usuário pausou o Worker em execução. Esta chamada foi cancelada e a intenção passa para paused; as intenções, fatos, vulnerabilidades e registros de atividade já gravados são todos preservados, e após a retomada a execução recomeça do início")
	AbortWorkCancelledByUser = cause("work_cancelled_by_user", "O usuário excluiu esta intenção",
		"O usuário excluiu o Worker em execução. Esta chamada foi cancelada; depois que o Worker sai da zona de escrita, o servidor trata a intenção conforme o modo de exclusão escolhido pelo usuário: a exclusão lógica apenas marca como excluída e preserva toda a produção, enquanto a exclusão definitiva remove em cascata a intenção e os nós downstream sustentados somente por ela")
	AbortWorkFinished = cause("work_finished", "Worker concluído e context liberado",
		"O Worker terminou normalmente e o motor liberou seus recursos de context em detachWork. Isto não é uma interrupção da execução; se aparecer numa mensagem de interrupção, significa que houve uma condição de corrida entre os eventos de cancelamento e de encerramento")
	AbortPausedRaceGuard = cause("paused_race_guard", "Nova execução recusada durante a pausa",
		"Com a tarefa em estado de pausa, o motor se recusa a emitir novos context de execução, para evitar que uma condição de corrida entre claim e pausa faça um Worker continuar iniciando; as intenções já reivindicadas voltam ao frontier")

	// Main Agent and standalone conversation contexts.
	AbortChatStoppedByUser = cause("chat_stopped_by_user", "O usuário parou esta rodada da conversa",
		"O usuário clicou em parar, abortando ativamente a execução do Agent principal ou do Agent de sessão nesta rodada. Os registros de atividade já gerados são preservados e é possível enviar a próxima mensagem")
	AbortChatPausedWithTask = cause("chat_paused_with_task", "Conversa abortada ao pausar a tarefa",
		"Quando o usuário pausou a tarefa, a conversa do Agent principal em execução também foi cancelada de forma sincronizada. Os registros de atividade já gerados são preservados; após a retomada da tarefa, a mensagem desta rodada não é reexecutada automaticamente")
	AbortChatTurnFinished = cause("chat_turn_finished", "Rodada concluída e context liberado",
		"Esta rodada da conversa terminou normalmente e o servidor está liberando os recursos de context da rodada. Isto não é uma interrupção da execução; se aparecer numa mensagem de interrupção, significa que houve uma condição de corrida entre os eventos de cancelamento e de encerramento")

	// Process-level and per-run hard backstop.
	AbortShutdown = cause("shutdown", "O backend está sendo encerrado",
		"O processo do backend recebeu SIGINT ou SIGTERM e está reiniciando, atualizando ou encerrando. Todos os Agents em execução serão cancelados; após a reinicialização, as intenções running remanescentes são redefinidas para open e reexecutadas")
	AbortRunHardTimeout = cause("run_hard_timeout", "Limite rígido de timeout da execução",
		"Uma única execução excedeu o orçamento de tempo de relógio flexível mais a tolerância adicional, o que indica que uma requisição ao modelo ou alguma ferramenta ficou muito tempo sem retornar, impedindo o encerramento normal na fronteira de turno. Verifique principalmente a última chamada de ferramenta que não retornou antes da interrupção")
)

// AbortReason resolves the named cause attached to a cancelled run context.
func AbortReason(ctx context.Context) (code, short, text string, ok bool) {
	c := context.Cause(ctx)
	if c == nil {
		return "", "", "", false
	}
	var ac *AbortCause
	if errors.As(c, &ac) {
		return ac.Code, ac.Short, ac.Text, true
	}
	switch {
	case errors.Is(c, context.DeadlineExceeded):
		return "deadline_exceeded", "O context upstream atingiu o deadline",
			"O context upstream atingiu o deadline, mas quem o definiu não anexou uma causa nomeada via WithTimeoutCause: " + c.Error(), true
	case errors.Is(c, context.Canceled):
		return "canceled_no_cause", "Quem cancelou não anexou uma causa nomeada",
			"O context upstream foi cancelado, mas quem cancelou não anexou uma causa nomeada via context.WithCancelCause; registre a causa em agent/cancelcause.go e conecte este ponto de cancelamento", true
	default:
		return "other", firstLine(c.Error(), 80), c.Error(), true
	}
}
