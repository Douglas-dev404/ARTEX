package notify

import (
	"strings"
	"unicode/utf8"
)

const ellipsis = "…"

// TruncateBytes trunca s para no máximo max bytes, garantindo que o resultado seja UTF-8 válido e não corte um caractere ao meio.
//
// Por que é obrigatório cortar na fronteira de caractere: o markdown do robô de grupo do WeCom tem um limite rígido de 4096 **bytes** (não
// de caracteres), e um caractere chinês ocupa 3 bytes. Cortar o slice direto por bytes parte um caractere em dois, gerando
// UTF-8 inválido — a plataforma ou rejeita a mensagem inteira ou exibe quadrados ilegíveis. A abordagem aqui é, a partir da posição do orçamento,
// recuar até o byte inicial de rune mais próximo (utf8.RuneStart identifica os bytes de continuação 0b10xxxxxx).
//
// max<=0 significa sem limite. Após o truncamento acrescenta reticências, a menos que max seja tão pequeno que não caibam as reticências.
func TruncateBytes(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	budget := max - len(ellipsis)
	suffix := ellipsis
	if budget < 0 {
		// max é menor que as próprias reticências: abandona as reticências e faz truncamento puro, evitando que o resultado acabe excedendo max.
		budget = max
		suffix = ""
	}
	cut := budget
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + suffix
}

// OneLine comprime texto de várias linhas em uma única linha: colapsa todo o espaço em branco e depois trunca por número de caracteres.
// Usado na linha de título das mensagens de IM — o resumo costuma ter quebras de linha, e enfiá-lo direto numa tabela/título estraga o layout.
// max<=0 significa sem limite de tamanho.
func OneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	return TruncateRunes(s, max)
}

// TruncateRunes trunca s para no máximo max caracteres (e não bytes), acrescentando reticências quando excede.
// max<=0 significa sem limite.
//
// A diferença para TruncateBytes está no critério da plataforma: o WeCom limita por bytes e o Telegram limita por número de caracteres.
// Usar o critério errado não gera erro, apenas faz a mensagem ser cortada muito mais curta que o esperado (em chinês 1 caractere = 3 bytes,
// cortando 4096 por bytes sobram só cerca de 1365 caracteres), por isso as duas funções devem ser mantidas e escolhidas conforme o canal.
func TruncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + ellipsis
}

// TruncateHTML trunca um fragmento HTML por número de caracteres e garante que não produza uma tag cortada pela metade.
//
// Truncar HTML direto por caracteres pode cortar uma tag incompleta como `<a href="htt`, e o parser da plataforma ou
// rejeita a mensagem inteira, ou engole o corpo seguinte como se fosse valor de atributo. A abordagem aqui é: primeiro truncar por caracteres,
// depois verificar se o final tem um `<` não fechado, e se tiver recuar até antes dele.
//
// Não faz balanceamento de tags (completar </b> e semelhantes): o parser HTML do Telegram fecha automaticamente as tags não fechadas,
// e implementar o balanceamento por conta própria exigiria tratar aspas em atributos, comentários e tags autofechadas, com complexidade desproporcional ao benefício.
func TruncateHTML(s string, max int) string {
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	cut := TruncateRunes(s, max)
	// Se o final é um fragmento começando com `<` (depois do último `<` não há `>`), recua até antes do `<`.
	if lt := strings.LastIndex(cut, "<"); lt >= 0 && !strings.Contains(cut[lt:], ">") {
		cut = cut[:lt]
	}
	// Se o final é uma entidade HTML cortada (como `&amp;` cortada em `&amp`), também é preciso recuar.
	// Um fragmento de entidade, num parser que só reconhece entidades, pode fazer **a mensagem inteira** ser rejeitada — uma mensagem consolidada que
	// excede o limite de tamanho já é comum, e não vale a pena perder a notificação inteira por isso.
	if amp := strings.LastIndex(cut, "&"); amp >= 0 && !strings.Contains(cut[amp:], ";") {
		cut = cut[:amp]
	}
	return cut
}

// packItemCount calcula quantos itens cabem **por inteiro** dentro do orçamento, para o empacotamento de mensagens consolidadas por item inteiro.
//
// Por que por item inteiro e não renderizar o texto completo e truncar: o truncamento faz os itens da segunda metade sumirem do nada,
// enquanto seus registros de entrega continuam sendo marcados como entregues — não se vê na mensagem nem no histórico de entregas,
// e a vulnerabilidade simplesmente se perde. Com o empacotamento por item inteiro, os itens que não cabem ficam no banco e viram o próximo lote,
// e o kept que o chamador recebe é o número de itens realmente entregues nesta mensagem.
//
// Parâmetros: maxSize<=0 significa sem limite; reserve é o espaço reservado para o cabeçalho/rodapé da mensagem;
// size é responsável pela medição (o critério varia por plataforma: WeCom/DingTalk medem em bytes, Telegram em caracteres —
// usar o critério errado não gera erro, apenas comprime a mensagem em chinês para muito abaixo do limite);
// render renderiza o item idx como o seu texto real — o tamanho varia com o conteúdo e não pode ser estimado.
//
// Retorna pelo menos 1 (enquanto ainda houver itens). Mesmo quando um único item é extremamente longo, ele deve ser enviado, ficando para o
// truncamento final do chamador cobrir; caso contrário um item longo demais prenderia o lote inteiro no mesmo lugar para sempre.
func packItemCount(items []Item, maxSize, reserve int, footer string, size func(string) int, render func(Item, int) string) int {
	if maxSize <= 0 {
		return len(items)
	}
	budget := maxSize - reserve - size(footer)
	if budget < 0 {
		budget = 0
	}
	used := 0
	for i, it := range items {
		used += size(render(it, i))
		if used > budget && i > 0 {
			return i
		}
	}
	return len(items)
}

// byteSize / runeSize são os dois critérios de medição de packItemCount; dar nome a eles evita que os pontos de chamada
// tenham closures soltas func(s string) int, o que dificultaria ver de relance qual critério está sendo usado.
func byteSize(s string) int { return len(s) }
func runeSize(s string) int { return utf8.RuneCountInString(s) }

// assetLine renderiza a lista de ativos em uma linha de texto de exibição; ao passar de limit itens, omite o restante e indica o total.
// Uma vulnerabilidade pode estar ancorada em dezenas de ativos, e listar todos estouraria a mensagem.
func assetLine(assets []string, limit int) string {
	if len(assets) == 0 {
		return ""
	}
	if limit <= 0 || len(assets) <= limit {
		return strings.Join(assets, ", ")
	}
	return strings.Join(assets[:limit], ", ") + " (total: " + itoa(len(assets)) + ")"
}

// itoa é um alias curto de strconv.Itoa, usado só para montar o texto de exibição, evitando importar strconv por toda parte.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
