package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
	"time"
)

// allowLocalTargets decide se é permitido entregar mensagens a endereços de loopback / link-local.
//
// Por padrão é recusado. Esses blocos de endereços não são lugares onde apareceriam robôs de IM ou servidores de e-mail públicos, e o que se
// consegue alcançar com eles é sensível: a porta de administração de outro serviço na mesma máquina e o endpoint de metadados do ambiente de nuvem
// (169.254.169.254, de onde se podem ler credenciais da instância). O endereço de entrega é configurado pelo administrador, mas uma sessão administrativa
// emprestada por XSS/CSRF, ou uma segunda pessoa que compartilhe o mesmo JWT, poderia alterar a configuração para ler o conteúdo da resposta de volta
// — o doJSON grava os primeiros 200 bytes do corpo da resposta 4xx/5xx em last_error, e a interface de histórico de entregas
// o ecoa, o que constitui uma primitiva de leitura semi-cega.
//
// Mas o "relay SMTP local" (postfix em 127.0.0.1:25) é uma configuração comum de e-mail próprio,
// e uma proibição total travaria as pessoas. Por isso deixamos uma saída de emergência explícita em vez de liberar de forma fixa no código:
// definir ARTEX_NOTIFY_ALLOW_LOCAL=1 permite.
//
// É exportado como AllowLocalTargetsEnv para que os testes possam ativá-lo de forma explícita — os casos de teste deste pacote e do pacote server
// usam intensamente receptores falsos httptest em 127.0.0.1, e sem ativá-lo todos seriam barrados pela guarda.
const AllowLocalTargetsEnv = "ARTEX_NOTIFY_ALLOW_LOCAL"

func allowLocalTargets() bool {
	v := strings.TrimSpace(os.Getenv(AllowLocalTargetsEnv))
	return v == "1" || strings.EqualFold(v, "true")
}

// isBlockedDialIP informa se o IP de destino pertence aos blocos de endereços "cuja entrega não é permitida por padrão".
//
// Recusa apenas loopback, link-local (incluindo os metadados de nuvem 169.254.169.254), não especificado e multicast.
// **Não recusa** as redes privadas RFC1918: Mattermost / relay SMTP próprios na rede interna são usos legítimos muito comuns,
// e barrá-los junto tornaria o recurso simplesmente inutilizável em ambientes reais. Esta troca é deliberada —
// a proteção deve barrar os alvos realmente sensíveis, sem inviabilizar junto as implantações normais.
func isBlockedDialIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// IPv6 mapeado em IPv4 (::ffff:127.0.0.1) deve ser convertido de volta para IPv4 antes do julgamento, senão contorna a verificação.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// blockInternalDial é o gancho Control do dialer do http.Transport, que verifica o endereço de destino
// **no momento do estabelecimento da conexão**.
//
// Por que fica na fase de conexão e não apenas na validação ao salvar a configuração: é aqui o ponto em que a proteção de fato vale.
// Ele cobre ao mesmo tempo os dois casos que contornam a validação da configuração — DNS rebinding (na validação resolve para um IP público,
// e na conexão de verdade resolve para a rede interna) e redirecionamento (embora já recusemos saltos entre hosts diferentes, um salto no
// mesmo host ainda pode apontar o caminho para outro lugar).
func blockInternalDial(_, address string, _ syscall.RawConn) error {
	if allowLocalTargets() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("não foi possível interpretar o endereço de destino %q", host)
	}
	if isBlockedDialIP(ip) {
		return fmt.Errorf("entrega recusada para endereço local/link-local %s (se for realmente necessário entregar a um serviço local, defina %s=1)", ip, AllowLocalTargetsEnv)
	}
	return nil
}

// notifyTransport acrescenta apenas uma guarda de conexão sobre o Transport padrão.
// Usa Clone para preservar todo o ajuste fino padrão (pool de conexões, HTTP/2, timeouts, proxy etc.),
// evitando alterar outros comportamentos só para adicionar uma verificação.
var notifyTransport = func() *http.Transport {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Transport{}
	}
	clone := t.Clone()
	clone.DialContext = (&net.Dialer{Timeout: 10 * time.Second, Control: blockInternalDial}).DialContext
	return clone
}()

// httpClient é o cliente compartilhado por todas as entregas dos canais.
//
// Deliberadamente **não** reutiliza o proxy de saída global do projeto (GlobalProxy no lado do server): esse proxy serve ao tráfego
// dos alvos do teste de intrusão, costuma ser um túnel instável, e a disponibilidade das notificações não deve ficar refém da oscilação da rede do alvo.
// O push de IM pode ir direto. O timeout é de 15 segundos — um par mais lento que isso na prática já está em estado de falha.
//
// Recusa redirecionamentos entre hosts diferentes: os endereços de entrega deste recurso têm a forma de "um endpoint fixo" e normalmente não
// redirecionam para outro host; e as credenciais desses canais (access_token do DingTalk, key do WeCom, bot token do Telegram)
// **estão na própria URL**, então seguir um salto entre hosts equivale a entregar a credencial ao destino do redirecionamento. Saltos no mesmo host
// (como acrescentar uma barra no final) continuam permitidos.
var httpClient = &http.Client{
	Timeout:   15 * time.Second,
	Transport: notifyTransport,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("redirecionamentos demais")
		}
		if len(via) > 0 && req.URL.Host != via[0].URL.Host {
			return fmt.Errorf("redirecionamento entre hosts recusado (%s → %s)", via[0].URL.Host, req.URL.Host)
		}
		return nil
	},
}

// respBodyLimit limita o tamanho lido do corpo da resposta. Quando o outro lado se comporta de forma anormal, pode devolver conteúdo enorme, e só precisamos
// do código de erro e de uma pequena descrição do erro para exibir no histórico de entregas.
const respBodyLimit = 8 << 10

// doJSON envia uma requisição e retorna o corpo da resposta (já com tamanho limitado).
//
// Quando payload é nil, envia body vazio (para GET ou plataformas que não exigem body).
// Chaves e valores em headers são acrescentados como estão, para os cabeçalhos personalizados do Webhook genérico.
//
// A classificação de erros é a responsabilidade central desta função: falhas na camada de rede e 5xx/408/429 são classificadas como "passíveis de nova tentativa",
// e os demais 4xx como "falha permanente" — repetir um 403 só faz o mesmo erro aparecer 3 vezes no log.
func doJSON(ctx context.Context, method, url string, headers map[string]string, payload any) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			// Falha de serialização é um bug local (tipo do campo de configuração incorreto), e repetir não vai melhorar.
			return nil, Permanent(fmt.Errorf("falha ao montar o corpo da requisição: %w", err))
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		// URL inválida — na maioria das vezes o usuário preencheu o endereço errado, o que é falha permanente.
		// Aqui também não se pode repassar err: o texto de erro do url.Parse contém o endereço completo.
		return nil, Permanent(fmt.Errorf("endereço da requisição inválido: %s", redactRequestTarget(url)))
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		// Conexão recusada, falha de DNS, timeout — na maioria falhas transitórias, entregues ao retry com recuo.
		//
		// O texto do erro deve ser ofuscado antes de ser repassado. Motivo: o que o http.Client.Do retorna é um *url.Error,
		// cujo Error() é `Op "URL completa": erro subjacente`, e as credenciais destes canais **estão na URL**
		// (access_token do DingTalk, key do WeCom, hook id do Feishu, /bot<token>/ do Telegram).
		// Sem ofuscar, a credencial escorreria por essa string de erro até quatro lugares: o last_error de notification_deliveries
		// (gravado em texto claro no banco), a resposta da interface de histórico de entregas (**contornando a máscara da configuração do canal**),
		// o log do servidor e o texto 502 que a interface de teste de envio devolve ao front-end.
		return nil, fmt.Errorf("falha na requisição: %s", redactTransportError(err))
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, respBodyLimit))
	if readErr != nil {
		return nil, fmt.Errorf("falha ao ler a resposta: %w", readErr)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return raw, nil
	}
	// 429 (limitação de taxa) e 408 (timeout) merecem nova tentativa; os demais 4xx são problemas de configuração ou de permissão, e repetir não adianta.
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusRequestTimeout {
		return nil, fmt.Errorf("limitação de taxa ou timeout do outro lado (HTTP %d): %s", resp.StatusCode, snippet(raw))
	}
	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("falha de serviço do outro lado (HTTP %d): %s", resp.StatusCode, snippet(raw))
	}
	return nil, Permanent(fmt.Errorf("o outro lado recusou a requisição (HTTP %d): %s", resp.StatusCode, snippet(raw)))
}

// snippet comprime o corpo da resposta em um texto curto de uma linha, para uso em mensagens de erro. A resposta pode conter quebras de linha e muito espaço em branco,
// e gravá-la direto em last_error estragaria o layout da página de histórico de entregas.
func snippet(raw []byte) string {
	return OneLine(string(raw), 200)
}

// redactRequestTarget comprime o endereço de entrega em "scheme://host/…", para uso em mensagens de erro.
//
// Esta é a única forma de ofuscação de endereços neste pacote, deliberadamente feita de modo **bem grosseiro**: além de scheme e host,
// todo o resto é descartado. O motivo é que não existe uma forma "genérica e segura" de saber qual trecho da URL é a credencial:
//
//	DingTalk   credencial na query      /robot/send?access_token=xxx
//	WeCom      credencial na query      /cgi-bin/webhook/send?key=xxx
//	Feishu     credencial no **último trecho do path** /open-apis/bot/v2/hook/<hook_id>
//	Telegram   credencial no **trecho do meio do path** /bot<token>/sendMessage
//
// Tentar "manter só a parte útil" exigiria remendos por canal, e esquecer qualquer um deles seria um vazamento de credencial.
// Manter o host já basta para investigar (DNS que não resolve, falha de conexão, certificado errado, tudo se localiza),
// e qual é o robô específico se reconhece pela dica dos últimos caracteres mascarados na configuração do canal.
//
// Em caso de falha de interpretação retorna um placeholder fixo — jamais ecoa a string original.
func redactRequestTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "(endereço não interpretável)"
	}
	return u.Scheme + "://" + u.Host + "/…"
}

// redactTransportError remove o endereço do erro da camada de transporte, mantendo apenas a causa subjacente.
//
// A estrutura de *url.Error é {Op, URL, Err}, e o Error() imprime a URL junto.
// Aqui se obtém explicitamente o campo Err, contornando o seu Error() — mais confiável do que fazer substituição de string depois,
// porque a substituição teria de lidar corretamente com todas as variantes de codificação/escape de URL, e é fácil deixar escapar alguma.
func redactTransportError(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		host := ""
		if u, parseErr := url.Parse(uerr.URL); parseErr == nil {
			host = u.Host
		}
		if uerr.Err != nil {
			return fmt.Sprintf("%s %s: %s", uerr.Op, host, uerr.Err)
		}
		return fmt.Sprintf("%s %s: erro desconhecido", uerr.Op, host)
	}
	// Erros que não são *url.Error (como o retornado pela política de redirecionamento) também podem conter endereço, e todos passam pela ofuscação.
	return redactURLsInText(err.Error())
}

// redactURLsInText substitui os endereços http(s) que aparecem num trecho de texto pela forma ofuscada.
//
// Usado como rede de segurança para erros dos quais não se consegue obter campos estruturados (erros da política de redirecionamento, erros personalizados de bibliotecas de terceiros).
// Reconhece apenas o prefixo http/https e divide por espaço em branco e aspas — um endereço não contém esses dois tipos de caracteres.
func redactURLsInText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		rest := s[i:]
		if strings.HasPrefix(rest, "http://") || strings.HasPrefix(rest, "https://") {
			end := len(rest)
			if j := strings.IndexAny(rest, " \t\n\"'"); j >= 0 {
				end = j
			}
			b.WriteString(redactRequestTarget(rest[:end]))
			i += end
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
