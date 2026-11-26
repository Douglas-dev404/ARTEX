package selfupdate

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"runtime"
	"strings"
	"time"
)

// sumsAsset é a lista de checksums gerada pelo release.yml, cobrindo todos os zips da Release.
const sumsAsset = "SHA256SUMS"

// maxBinarySize limita o tamanho do binário extraído, evitando que um zip malformado encha o disco.
const maxBinarySize = 512 << 20 // 512 MiB

// Phase é a fase do processo de atualização, usada diretamente como o campo phase nos eventos SSE.
type Phase string

const (
	PhaseIdle     Phase = "idle"
	PhaseDownload Phase = "downloading"
	PhaseVerify   Phase = "verifying"
	PhaseExtract  Phase = "extracting"
	PhaseStaged   Phase = "staged"
	PhaseFailed   Phase = "failed"
)

// Progress é fornecido pelo chamador, para enviar o progresso ao front-end. pct só tem significado na fase de download (0-100);
// nas demais fases passa-se -1.
type Progress func(ph Phase, pct int, msg string)

// Stage baixa o pacote de release da plataforma atual para a Release indicada, verifica e prepara o novo binário como artex.new.
//
// Usa o zip completo e não o binário puro por dois motivos: o SHA256SUMS das Releases existentes já cobre apenas
// o zip, então usar o zip dispensa mudar o CI e mantém compatibilidade com versões já publicadas; o zip ainda traz
// skills/, deixando uma porta aberta para sincronizar as skills embutidas no futuro. O custo é só baixar mais algumas centenas de KB de skills.
//
// Quando a função retorna, o staging está concluído; o chamador então encerra graciosamente e sai com ExitRestart.
func Stage(ctx context.Context, c *http.Client, rel *Release, currentVersion string, prog Progress) error {
	if prog == nil {
		prog = func(Phase, int, string) {}
	}
	p, err := ResolvePaths()
	if err != nil {
		return err
	}
	if err := checkWritable(p.Dir); err != nil {
		return err
	}

	name := AssetName(rel.TagName, runtime.GOOS, runtime.GOARCH)
	asset, ok := rel.FindAsset(name)
	if !ok {
		return fmt.Errorf("esta versão não oferece pacote de release para %s/%s (falta %s)", runtime.GOOS, runtime.GOARCH, name)
	}

	prog(PhaseDownload, 0, "Obtendo lista de checksums…")
	sums, err := fetchSums(ctx, c, rel)
	if err != nil {
		return err
	}
	want, ok := sums[name]
	if !ok {
		return fmt.Errorf("%s não inclui %s; instalação de binário não verificado recusada", sumsAsset, name)
	}

	// Todos os arquivos temporários ficam no diretório de destino, garantindo que o rename final seja uma operação atômica dentro do mesmo sistema de arquivos
	// (rename entre dispositivos falha, e /tmp costuma ser um ponto de montagem separado).
	zipPath := p.New + ".zip.part"
	binPath := p.New + ".part"
	defer func() {
		_ = os.Remove(zipPath)
		_ = os.Remove(binPath)
	}()

	prog(PhaseDownload, 0, fmt.Sprintf("Baixando %s (%s)…", name, humanSize(asset.Size)))
	got, err := download(ctx, c, asset, zipPath, prog)
	if err != nil {
		return err
	}

	prog(PhaseVerify, -1, "Verificando SHA256…")
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("SHA256 não confere: esperado %s, obtido %s (download corrompido ou adulterado)", short(want), short(got))
	}

	prog(PhaseExtract, -1, "Extraindo e executando teste de fumaça…")
	if err := extractBinary(zipPath, binPath); err != nil {
		return err
	}
	if err := smokeTest(binPath); err != nil {
		return fmt.Errorf("a nova versão não consegue rodar neste sistema: %w", err)
	}

	// O sha256 do próprio arquivo staged é guardado à parte: antes da troca na próxima inicialização ele ainda será verificado de novo,
	// prevenindo que o arquivo seja alterado ou corrompido entre o staging e o reinício.
	binSum, err := fileSHA256(binPath)
	if err != nil {
		return fmt.Errorf("calcular checksum do novo binário: %w", err)
	}
	if err := os.WriteFile(p.Sum, []byte(binSum), 0o644); err != nil {
		return fmt.Errorf("gravar checksum: %w", err)
	}
	if err := os.Rename(binPath, p.New); err != nil {
		_ = os.Remove(p.Sum)
		return fmt.Errorf("preparar (stage) a nova versão: %w", err)
	}

	if err := writeMarker(p.Marker, marker{
		From:     currentVersion,
		To:       strings.TrimPrefix(rel.TagName, "v"),
		StagedAt: time.Now().Unix(),
	}); err != nil {
		// O marcador só afeta a capacidade de rollback automático; o arquivo staged já está no lugar, então isso não interrompe a atualização.
		prog(PhaseStaged, -1, "Aviso: falha ao gravar o marcador de atualização; esta atualização ficará sem proteção de rollback automático")
	}

	prog(PhaseStaged, 100, "Nova versão pronta, reiniciando…")
	return nil
}

// fetchSums baixa e interpreta o SHA256SUMS, retornando nome do arquivo → digest hexadecimal.
func fetchSums(ctx context.Context, c *http.Client, rel *Release) (map[string]string, error) {
	asset, ok := rel.FindAsset(sumsAsset)
	if !ok {
		return nil, fmt.Errorf("esta Release não tem %s; não é possível verificar a integridade, atualização recusada", sumsAsset)
	}
	body, err := get(ctx, c, asset.downloadURL())
	if err != nil {
		return nil, fmt.Errorf("baixar %s: %w", sumsAsset, err)
	}
	defer body.Close()

	raw, err := io.ReadAll(io.LimitReader(body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("ler %s: %w", sumsAsset, err)
	}
	out := parseSums(string(raw))
	if len(out) == 0 {
		return nil, fmt.Errorf("o conteúdo de %s está vazio ou em formato não reconhecido", sumsAsset)
	}
	return out, nil
}

// parseSums interpreta uma lista no estilo sha256sum, retornando nome do arquivo → digest hexadecimal.
//
// Só é aceita a entrada cujo primeiro campo seja hexadecimal de 64 dígitos. Julgar apenas por "exatamente dois campos" não basta —
// qualquer linha de texto explicativo com duas palavras seria tomada como entrada válida, colocando valores lixo na tabela de digests,
// e o asset verdadeiro poderia casar com o digest errado.
func parseSums(raw string) map[string]string {
	out := map[string]string{}
	for line := range strings.Lines(raw) {
		// O formato é "<sha256>  <filename>" (sha256sum usa dois espaços; o modo binário do shasum
		// acrescenta o prefixo * ao nome do arquivo).
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) != 2 || !isHexSHA256(fields[0]) {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == "" {
			continue
		}
		out[name] = strings.ToLower(fields[0])
	}
	return out
}

func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// download grava o asset em dst, calculando o SHA256 ao mesmo tempo e reportando o progresso conforme o Content-Length.
func download(ctx context.Context, c *http.Client, a Asset, dst string, prog Progress) (string, error) {
	body, err := get(ctx, c, a.downloadURL())
	if err != nil {
		return "", fmt.Errorf("baixar %s: %w", a.Name, err)
	}
	defer body.Close()

	f, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("criar arquivo temporário: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	pw := &progressWriter{total: a.Size, prog: prog, name: a.Name, last: time.Now()}
	if _, err := io.Copy(io.MultiWriter(f, h, pw), body); err != nil {
		return "", fmt.Errorf("download interrompido: %w", err)
	}
	if err := f.Sync(); err != nil {
		return "", fmt.Errorf("falha ao gravar em disco: %w", err)
	}
	if a.Size > 0 && pw.written != a.Size {
		return "", fmt.Errorf("download incompleto: esperado %d bytes, obtido %d bytes", a.Size, pw.written)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// get faz um GET restrito pela lista de domínios permitidos, retornando o corpo da resposta.
func get(ctx context.Context, c *http.Client, rawURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if err := checkURL(req.URL); err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "artex-selfupdate")
	req.Header.Set("Accept", "application/octet-stream")
	setUpdateAuth(req)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// extractBinary extrai o executável artex do pacote de release.
//
// A estrutura interna do pacote é artex-<versão>-<os>-<arch>/artex, mas aqui a busca é pelo **nome base** e não por um caminho
// completo montado: o número da versão aparece uma vez no nome do pacote, e errar um caractere derrubaria toda a atualização; buscar pelo nome base é mais tolerante a mudanças.
func extractBinary(zipPath, dst string) error {
	want := "artex"
	if runtime.GOOS == "windows" {
		want = "artex.exe"
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("abrir o pacote de release: %w", err)
	}
	defer zr.Close()

	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() || !strings.EqualFold(path.Base(entry.Name), want) {
			continue
		}
		rc, err := entry.Open()
		if err != nil {
			return fmt.Errorf("ler %s: %w", entry.Name, err)
		}
		defer rc.Close()

		f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return fmt.Errorf("gravar o novo binário: %w", err)
		}
		defer f.Close()

		n, err := io.Copy(f, io.LimitReader(rc, maxBinarySize+1))
		if err != nil {
			return fmt.Errorf("extrair %s: %w", entry.Name, err)
		}
		if n > maxBinarySize {
			return fmt.Errorf("o executável dentro do pacote de release excede %s; extração recusada", humanSize(maxBinarySize))
		}
		if n == 0 {
			return fmt.Errorf("o arquivo %s dentro do pacote de release está vazio", want)
		}
		return f.Sync()
	}
	return fmt.Errorf("%s não encontrado no pacote de release", want)
}

// checkWritable confirma antecipadamente que o diretório é gravável. Sem esta etapa, ao rodar sem root ou com o binário em um diretório
// do sistema, a falha só ocorreria no momento da troca, depois de baixar dezenas de MB.
func checkWritable(dir string) error {
	probe, err := os.CreateTemp(dir, ".artex-update-probe-*")
	if err != nil {
		return fmt.Errorf("o diretório do programa %s não é gravável; não é possível atualizar automaticamente (verifique as permissões ou use a atualização manual): %w", dir, err)
	}
	name := probe.Name()
	_ = probe.Close()
	_ = os.Remove(name)
	return nil
}

// progressWriter conta os bytes gravados e limita a frequência dos relatórios, evitando enviar um evento SSE a cada bloco de 32KiB.
type progressWriter struct {
	total   int64
	written int64
	name    string
	prog    Progress
	last    time.Time
}

func (w *progressWriter) Write(b []byte) (int, error) {
	w.written += int64(len(b))
	if time.Since(w.last) < 300*time.Millisecond {
		return len(b), nil
	}
	w.last = time.Now()
	pct := -1
	if w.total > 0 {
		pct = int(w.written * 100 / w.total)
	}
	w.prog(PhaseDownload, pct, fmt.Sprintf("Baixando %s / %s", humanSize(w.written), humanSize(w.total)))
	return len(b), nil
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

func short(sum string) string {
	if len(sum) > 12 {
		return sum[:12] + "…"
	}
	return sum
}
