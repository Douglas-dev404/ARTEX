package selfupdate

import (
	"archive/zip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// testPaths cria um diretório de atualização isolado. Não dá para usar ResolvePaths() diretamente — ele apontaria para o próprio
// binário de teste, e ao rodar renomearia o executável do go test.
func testPaths(t *testing.T) Paths {
	t.Helper()
	dir := t.TempDir()
	return Paths{
		Dir:     dir,
		Current: filepath.Join(dir, "artex"),
		New:     filepath.Join(dir, "artex.new"),
		Sum:     filepath.Join(dir, "artex.new.sha256"),
		Old:     filepath.Join(dir, "artex.old"),
		Marker:  filepath.Join(dir, "artex.upgrade.json"),
	}
}

// fakeBin escreve um script shell executável fingindo ser o artex. O smokeTest apenas o inicia com -h para ver o código de saída,
// um script basta e é muito mais rápido do que compilar um binário de verdade.
func fakeBin(t *testing.T, path, marker string, exitCode int) {
	t.Helper()
	script := "#!/bin/sh\necho " + marker + "\nexit " + itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("gravar binário falso %s: %v", path, err)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return string(rune('0' + n))
}

// stage deixa bin no estado "staged, aguardando troca": grava artex.new e seu checksum.
func stage(t *testing.T, p Paths, marker string, exitCode int) {
	t.Helper()
	fakeBin(t, p.New, marker, exitCode)
	sum, err := fileSHA256(p.New)
	if err != nil {
		t.Fatalf("calcular checksum: %v", err)
	}
	if err := os.WriteFile(p.Sum, []byte(sum), 0o644); err != nil {
		t.Fatalf("gravar checksum: %v", err)
	}
}

func readAll(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ler %s: %v", path, err)
	}
	return string(b)
}

func requireUnix(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("o binário falso é um script sh, não roda no Windows")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b       string
		want       int
		comparable bool
	}{
		{"0.3.7", "0.3.8", -1, true},
		{"0.3.8", "0.3.7", 1, true},
		{"0.3.7", "0.3.7", 0, true},
		{"v0.3.7", "0.3.8", -1, true}, // o build.sh remove o v, a tag leva o v; os dois lados precisam ser aceitos
		{"0.3.7", "v0.3.7", 0, true},
		{"0.9.0", "0.10.0", -1, true}, // compara numericamente e não em ordem lexicográfica
		{"1.0.0", "0.99.99", 1, true},
		// Build de desenvolvimento deve ser julgado não comparável, senão seria sobrescrito pela versão estável, perdendo alterações não commitadas.
		{"dev", "0.3.8", 0, false},
		{"0.3.7-2-gabc1234", "0.3.8", 0, false},
		{"0.3.7-dirty", "0.3.8", 0, false},
		{"0.3", "0.3.8", 0, false},
		{"", "0.3.8", 0, false},
	}
	for _, c := range cases {
		got, ok := CompareVersions(c.a, c.b)
		if ok != c.comparable {
			t.Errorf("CompareVersions(%q,%q) comparable=%v, esperado %v", c.a, c.b, ok, c.comparable)
			continue
		}
		if ok && got != c.want {
			t.Errorf("CompareVersions(%q,%q)=%d, esperado %d", c.a, c.b, got, c.want)
		}
	}
}

func TestResolvePathsNaming(t *testing.T) {
	p, err := ResolvePaths()
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	// Invariante crítico: todos os arquivos de atualização ficam no mesmo diretório do executável. Cair no CWD invalidaria totalmente a troca
	// ao rodar como serviço (o diretório de trabalho pode ser /).
	for name, path := range map[string]string{"New": p.New, "Sum": p.Sum, "Old": p.Old, "Marker": p.Marker} {
		if filepath.Dir(path) != p.Dir {
			t.Errorf("%s não está no diretório do executável: %s (esperado %s)", name, path, p.Dir)
		}
	}
	// No Windows, .new/.old precisam manter o .exe, senão o teste de fumaça e a execução pós-troca falham.
	if runtime.GOOS == "windows" {
		if !strings.HasSuffix(p.New, ".exe") || !strings.HasSuffix(p.Old, ".exe") {
			t.Errorf("no Windows, .new/.old devem terminar em .exe: new=%s old=%s", p.New, p.Old)
		}
	}
}

func TestVerifyStagedRejectsTamperedBinary(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	stage(t, p, "new", 0)

	// Altera o arquivo depois de gravar o checksum, simulando download corrompido / arquivo trocado.
	fakeBin(t, p.New, "tampered", 0)
	if err := verifyStaged(p); err == nil {
		t.Fatal("esperava-se que SHA256 divergente fosse rejeitado, mas passou")
	}
}

func TestVerifyStagedRejectsUnrunnableBinary(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	stage(t, p, "broken", 1) // executa, mas com código de saída diferente de 0

	if err := verifyStaged(p); err == nil {
		t.Fatal("esperava-se que falha no teste de fumaça fosse rejeitada, mas passou")
	}
}

func TestApplyStagedHappyPath(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "old", 0)
	stage(t, p, "new", 0)
	if err := writeMarker(p.Marker, marker{From: "0.3.7", To: "0.3.8"}); err != nil {
		t.Fatalf("gravar marcador: %v", err)
	}

	action, st := applyStaged(p)
	if action != Restart {
		t.Fatalf("esperado Restart, obtido %v", action)
	}
	if !st.Pending {
		t.Error("após a troca o estado deveria ser Pending")
	}
	if !strings.Contains(readAll(t, p.Current), "new") {
		t.Error("artex deveria ter sido substituído pela nova versão")
	}
	if !strings.Contains(readAll(t, p.Old), "old") {
		t.Error("a versão antiga deveria ter sido salva em artex.old")
	}
	if _, err := os.Stat(p.New); !os.IsNotExist(err) {
		t.Error("após a troca artex.new deveria ter desaparecido")
	}
	if _, err := os.Stat(p.Sum); !os.IsNotExist(err) {
		t.Error("após a troca o arquivo de checksum deveria ter sido limpo")
	}
	// O marcador deve permanecer: a próxima inicialização (que roda a nova versão) depende dele para contar e, se preciso, fazer rollback.
	if _, ok := readMarker(p.Marker); !ok {
		t.Error("após a troca o marcador de atualização deveria permanecer")
	}
}

func TestApplyStagedKeepsCurrentWhenVerifyFails(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "old", 0)
	stage(t, p, "new", 0)
	fakeBin(t, p.New, "tampered", 0) // corrompe o checksum

	action, st := applyStaged(p)
	if action != Continue {
		t.Fatalf("em falha de verificação esperado Continue, obtido %v", action)
	}
	if !st.FailedStage {
		t.Error("o estado deveria estar marcado como FailedStage")
	}
	if !strings.Contains(readAll(t, p.Current), "old") {
		t.Fatal("em falha de verificação a versão atual jamais deve ser tocada")
	}
	if _, err := os.Stat(p.New); !os.IsNotExist(err) {
		t.Error("o arquivo staged reprovado deve ser limpo, senão a próxima inicialização tentará de novo")
	}
}

func TestSwapOverwritesPreviousBackup(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "v2", 0)
	fakeBin(t, p.Old, "v1", 0) // backup deixado pela atualização anterior
	stage(t, p, "v3", 0)

	if err := swap(p); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if !strings.Contains(readAll(t, p.Current), "v3") {
		t.Error("deveria ter trocado para v3")
	}
	if !strings.Contains(readAll(t, p.Old), "v2") {
		t.Error("o backup deveria ser atualizado para o v2 recém-substituído")
	}
}

func TestConfirmCountsAttemptsThenRollsBack(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "broken-new", 0)
	fakeBin(t, p.Old, "good-old", 0)
	m := marker{From: "0.3.7", To: "0.3.8"}

	// Nas primeiras maxAttempts inicializações só se acumula a contagem, dando à nova versão a chance de se firmar.
	for i := 1; i <= maxAttempts; i++ {
		action, st := confirmOrRollback(p, m)
		if action != Continue {
			t.Fatalf("na tentativa %d esperado Continue, obtido %v", i, action)
		}
		if !st.Pending {
			t.Errorf("na tentativa %d o estado deveria ser Pending", i)
		}
		got, ok := readMarker(p.Marker)
		if !ok || got.Attempts != i {
			t.Fatalf("após a tentativa %d attempts=%d (ok=%v), esperado %d", i, got.Attempts, ok, i)
		}
		m = got
	}

	// Mais uma falha excede o limite e a versão antiga é restaurada automaticamente.
	action, st := confirmOrRollback(p, m)
	if action != Restart {
		t.Fatalf("ao exceder o limite de tentativas esperado Restart, obtido %v", action)
	}
	if !st.RolledBack {
		t.Error("o estado deveria estar marcado como RolledBack")
	}
	if !strings.Contains(readAll(t, p.Current), "good-old") {
		t.Fatal("deveria ter feito rollback para a versão antiga")
	}
	if _, err := os.Stat(p.Marker); !os.IsNotExist(err) {
		t.Error("após o rollback o marcador deve ser limpo, senão haveria rollback infinito")
	}
	// A versão que não sobe é mantida para investigação, não é apagada.
	if _, err := os.Stat(p.Current + ".failed"); err != nil {
		t.Error("a versão com falha deve ser mantida como .failed para investigação")
	}
}

func TestManualRollbackIsReversible(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "v2", 0)
	fakeBin(t, p.Old, "v1", 0)

	// Rollback() passa por ResolvePaths(); aqui testa-se diretamente a semântica de troca subjacente.
	tmp := p.Current + ".swap"
	if err := os.Rename(p.Current, tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(p.Old, p.Current); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, p.Old); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readAll(t, p.Current), "v1") {
		t.Error("após o rollback a versão atual deveria ser v1")
	}
	if !strings.Contains(readAll(t, p.Old), "v2") {
		t.Error("após o rollback o backup deveria virar v2, para ainda ser possível voltar")
	}
}

func TestParseSums(t *testing.T) {
	const (
		linuxSum = "1111111111111111111111111111111111111111111111111111111111111111"
		winSum   = "ABCDEF0000000000000000000000000000000000000000000000000000000000"
	)
	// A saída do sha256sum usa dois espaços como separador; shasum -a 256 em modo binário acrescenta * ao nome do arquivo.
	raw := linuxSum + "  artex-0.3.8-linux-amd64.zip\n" +
		winSum + " *artex-0.3.8-windows-amd64.zip\n" +
		"\n" +
		"garbage line\n" + // exatamente dois campos, mas o primeiro não é um digest
		"deadbeef  artex-0.3.8-darwin-arm64.zip\n" // tamanho de digest incorreto

	out := parseSums(raw)
	if out["artex-0.3.8-linux-amd64.zip"] != linuxSum {
		t.Errorf("entrada linux interpretada incorretamente: %v", out)
	}
	// Digests em minúsculas de forma uniforme, para que a comparação não resulte em divergência falsa por diferença de caixa.
	if got := out["artex-0.3.8-windows-amd64.zip"]; got != strings.ToLower(winSum) {
		t.Errorf("entrada windows incorreta (prefixo * deve ser removido e digest convertido para minúsculas): %q", got)
	}
	if len(out) != 2 {
		t.Errorf("linhas vazias, linhas sem digest e linhas de tamanho incorreto devem ser ignoradas, obtido %v", out)
	}
}

func TestExtractBinaryFindsNestedEntry(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("o nome base no pacote é artex.exe no Windows; este caso é construído com nomenclatura Unix")
	}
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "release.zip")

	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	// Estrutura de um pacote de release real: artex-<versão>-<os>-<arch>/artex, mais alguns arquivos de ruído.
	for name, body := range map[string]string{
		"artex-0.3.8-linux-amd64/README.md":           "readme",
		"artex-0.3.8-linux-amd64/skills/a.md":         "skill",
		"artex-0.3.8-linux-amd64/artex":               "#!/bin/sh\nexit 0\n",
		"artex-0.3.8-linux-amd64/config.example.json": "{}",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	dst := filepath.Join(dir, "out")
	if err := extractBinary(zipPath, dst); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if got := readAll(t, dst); !strings.Contains(got, "exit 0") {
		t.Errorf("o extraído não é o executável artex: %q", got)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Error("o binário extraído deve ter o bit de execução")
	}
}

func TestExtractBinaryMissingEntry(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "release.zip")
	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("artex-0.3.8-linux-amd64/README.md")
	_, _ = w.Write([]byte("readme"))
	_ = zw.Close()
	f.Close()

	if err := extractBinary(zipPath, filepath.Join(dir, "out")); err == nil {
		t.Fatal("deve dar erro quando não há executável no pacote")
	}
}

func TestCheckURLRejectsNonGitHub(t *testing.T) {
	bad := []string{
		"http://github.com/x",           // não HTTPS
		"https://evil.com/artex.zip",    // domínio fora da lista permitida
		"https://github.com.evil.com/x", // disfarce por sufixo
		"https://raw.githubusercontent.com.evil.com/x",
	}
	for _, raw := range bad {
		u := mustParse(t, raw)
		if err := checkURL(u); err == nil {
			t.Errorf("checkURL(%q) deveria ser recusado", raw)
		}
	}
	good := []string{
		"https://api.github.com/repos/x/releases/latest",
		"https://objects.githubusercontent.com/blah",
		"https://GitHub.com/x", // domínio não diferencia maiúsculas de minúsculas
	}
	for _, raw := range good {
		u := mustParse(t, raw)
		if err := checkURL(u); err != nil {
			t.Errorf("checkURL(%q) deveria ser aceito, mas deu erro: %v", raw, err)
		}
	}
}

func TestAssetNameMatchesBuildScript(t *testing.T) {
	// O package_binary do build.sh usa artex-<versão>-<os>-<arch>.zip, e o número da versão
	// sem o prefixo v. Errar um único caractere aqui faria a atualização com um clique não achar o asset em nenhuma plataforma.
	if got := AssetName("v0.3.8", "linux", "amd64"); got != "artex-0.3.8-linux-amd64.zip" {
		t.Errorf("AssetName = %q", got)
	}
	if got := AssetName("0.3.8", "windows", "amd64"); got != "artex-0.3.8-windows-amd64.zip" {
		t.Errorf("AssetName = %q", got)
	}
}

func mustParse(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("interpretar %q: %v", raw, err)
	}
	return u
}

func TestSettleClearsMarkerAndStopsRollback(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "new", 0)
	fakeBin(t, p.Old, "old", 0)
	if err := writeMarker(p.Marker, marker{From: "0.3.7", To: "0.3.8", Attempts: 2}); err != nil {
		t.Fatal(err)
	}

	settle(p)

	if _, err := os.Stat(p.Marker); !os.IsNotExist(err) {
		t.Fatal("após confirmar a estabilidade o marcador de atualização deve ser limpo")
	}
	// Sem o marcador, reinícios normais seguintes não acumulam contagem nem disparam rollback por engano.
	if _, ok := readMarker(p.Marker); ok {
		t.Error("a leitura do marcador deveria falhar")
	}
	// O backup deve permanecer; o usuário ainda pode fazer rollback manual.
	if _, err := os.Stat(p.Old); err != nil {
		t.Error("após confirmar a estabilidade o backup da versão anterior ainda deve ser mantido")
	}
}

func TestSettleIsNoopWithoutMarker(t *testing.T) {
	requireUnix(t)
	p := testPaths(t)
	fakeBin(t, p.Current, "cur", 0)
	settle(p) // caminho de inicialização normal; não deve dar panic nem mexer em nenhum arquivo
	if _, err := os.Stat(p.Current); err != nil {
		t.Error("sem marcador, settle não deve afetar nenhum arquivo")
	}
}
