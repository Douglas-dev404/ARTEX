package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Repo é a origem das releases. Fixo no código e não configurável: tornar a origem de atualização configurável daria a qualquer pessoa que possa alterar a configuração um canal de
// execução remota de código, e numa plataforma de teste de intrusão essa porta não pode existir.
const Repo = "Hinln/ARTEX"

// GitHubTokenEnv é lido apenas pelo processo de backend; o token não passa pelo front-end nem pela API de configuração.
const GitHubTokenEnv = "ARTEX_UPDATE_GITHUB_TOKEN"

// latestURL é a API do GitHub para a "última versão estável". Ela ignora prerelease e draft automaticamente.
const latestURL = "https://api.github.com/repos/" + Repo + "/releases/latest"

// allowedHosts limita os domínios que a cadeia de atualização pode acessar. Em conjunto com o checkRedirect abaixo,
// qualquer redirecionamento para um host fora da lista falha imediatamente — esta é a primeira barreira contra envenenamento de DNS / man-in-the-middle
// trocando o binário; a segunda é a comparação do SHA256SUMS.
var allowedHosts = map[string]bool{
	"api.github.com":                       true,
	"github.com":                           true,
	"objects.githubusercontent.com":        true, // armazenamento de objetos onde os assets da release realmente ficam
	"release-assets.githubusercontent.com": true,
	"raw.githubusercontent.com":            true,
}

// Release contém os campos do GitHub Release que nos interessam.
type Release struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Assets      []Asset   `json:"assets"`
}

// Asset é um arquivo anexado a uma Release.
type Asset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	APIURL string `json:"url"`
	Size   int64  `json:"size"`
}

// Anexos de repositório privado precisam ser baixados com autenticação pela Release Asset API.
func (a Asset) downloadURL() string {
	if a.APIURL != "" {
		return a.APIURL
	}
	return a.URL
}

// setUpdateAuth envia o token apenas à API HTTPS do repositório de releases fixo. Quando o anexo é redirecionado
// para o armazenamento de objetos do GitHub, usa-se URL assinada, e o Authorization não deve ser encaminhado junto.
func setUpdateAuth(req *http.Request) {
	req.Header.Del("Authorization")
	u := req.URL
	if u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "api.github.com") ||
		(u.Port() != "" && u.Port() != "443") || u.User != nil ||
		!strings.HasPrefix(strings.ToLower(u.EscapedPath()), strings.ToLower("/repos/"+Repo+"/releases/")) {
		return
	}
	if token := strings.TrimSpace(os.Getenv(GitHubTokenEnv)); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

// NewClient constrói um cliente HTTP que só aceita domínios do GitHub. Se proxy estiver vazio, conecta direto.
//
// Deliberadamente não reutiliza o Transport padrão: a cadeia de atualização precisa forçar TLS e validar o certificado, sem ser afetada por
// InsecureSkipVerify e similares definidos em outros lugares.
func NewClient(proxy string) *http.Client {
	tr := &http.Transport{
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 15 * time.Second,
	}
	if p := strings.TrimSpace(proxy); p != "" {
		if pu, err := url.Parse(p); err == nil {
			tr.Proxy = http.ProxyURL(pu)
		}
	}
	return &http.Client{
		Transport: tr,
		Timeout:   30 * time.Minute, // baixa o pacote inteiro; não pode travar por timeout em nível de requisição
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("redirecionamentos demais")
			}
			if err := checkURL(req.URL); err != nil {
				return err
			}
			setUpdateAuth(req)
			return nil
		},
	}
}

// checkURL força https + lista de domínios permitidos.
func checkURL(u *url.URL) error {
	if u.Scheme != "https" {
		return fmt.Errorf("endereço não HTTPS recusado: %s", u.Scheme+"://"+u.Host)
	}
	if !allowedHosts[strings.ToLower(u.Hostname())] {
		return fmt.Errorf("domínio fora do GitHub recusado: %s", u.Hostname())
	}
	return nil
}

// FetchLatest consulta a última versão estável.
func FetchLatest(ctx context.Context, c *http.Client) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return nil, err
	}
	if err := checkURL(req.URL); err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "artex-selfupdate")
	setUpdateAuth(req)

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("falha ao acessar o GitHub (é possível configurar um proxy global nas configurações do sistema): %w", err)
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, fmt.Errorf("falha de autenticação no GitHub; verifique a variável de ambiente do backend %s", GitHubTokenEnv)
	case resp.StatusCode == http.StatusForbidden:
		return nil, fmt.Errorf("o GitHub negou o acesso: verifique a permissão de leitura de Contents do repositório no token ou a cota da API")
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, fmt.Errorf("limite de requisições da API do GitHub atingido, tente novamente mais tarde")
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("o repositório %s está inacessível ou ainda não publicou versão estável; para repositório privado, defina %s no backend (requer permissão de leitura de Contents)", Repo, GitHubTokenEnv)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("GitHub retornou %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("falha ao interpretar a Release: %w", err)
	}
	if strings.TrimSpace(rel.TagName) == "" {
		return nil, fmt.Errorf("Release sem tag")
	}
	return &rel, nil
}

// AssetName retorna o nome do pacote de release da plataforma atual, consistente com o package_binary do build.sh:
// artex-<versão>-<os>-<arch>.zip (o número da versão não leva o prefixo v).
func AssetName(tag, goos, goarch string) string {
	return fmt.Sprintf("artex-%s-%s-%s.zip", strings.TrimPrefix(tag, "v"), goos, goarch)
}

// FindAsset procura um asset na Release pelo nome.
func (r *Release) FindAsset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if strings.EqualFold(a.Name, name) {
			return a, true
		}
	}
	return Asset{}, false
}
