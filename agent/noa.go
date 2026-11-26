package agent

import (
	"log"
	"path/filepath"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/noaadapter"
)

// noaWarn returns a diagnostics sink tagging non-fatal noa messages with the
// session, routed through the package logger (agents have no per-instance one).
func noaWarn(session string) func(string) {
	return func(msg string) { log.Printf("[noa] %s: %s", session, msg) }
}

// noa é o mecanismo de "compressão de contexto dirigida pelo modelo" introduzido no norma v0.4.0, ativado/desativado pelo usuário como recurso experimental da plataforma nas
// configurações do sistema. É mutuamente exclusivo com a compaction embutida: noaadapter.Enable é a única entrada, que de uma vez anexa o
// assumidor de contexto (Compactor), a ferramenta Compress e três prompts residentes; sem chamar Enable ele fica desativado
// (a compaction embutida funciona normalmente). A chave é resolvida pelo noaEnabledFn injetado em cada agent, lido uma vez
// por run, portanto a alternância afeta apenas os runs iniciados depois, sem precisar recriar o agent.

// enableNoa conecta o noa em opts quando o resolvedor informa que está ativado. archiveRoot é o diretório-base persistente do texto original comprimido
// (usa o workDir global; todos os agents gravam uniformemente em <workDir>/noa, sem se dispersar por diretórios de tarefa/intenção), sessionID
// nomeia o subdiretório de arquivo abaixo dele (globalmente único, logo sem conflito dentro do mesmo diretório-base).
//
// noa é um recurso experimental: falha ao conectá-lo não deve interromper tarefas reais. Em caso de erro, reporta via onWarn e volta à compressão embutida.
// Ao ativar com sucesso, limpa opts.Compaction, evitando que o agentcore avise sobre "dois gerenciadores de contexto configurados ao mesmo tempo".
func enableNoa(opts *agentcore.Options, enabled func() bool, archiveRoot, sessionID string, onWarn func(string)) {
	if enabled == nil || !enabled() {
		return
	}
	if opts.OnWarn == nil {
		opts.OnWarn = onWarn
	}
	if err := noaadapter.Enable(opts, noaadapter.Options{
		ArchiveBaseDir: filepath.Join(archiveRoot, "noa"),
		SessionID:      sessionID,
		OnWarn:         onWarn,
	}); err != nil {
		if onWarn != nil {
			onWarn("falha ao ativar a compressão noa, voltando à compressão embutida: " + err.Error())
		}
		return
	}
	// Compactor sobrescreve Compaction, mas com ambos presentes o agentcore avisa a cada vez; limpa explicitamente.
	opts.Compaction = nil
}
