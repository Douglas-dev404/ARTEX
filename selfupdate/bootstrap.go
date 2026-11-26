package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// smokeEnv faz o subprocesso iniciado pelo teste de fumaça pular o Bootstrap diretamente.
//
// A rigor, sem isso nada daria errado: o os.Executable() do subprocesso é artex.new, e todos os caminhos
// derivados dele levam o prefixo .new, sem tocar nos arquivos reais da atualização. Mas depender dessa
// coincidência é frágil demais; o curto-circuito explícito é evidente e poupa ao subprocesso uma sondagem de disco inútil.
const smokeEnv = "ARTEX_SELFUPDATE_SMOKE"

// Action é a instrução que o Bootstrap dá ao main.
type Action int

const (
	// Continue: inicia o server normalmente.
	Continue Action = iota
	// Restart: sai imediatamente com ExitRestart, para o script guardião relançar o processo.
	Restart
)

// State descreve o estado da atualização nesta inicialização, para que /api/update/check informe
// ao front-end com fidelidade "se a última atualização teve sucesso ou foi revertida".
type State struct {
	Pending     bool   // troca feita, ainda sem confirmação de estabilidade
	RolledBack  bool   // rollback automático acabou de ser executado nesta inicialização
	FailedStage bool   // arquivo staged reprovado na verificação/teste de fumaça, descartado
	Detail      string // explicação de uma frase voltada ao usuário
}

// Bootstrap roda logo no início do main e deve ser chamado antes de abrir qualquer porta ou o banco de dados.
//
// Três cenários:
//
// \t① Existe arquivo staged artex.new  → verifica + teste de fumaça; se passar, troca e pede reinício; se não, descarta e segue com a versão antiga
// \t② Só resta o arquivo marcador       → a troca acabou de ocorrer; conta uma tentativa; se falhar vezes demais seguidas, faz rollback
// \t③ Nada existe                       → inicialização normal
func Bootstrap() (Action, State) {
	if os.Getenv(smokeEnv) != "" {
		return Continue, State{}
	}
	p, err := ResolvePaths()
	if err != nil {
		log.Printf("[update] bootstrap ignorado: %v", err)
		return Continue, State{}
	}

	if _, err := os.Stat(p.New); err == nil {
		return applyStaged(p)
	}

	m, ok := readMarker(p.Marker)
	if !ok {
		return Continue, State{}
	}
	return confirmOrRollback(p, m)
}

// applyStaged trata o cenário "existe arquivo staged": se a verificação passar, faz a troca; se falhar, descarta.
//
// Este é o único ponto de toda a cadeia de atualização que sobrescreve o executável, e também a última barreira — o teste de fumaça barra
// download corrompido, arquitetura errada, dependências dinâmicas ausentes e problemas do tipo. Se um binário que não executa passar,
// o script guardião o relançará incansavelmente, o código Go nunca terá chance de rodar e o rollback automático se torna impossível.
func applyStaged(p Paths) (Action, State) {
	m, _ := readMarker(p.Marker)

	if err := verifyStaged(p); err != nil {
		log.Printf("[update] nova versão staged reprovada na verificação, descartada; continuando com a versão atual: %v", err)
		cleanStaged(p)
		_ = os.Remove(p.Marker)
		return Continue, State{FailedStage: true, Detail: "Falha na verificação da nova versão, descartada: " + err.Error()}
	}

	if err := swap(p); err != nil {
		log.Printf("[update] troca falhou; continuando com a versão atual: %v", err)
		cleanStaged(p)
		_ = os.Remove(p.Marker)
		return Continue, State{FailedStage: true, Detail: "Falha na troca: " + err.Error()}
	}

	// Troca concluída. Mantém o marcador, para que a próxima inicialização (que já roda a nova versão) confirme se está estável.
	m.Attempts = 0
	if m.StagedAt == 0 {
		m.StagedAt = time.Now().Unix()
	}
	if err := writeMarker(p.Marker, m); err != nil {
		log.Printf("[update] falha ao gravar o marcador de atualização (perde-se o rollback automático): %v", err)
	}
	log.Printf("[update] troca concluída para %s; saindo para reiniciar (exit %d)", orUnknown(m.To), ExitRestart)
	return Restart, State{Pending: true}
}

// confirmOrRollback trata a "inicialização após a troca": acumula o número de tentativas e, ao exceder o limite, restaura a versão antiga.
//
// A contagem só incrementa depois que o código Go começa a rodar, então cobre falhas do tipo "executa, mas trava na inicialização"
// (configuração incompatível, porta ocupada, migração do DB que explode); o caso "nem consegue dar exec" é barrado pelo
// teste de fumaça antes da troca. Só os dois juntos formam a cobertura completa.
func confirmOrRollback(p Paths, m marker) (Action, State) {
	m.Attempts++
	if m.Attempts > maxAttempts {
		if err := rollback(p); err != nil {
			// Se até o rollback falhar, não reiniciar mais, senão cairia em reinício infinito. Limpa o marcador
			// e deixa o processo subir no estado atual — se não subir, o usuário ao menos vê a causa nos logs.
			log.Printf("[update] nova versão falhou ao iniciar %d vezes seguidas e o rollback também falhou: %v", maxAttempts, err)
			_ = os.Remove(p.Marker)
			return Continue, State{Detail: "A nova versão falhou ao iniciar e o rollback também falhou: " + err.Error()}
		}
		log.Printf("[update] nova versão falhou ao iniciar %d vezes seguidas; rollback para %s concluído; saindo para reiniciar (exit %d)",
			maxAttempts, orUnknown(m.From), ExitRestart)
		_ = os.Remove(p.Marker)
		return Restart, State{RolledBack: true, Detail: fmt.Sprintf("A nova versão falhou ao iniciar; rollback para %s concluído", orUnknown(m.From))}
	}
	if err := writeMarker(p.Marker, m); err != nil {
		log.Printf("[update] falha ao atualizar o marcador de atualização: %v", err)
	}
	log.Printf("[update] nova versão iniciando (tentativa %d/%d); a atualização será confirmada após execução estável",
		m.Attempts, maxAttempts)
	return Continue, State{Pending: true}
}

// Settle confirma que a nova versão está rodando de forma estável e limpa o marcador de atualização.
//
// Chamado pelo main com atraso, depois que o listener HTTP sobe: só conta quem sobrevive a esse intervalo; do contrário o marcador permanece,
// a próxima inicialização continua acumulando tentativas, até disparar o rollback.
func Settle() {
	p, err := ResolvePaths()
	if err != nil {
		return
	}
	settle(p)
}

func settle(p Paths) {
	if _, ok := readMarker(p.Marker); !ok {
		return // não é uma inicialização pós-atualização, nada a fazer
	}
	if err := os.Remove(p.Marker); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("[update] falha ao limpar o marcador de atualização: %v", err)
		return
	}
	log.Printf("[update] nova versão estável, atualização concluída (versão anterior mantida como %s)", p.Old)
}

// SettleDelay é o tempo de execução necessário para julgar que "a nova versão sobreviveu".
const SettleDelay = 30 * time.Second

// verifyStaged verifica o arquivo staged: primeiro compara o SHA256, depois realmente o inicia uma vez.
func verifyStaged(p Paths) error {
	want, err := os.ReadFile(p.Sum)
	if err != nil {
		return fmt.Errorf("ler checksum: %w", err)
	}
	got, err := fileSHA256(p.New)
	if err != nil {
		return fmt.Errorf("calcular checksum: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(string(want)), got) {
		return errors.New("SHA256 não confere (download corrompido ou adulterado)")
	}
	return smokeTest(p.New)
}

// smokeTest inicia o novo binário com -h para confirmar que ele realmente executa neste sistema.
// Isso barra uma grande classe de problemas: download truncado, arquitetura errada (exec format error), dependências ausentes etc.
func smokeTest(bin string) error {
	if err := os.Chmod(bin, 0o755); err != nil {
		return fmt.Errorf("conceder permissão de execução: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "-h")
	cmd.Env = append(os.Environ(), smokeEnv+"=1")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return errors.New("teste de fumaça expirou (novo binário não responde)")
	}
	if err != nil {
		snippet := strings.TrimSpace(string(out))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		return fmt.Errorf("teste de fumaça falhou: %v: %s", err, snippet)
	}
	return nil
}

// swap troca o binário atual pela nova versão staged.
//
// Unix e Windows permitem rename em um executável em execução (o que o Windows proíbe é excluir e
// sobrescrever; rename não entra nessa lista), então aqui não precisa distinguir plataforma nem parar o próprio processo antes.
func swap(p Paths) error {
	// O rename do Windows não sobrescreve um destino existente; o .old deixado pela atualização anterior precisa ser removido antes.
	if err := os.Remove(p.Old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("limpar backup antigo %s: %w", p.Old, err)
	}
	if err := os.Rename(p.Current, p.Old); err != nil {
		return fmt.Errorf("fazer backup da versão atual: %w", err)
	}
	if err := os.Rename(p.New, p.Current); err != nil {
		// A troca falhou, mas a versão atual já foi movida; é preciso devolvê-la ao lugar, senão a próxima inicialização fica sem executável.
		if rerr := os.Rename(p.Old, p.Current); rerr != nil {
			return fmt.Errorf("falha ao instalar a nova versão (%v) e falha ao restaurar a versão atual: %w", err, rerr)
		}
		return fmt.Errorf("instalar a nova versão: %w", err)
	}
	_ = os.Remove(p.Sum)
	return nil
}

// rollback devolve a versão antiga salva pelo swap.
func rollback(p Paths) error {
	if _, err := os.Stat(p.Old); err != nil {
		return fmt.Errorf("não há backup disponível para rollback: %s: %w", p.Old, err)
	}
	// Move a nova versão que não sobe para .failed, para investigação, em vez de apagá-la.
	failed := p.Current + ".failed"
	_ = os.Remove(failed)
	if err := os.Rename(p.Current, failed); err != nil {
		return fmt.Errorf("mover a versão com falha: %w", err)
	}
	if err := os.Rename(p.Old, p.Current); err != nil {
		return fmt.Errorf("restaurar a versão antiga: %w", err)
	}
	return nil
}

// Rollback é a implementação de /api/update/rollback: volta ativamente para a versão anterior.
// Só faz a troca; o reinício também fica com o script guardião (o chamador sai em seguida com ExitRestart).
func Rollback() error {
	p, err := ResolvePaths()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p.Old); err != nil {
		return errors.New("Não há versão anterior para rollback (" + p.Old + " não existe)")
	}
	cleanStaged(p)
	if err := smokeTest(p.Old); err != nil {
		return fmt.Errorf("a versão anterior não executa; rollback recusado: %w", err)
	}
	// Troca a atual com o backup: depois do rollback ainda é possível voltar.
	tmp := p.Current + ".swap"
	_ = os.Remove(tmp)
	if err := os.Rename(p.Current, tmp); err != nil {
		return fmt.Errorf("mover a versão atual: %w", err)
	}
	if err := os.Rename(p.Old, p.Current); err != nil {
		_ = os.Rename(tmp, p.Current)
		return fmt.Errorf("instalar a versão anterior: %w", err)
	}
	if err := os.Rename(tmp, p.Old); err != nil {
		log.Printf("[update] falha ao organizar o backup após o rollback (não afeta a execução): %v", err)
	}
	_ = os.Remove(p.Marker)
	return nil
}

// HasBackup informa se existe uma versão anterior para rollback, para o front-end decidir se exibe o botão de rollback.
func HasBackup() bool {
	p, err := ResolvePaths()
	if err != nil {
		return false
	}
	_, err = os.Stat(p.Old)
	return err == nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "versão desconhecida"
	}
	return s
}
