// Package selfupdate implements a atualização com um clique do ARTEX pela página: baixa o novo
// binário da GitHub Release, verifica, prepara (stage) e, na próxima inicialização, troca atomicamente.
//
// Divisão geral de responsabilidades (veja start.sh / start.bat):
//
// \tScript de inicialização = loop guardião simples; só decide se relança com base no código de saída após o processo terminar
// \tEste pacote             = toda a lógica propensa a erro (download / verificação SHA256 / teste de fumaça / troca / rollback em caso de falha)
//
// A troca fica em Go e não nos scripts porque a verificação sha256 e o teste de fumaça precisariam ser escritos em duas versões, sh e bat
// (sha256sum / shasum / certutil), e este é justamente o elo que menos pode falhar — instalar um binário
// que não executa faria o processo guardião relançá-lo fielmente, sem parar, e o usuário só resolveria entrando na máquina.
//
// Uma atualização completa passa por três inicializações de processo:
//
// \t① O server antigo recebe /api/update/apply → baixa e verifica → prepara artex.new → exit 75
// \t② O script relança a versão antiga → o Bootstrap encontra artex.new → verificação + teste de fumaça → troca → exit 75
// \t③ O script relança, agora já na nova versão → o Bootstrap registra uma tentativa → após iniciar com sucesso, limpa o marcador
//
// Qualquer etapa que falhe volta para a versão antiga: em ② se a verificação não passar, apaga o arquivo staged e segue com a antiga; em ③ se 3 inicializações seguidas não chegarem a
// limpar o marcador (crash na subida), troca artex.old de volta automaticamente.
package selfupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ExitRestart é o código de saída que significa "peça ao processo guardião para me relançar" (EX_TEMPFAIL). O script de inicialização, ao vê-lo,
// relança imediatamente, sem contar no backoff de crash. 0 significa parada normal pelo usuário (o script sai do loop); qualquer outro valor é tratado como crash.
const ExitRestart = 75

// maxAttempts é o número de tentativas de inicialização permitidas após a troca. A cada inicialização a nova versão soma 1 à contagem; sobrevivendo a
// settleDelay, o marcador é limpo; se cair maxAttempts vezes seguidas, a nova versão simplesmente não sobe, e o rollback é automático.
const maxAttempts = 3

// Paths reúne todos os arquivos envolvidos em uma atualização, todos sob o **diretório do executável**.
// Deliberadamente não usa o CWD: ao rodar como serviço o diretório de trabalho pode ser / ou qualquer caminho, e usar o CWD faria o arquivo staged cair
// em outro lugar, invalidando a lógica de troca.
type Paths struct {
	Dir     string // diretório do executável
	Current string // binário em execução        artex      / artex.exe
	New     string // nova versão staged            artex.new  / artex.new.exe
	Sum     string // sha256 da nova versão (hex)  artex.new.sha256 / artex.new.exe.sha256
	Old     string // versão antiga salva antes da troca      artex.old  / artex.old.exe
	Marker  string // marcador de estado da atualização            artex.upgrade.json
}

// ResolvePaths deriva todos os caminhos de atualização a partir do executável atual.
//
// No Windows, .new/.old também precisam manter o sufixo .exe, senão o teste de fumaça e a execução pós-troca falham,
// por isso o sufixo é removido primeiro e depois remontado, para que a nomenclatura das duas plataformas seja simétrica.
func ResolvePaths() (Paths, error) {
	exe, err := os.Executable()
	if err != nil {
		return Paths{}, fmt.Errorf("localizar o executável: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	name := filepath.Base(exe)
	ext := filepath.Ext(name) // no Windows é ".exe"; no Unix costuma ser vazio
	stem := strings.TrimSuffix(name, ext)

	join := func(suffix string) string { return filepath.Join(dir, stem+suffix+ext) }
	return Paths{
		Dir:     dir,
		Current: exe,
		New:     join(".new"),
		Sum:     join(".new") + ".sha256",
		Old:     join(".old"),
		Marker:  filepath.Join(dir, stem+".upgrade.json"),
	}, nil
}

// marker registra o progresso de uma troca, usado para disparar o rollback automático quando a nova versão não sobe.
type marker struct {
	From     string `json:"from"`     // versão antes da atualização
	To       string `json:"to"`       // versão de destino
	Attempts int    `json:"attempts"` // número de tentativas de inicialização após a troca
	StagedAt int64  `json:"staged_at"`
}

func readMarker(path string) (marker, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return marker{}, false
	}
	var m marker
	if json.Unmarshal(b, &m) != nil {
		return marker{}, false
	}
	return m, true
}

func writeMarker(path string, m marker) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// cleanStaged limpa o arquivo staged. Troca concluída, falha na verificação e cancelamento pelo usuário passam por aqui, para que um
// artex.new remanescente não seja tentado de novo na próxima inicialização.
func cleanStaged(p Paths) {
	_ = os.Remove(p.New)
	_ = os.Remove(p.Sum)
}

// CompareVersions compara dois números de versão, retornando -1/0/1 (a<b / a==b / a>b).
// ok=false indica que pelo menos um lado não é um número de versão comparável (por exemplo o "dev" de um build de desenvolvimento local ou
// o "0.3.7-2-gabc1234-dirty" produzido por git describe); nesse caso o chamador deve desabilitar a atualização com um clique,
// senão um build em desenvolvimento seria "atualizado" para a versão estável, sobrescrevendo alterações não commitadas.
func CompareVersions(a, b string) (int, bool) {
	av, aok := parseVersion(a)
	bv, bok := parseVersion(b)
	if !aok || !bok {
		return 0, false
	}
	for i := range 3 {
		if av[i] != bv[i] {
			if av[i] < bv[i] {
				return -1, true
			}
			return 1, true
		}
	}
	return 0, true
}

// parseVersion interpreta um número de versão no formato "v0.3.7" / "0.3.7" como [3]int.
//
// Aceita apenas o formato limpo de três partes: o build.sh, em builds sem tag, usa git describe para produzir
// versões com sufixo como "0.3.7-2-gabc1234"; elas devem ser julgadas não comparáveis, e não tratadas como
// 0.3.7 — senão o build de desenvolvimento seria considerado "já na última versão" ou sobrescrito pela versão estável.
func parseVersion(s string) ([3]int, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return [3]int{}, false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return [3]int{}, false
	}
	var out [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return [3]int{}, false
		}
		out[i] = n
	}
	return out, true
}

// InDocker informa se o processo roda dentro de um contêiner. No Docker, a troca é gravada na camada gravável do contêiner;
// recriar o contêiner com `docker compose up -d` volta à versão que vem na imagem — comportamento esperado
// (nesse momento o usuário estaria mesmo baixando uma imagem nova), mas o front-end precisa conseguir explicar isso com clareza.
func InDocker() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	b, err := os.ReadFile("/proc/1/cgroup")
	if err != nil {
		return false
	}
	s := string(b)
	return strings.Contains(s, "docker") || strings.Contains(s, "containerd")
}
