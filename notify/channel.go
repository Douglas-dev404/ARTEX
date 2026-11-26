package notify

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// Channel é o adaptador de um canal de notificação. A implementação deve ser **sem estado**: a mesma instância é reutilizada
// de forma concorrente por várias configurações de canal, e as credenciais sempre chegam pelo parâmetro cfg.
type Channel interface {
	// Kind retorna o identificador do tipo de canal, que deve coincidir com a chave do registro.
	Kind() string
	// Validate é chamado ao salvar a configuração e valida campos obrigatórios e formato. O erro retornado é exibido
	// diretamente a quem configura, então a mensagem deve dizer "qual campo falta" e não um genérico "configuração inválida".
	Validate(cfg map[string]any) error
	// Send entrega uma mensagem e retorna o **número de itens efetivamente entregues** e um erro.
	//
	// Por que retornar a contagem: cada plataforma tem um limite de tamanho de mensagem, e uma mensagem consolidada que não
	// cabe inteira é truncada. Se o chamador marcasse o lote inteiro como entregue sem condição, os itens cortados
	// desapareceriam: não apareceriam na mensagem, o histórico de entregas mostraria sucesso, e não haveria nenhum lugar
	// onde notar que a vulnerabilidade nunca foi enviada. Com kept retornado, o chamador marca só os primeiros kept itens e deixa o restante para o próximo lote.
	//
	// Um erro retornado indica falha de entrega; *PermanentError indica que não se deve tentar novamente.
	// Em caso de falha, kept não tem significado e o chamador deve ignorá-lo.
	Send(ctx context.Context, cfg map[string]any, m Message) (int, error)
	// DefaultRatePerMin retorna o limite de entregas por minuto sugerido oficialmente para o canal, usado como valor
	// padrão de limitação de taxa ao criar uma nova instância de canal. Retornar 0 significa que não há limite conhecido.
	DefaultRatePerMin() int
	// SecretKeys retorna os nomes das chaves da configuração do canal que são credenciais. Quando a API devolve a
	// configuração, os valores dessas chaves são mascarados; ao atualizar, se o valor recebido for o mascarado, preserva-se
	// o valor original do banco. Só a própria implementação sabe quais campos contam como credencial
	// (no WeCom o endereço inteiro do Webhook é a credencial, enquanto no DingTalk é apenas o secret), então esse conhecimento deve vir do canal e não pode ser adivinhado pela camada superior.
	SecretKeys() []string
	// DestinationKeys retorna os nomes das chaves da configuração do canal que definem "para onde a mensagem vai".
	//
	// Assim como SecretKeys, é algo relevante para a segurança: o endereço de destino e a credencial são dois conjuntos
	// independentes de campos. Se fosse permitido "alterar só o endereço e manter a credencial como está", qualquer pessoa
	// que pudesse alterar a configuração do canal poderia enviar a credencial real do banco a um servidor sob seu controle,
	// e a máscara da configuração do canal perderia todo o sentido. Detalhes em PrepareConfigUpdate.
	DestinationKeys() []string
}

// registry é o registro de canais. Usa deliberadamente um literal explícito em vez de auto-registro via init(): assim "quais canais existem"
// pode ser visto por inteiro em um só lugar, e um canal novo expõe omissões em tempo de compilação, em vez de depender de efeitos colaterais em tempo de execução.
var registry = map[string]Channel{
	KindDingTalk: dingTalkChannel{},
	KindFeishu:   feishuChannel{},
	KindWeCom:    weComChannel{},
	KindWebhook:  webhookChannel{},
	KindTelegram: telegramChannel{},
	KindEmail:    emailChannel{},
}

// Get obtém a implementação do canal pelo tipo.
func Get(kind string) (Channel, bool) {
	c, ok := registry[kind]
	return c, ok
}

// ValidKind informa se kind é um tipo de canal suportado.
func ValidKind(kind string) bool {
	_, ok := registry[kind]
	return ok
}

// Kinds retorna todos os tipos de canal suportados, em ordem lexicográfica (para exibição estável no dropdown da UI).
func Kinds() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PermanentError marca uma falha de entrega que não deve ser repetida: credencial errada, destino que recusa, corpo da requisição inválido etc.
// Tentar de novo só faz sentido para falhas transitórias (instabilidade de rede, limitação de taxa, 5xx do outro lado); repetir com recuo
// uma falha permanente não terá sucesso e ainda enterra o erro real no log de tentativas.
type PermanentError struct{ Err error }

func (e *PermanentError) Error() string { return e.Err.Error() }
func (e *PermanentError) Unwrap() error { return e.Err }

// Permanent marca err como falha permanente. Quando err é nil, retorna nil,
// o que permite escrever `return Permanent(someCheck())`.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return &PermanentError{Err: err}
}

// IsPermanent informa se a cadeia de err contém a marca de falha permanente.
func IsPermanent(err error) bool {
	var pe *PermanentError
	return errors.As(err, &pe)
}

// ---- helpers de leitura de configuração ----
//
// A configuração do canal vem de uma coluna JSONB do banco e, após a desserialização por encoding/json, é um map[string]any,
// com números sempre float64 e arrays []any. Os helpers abaixo unificam essa conversão e toleram
// desvios de tipo causados por campos deixados em branco na UI (como uma porta preenchida como string).

// cfgString obtém um item de configuração string, sempre removendo espaços nas pontas, pois é fácil copiar e colar de um formulário web trazendo-os.
func cfgString(cfg map[string]any, key string) string {
	v, ok := cfg[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// cfgInt obtém um item de configuração inteiro, aceitando as duas origens: float64 (padrão do JSON) e string.
func cfgInt(cfg map[string]any, key string) int {
	switch v := cfg[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0
		}
		return n
	default:
		return 0
	}
}

// cfgBool obtém um item de configuração booleano, aceitando as strings "true"/"1".
func cfgBool(cfg map[string]any, key string) bool {
	switch v := cfg[key].(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s == "true" || s == "1" || s == "yes"
	default:
		return false
	}
}

// cfgStrings obtém um item de configuração array de strings, removendo espaços e descartando strings vazias.
func cfgStrings(cfg map[string]any, key string) []string {
	raw, ok := cfg[key].([]any)
	if !ok {
		// também aceita uma única string, facilitando o envio de formulário quando há só um valor.
		if s := cfgString(cfg, key); s != "" {
			return []string{s}
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// cfgMap obtém um item de configuração mapa de strings (como cabeçalhos HTTP personalizados), removendo espaços de chave e valor e descartando chaves vazias.
func cfgMap(cfg map[string]any, key string) map[string]string {
	raw, ok := cfg[key].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		out[k] = s
	}
	return out
}
