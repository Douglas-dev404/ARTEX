package agent

import (
	"strings"

	"github.com/Autumn-27/norma/harness"
)

// Prompt de encerramento (wrap-up / settlement prompt): quando um agent é terminado por [esgotamento de passos (MaxTurns)] ou
// [timeout (run_seconds/MaxDuration)], a fase de settlement do SDK injeta este prompt,
// para que o agent primeiro grave no banco o que já identificou mas ainda não gravou e depois emita um resumo de uma frase, evitando um final largado.
//
// O prompt de encerramento de cada agent pode ser sobrescrito no painel conforme necessário (armazenado em agents.wrapup_prompt); se vazio, usa-se o
// padrão embutido daqui. Somente o [corpo do prompt] é editável; quais ferramentas desabilitar e quantos turnos de orçamento o encerramento recebe são política fixa no código.

// WrapupOverride, if set, returns the stored wrap-up prompt for an agent key and
// whether a non-empty one exists. Wired by the server to the agents table (like
// PromptOverride for system prompts). nil / empty → the built-in default is used.
var WrapupOverride func(agentKey string) (string, bool)

// WrapupMaxTurnsOverride, if set, returns the admin-configured turn budget for the
// wrap-up phase of an agent and whether a positive one exists. Wired to the agents
// table. nil / ≤0 → the built-in per-agent default (wrapupTurnDefaults) is used.
var WrapupMaxTurnsOverride func(agentKey string) (int, bool)

// Prompts de encerramento padrão embutidos, indexados pela chave do agent. O worker reutiliza o settleWrapUpPrompt historicamente fixo no código
// (definido em worker.go); planner/mainagent têm uma versão cada; os não encontrados (agents personalizados) usam o fallback genérico.
var wrapupDefaults = map[string]string{
	"worker":    settleWrapUpPrompt,
	"planner":   plannerWrapUpDefault,
	"mainagent": mainAgentWrapUpDefault,
}

// wrapupTurnDefaults: padrões embutidos do orçamento de turnos [próprio] da fase de encerramento de cada agent (podem ser sobrescritos por valor >0 no painel).
// Todos dão 10 turnos, garantindo passos suficientes para gravar no banco na fase de encerramento. Os não encontrados usam genericWrapupTurns.
var wrapupTurnDefaults = map[string]int{
	"worker":    10,
	"planner":   10,
	"mainagent": 10,
}

const genericWrapupTurns = 10

const plannerWrapUpDefault = "Os passos do seu planejamento desta rodada estão prestes a se esgotar — atenção: termina apenas [esta rodada], o sistema ainda o acordará novamente conforme a situação mudar para continuar planejando; a tarefa não está sendo encerrada, e você não precisa concluir aqui todo o planejamento. Consolide as conclusões que já ficaram claras nesta rodada, sem deixar a rodada em vão, mas também [não force intenções só para encerrar] (0 intenções nesta rodada continua sendo um resultado perfeitamente normal): (1) se já determinou direções de exploração que [devem ser despachadas agora], envie-as de uma vez em lote com add_intent (o que já está pensado não deve ficar guardado); (2) para metas já comprovadas como atingidas por alguma descoberta/fato, chame prove_goal para marcar como met (não deixe de julgar); (3) se identificou uma cadeia de exploração serial que precisa ser feita em etapas, registre com TodoWrite, para continuar despachando na próxima vez que for acordado. Ao terminar, encerre direto a rodada, sem precisar emitir texto de resumo."

const mainAgentWrapUpDefault = "Seus passos estão prestes a se esgotar e esta interação vai terminar. Não inicie novas explorações/operações. **Em uma única frase, em texto puro**, resuma ao usuário o progresso atual, as conclusões-chave e o próximo passo sugerido."

const genericWrapUpDefault = "Você está prestes a ser encerrado por esgotamento de orçamento. Primeiro grave de volta os resultados já concluídos que ainda não foram gravados e depois resuma **em uma única frase, em texto puro**, o que você fez e quais conclusões-chave obteve (esta frase será exibida como resultado desta execução)."

// WrapupDefault returns the built-in default wrap-up prompt for an agent key —
// used by the admin UI as the "restore default" value and empty-field placeholder.
func WrapupDefault(agentKey string) string {
	if d, ok := wrapupDefaults[agentKey]; ok {
		return d
	}
	return genericWrapUpDefault
}

// WrapupTurnsDefault returns the built-in wrap-up turn budget for an agent key —
// used by the admin UI as the "0 = default N" hint.
func WrapupTurnsDefault(agentKey string) int {
	if n, ok := wrapupTurnDefaults[agentKey]; ok {
		return n
	}
	return genericWrapupTurns
}

// resolveWrapup returns the effective wrap-up prompt: the DB override (if set and
// non-empty) over the built-in default.
func resolveWrapup(agentKey string) string {
	if WrapupOverride != nil {
		if t, ok := WrapupOverride(agentKey); ok && strings.TrimSpace(t) != "" {
			return t
		}
	}
	return WrapupDefault(agentKey)
}

// resolveWrapupTurns returns the effective wrap-up turn budget: a positive DB
// override over the built-in per-agent default.
func resolveWrapupTurns(agentKey string) int {
	if WrapupMaxTurnsOverride != nil {
		if v, ok := WrapupMaxTurnsOverride(agentKey); ok && v > 0 {
			return v
		}
	}
	return WrapupTurnsDefault(agentKey)
}

// wrapupSettlement builds the settlement config for an agent's run. Prompt and the
// turn budget are admin-editable per agent; disabled tools are code-owned policy so
// a user can't edit away the "stop probing" guardrail. Resolved fresh each run
// (reads DB live), so edits apply on the next run without a restart.
func wrapupSettlement(agentKey string, disabledTools []string) *harness.Settlement {
	return &harness.Settlement{
		Prompt:        resolveWrapup(agentKey),
		DisabledTools: disabledTools,
		MaxTurns:      resolveWrapupTurns(agentKey),
	}
}

// ---------- Prompts de encerramento por timeout de nível de tarefa (ver docs/任务级超时与收尾设计.md) ----------
//
// São [dois conjuntos] distintos do prompt per-run: o per-run diz "o orçamento desta run acabou"; o timeout da tarefa diz
// "a tarefa inteira chegou ao prazo e vai terminar". A semântica costuma ser oposta (principalmente no planner: o per-run diz "não pare, continue planejando",
// o timeout da tarefa diz "prazo atingido, pare de planejar e faça o julgamento final"). Configurados apenas para worker/planner.

// WrapupTaskTimeoutOverride / …TurnsOverride: sobrescritas no DB do prompt de encerramento por timeout da tarefa e do número de turnos
// (ligadas a agents.task_timeout_wrapup_prompt / _max_turns, apenas worker/planner).
var (
	WrapupTaskTimeoutOverride      func(agentKey string) (string, bool)
	WrapupTaskTimeoutTurnsOverride func(agentKey string) (int, bool)
)

var taskTimeoutWrapupDefaults = map[string]string{
	"worker":  workerTaskTimeoutDefault,
	"planner": plannerTaskTimeoutDefault,
}

const workerTaskTimeoutDefault = "**A tarefa inteira atingiu o limite de timeout e vai terminar** (não é o orçamento desta run, é a exploração inteira que chegou ao prazo). Esta é a última chance: (1) grave no banco [tudo] o que você já identificou mas ainda não gravou — novos ativos com insert_assets, conclusões/fatos de exploração com record_fact, vulnerabilidades confirmadas com report_finding; (2) não inicie mais nenhum comando/sondagem novo; (3) **por último, em uma única frase, em texto puro**, resuma suas conclusões-chave sobre esta intenção."

const plannerTaskTimeoutDefault = "**A tarefa inteira atingiu o limite de timeout e vai terminar** (não é esta rodada, é o término de toda a tarefa). Com base em [todos] os fatos e descobertas atuais, faça um último julgamento de metas: para as metas comprovadas por evidência como atingidas, chame prove_goal para marcar como met (não deixe de julgar). **Não gere mais nenhuma intenção nova** (intenções despachadas agora também não seriam mais executadas). Concluído o julgamento, encerre, sem precisar emitir texto de resumo."

// TaskTimeoutWrapupDefault retorna o prompt de encerramento por timeout de tarefa padrão embutido de um agent (para placeholder/restaurar padrão no painel).
func TaskTimeoutWrapupDefault(agentKey string) string {
	return taskTimeoutWrapupDefaults[agentKey] // não configurado (mainagent/chat) retorna string vazia
}

// resolveTaskTimeoutWrapup: sobrescrita no DB (não vazia) > padrão embutido. String vazia significa que o agent não tem prompt de timeout de tarefa
// (não worker/planner); nesse caso o chamador deve voltar ao prompt per-run.
func resolveTaskTimeoutWrapup(agentKey string) string {
	if WrapupTaskTimeoutOverride != nil {
		if t, ok := WrapupTaskTimeoutOverride(agentKey); ok && strings.TrimSpace(t) != "" {
			return t
		}
	}
	return TaskTimeoutWrapupDefault(agentKey)
}

func resolveTaskTimeoutTurns(agentKey string) int {
	if WrapupTaskTimeoutTurnsOverride != nil {
		if v, ok := WrapupTaskTimeoutTurnsOverride(agentKey); ok && v > 0 {
			return v
		}
	}
	return resolveWrapupTurns(agentKey) // por padrão segue o número de turnos per-run
}

// wrapupSettlementForTask builds settlement for a worker/planner run that is aware
// of the task deadline. See §5 of the design doc:
//   - clamped=true  → esta run foi limitada pelo deadline da tarefa: encerramento por Timeout = prazo da tarefa → prompt de timeout da tarefa;
//     encerramento por MaxTurns = os passos se esgotaram primeiro dentro da janela limitada, restam alguns minutos da tarefa → volta ao prompt per-run.
//   - clamped=false → a tarefa ainda tem tempo: os dois reasons usam o prompt per-run (isto é, degenera em wrapupSettlement).
//
// Entregue ao PromptByReason do harness, escolhido na hora do encerramento conforme o reason [real], sem descompasso na montagem.
func wrapupSettlementForTask(agentKey string, disabledTools []string, clamped bool) *harness.Settlement {
	perRun := resolveWrapup(agentKey)
	st := &harness.Settlement{
		Prompt:        perRun, // fallback (também o valor dos dois reasons quando não clamped)
		DisabledTools: disabledTools,
		MaxTurns:      resolveWrapupTurns(agentKey),
	}
	if clamped {
		if tt := resolveTaskTimeoutWrapup(agentKey); tt != "" {
			st.PromptByReason = map[harness.TerminalReason]string{
				harness.ReasonTimeout:  tt,     // prazo da tarefa
				harness.ReasonMaxTurns: perRun, // os passos se esgotaram primeiro, ainda resta tempo da tarefa
			}
			st.MaxTurns = resolveTaskTimeoutTurns(agentKey)
		}
	}
	return st
}
