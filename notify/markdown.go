package notify

import (
	"fmt"
	"strings"
)

// Este arquivo é a renderização de mensagem compartilhada pelos canais "da família Markdown" (DingTalk, WeCom).
// O Feishu usa JSON de cartão, o Telegram usa HTML e o e-mail usa HTML, cada um renderizado no seu próprio adaptador.

// maxAssetsShown é quantos ativos no máximo são listados na mensagem. Uma vulnerabilidade pode estar ancorada em dezenas de ativos,
// e listar todos estouraria a mensagem sem valor informativo — ninguém vai olhar no IM os domínios depois do 4º.
const maxAssetsShown = 3

// maxSummaryRunes é a quantidade de caracteres para a qual o resumo é comprimido. A mensagem de IM serve para "avisar para ver o detalhe",
// não é o relatório em si; o conteúdo completo está na plataforma.
const maxSummaryRunes = 120

// markdownReservedBytes é o espaço reservado para o cabeçalho da mensagem (linha de resumo + distribuição por severidade + possível aviso de truncamento)
// e para o rodapé (link da plataforma). No empacotamento por item inteiro, essa parte é descontada do orçamento, garantindo que cabeçalho e rodapé não sejam cortados —
// se cabeçalho e rodapé forem cortados, o leitor nem consegue ver "de qual lote é isso e quantos itens ainda não foram exibidos".
const markdownReservedBytes = 320

// markdownEscape escapa os metacaracteres do markdown.
//
// Por que é obrigatório: título, resumo, tipo e nome de exibição do ativo da vulnerabilidade vêm todos de **fontes não confiáveis** —
// título e resumo saem da saída do modelo (e o modelo lê as respostas do alvo testado), e a url do ativo é a URL completa obtida
// na varredura (incluindo a query string controlada pelo alvo). Sem escape, uma vulnerabilidade cujo título fosse
//
//	Login SQL Injection\n[Urgente: clique aqui para verificar a conta](http://attacker.tld)
//
// seria renderizada no DingTalk/Feishu do engenheiro de segurança como **um link externo clicável**; e
// `![](http://attacker.tld/beacon)` seria buscada pelo cliente na renderização, o que equivale a avisar
// "esta vulnerabilidade já foi vista" e vazar o IP de quem leu. Mesmo conteúdo sem má intenção, com negrito ou
// blocos de citação injetados, poderia empurrar as vulnerabilidades graves para baixo da linha de dobra.
//
// O conjunto de escape cobre os caracteres de título/link/ênfase/lista/citação/tachado que alteram a estrutura ou geram
// elementos clicáveis. `\` deve ser tratado primeiro, senão escaparia de novo as barras invertidas acrescentadas depois.
func markdownEscape(s string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		"`", "\\`",
		"*", `\*`,
		"_", `\_`,
		"[", `\[`,
		"]", `\]`,
		"(", `\(`,
		")", `\)`,
		"!", `\!`,
		"#", `\#`,
		">", `\>`,
		"|", `\|`,
		"~", `\~`,
	)
	return replacer.Replace(s)
}

// markdownText comprime texto não confiável em uma única linha e o escapa, para uso no corpo markdown.
// Reduzir a uma linha é a outra metade além do escape: a própria quebra de linha pode forjar um novo item de lista ou bloco de citação,
// e o escape de caracteres não a impede.
func markdownText(s string, maxRunes int) string {
	return markdownEscape(OneLine(s, maxRunes))
}

// markdownTitle retorna o título da mensagem (barra de título/título do cartão da plataforma de IM), cujo conteúdo é **o texto original sem escape**.
//
// Aqui deliberadamente não se faz escape: este título é compartilhado por renderizadores de quatro contextos — corpo markdown, HTML do Telegram,
// plain_text do cartão do Feishu, e o JSON do Webhook genérico e o assunto do e-mail. As regras de escape de cada contexto
// são diferentes (escape de markdown colocado em HTML deixa barras invertidas visíveis, colocado em JSON polui
// os dados), então o escape deve ficar a cargo de cada saída, veja writeItem / feishuItemLines /
// telegramEscape. Já se chegou a acrescentar escape de markdown numa função compartilhada, e o resultado foi
// `\(1\)`, uma barra invertida visível, aparecendo nas mensagens do Telegram.
func markdownTitle(m Message) string {
	if m.Batch {
		return fmt.Sprintf("Resumo de vulnerabilidades · total de %d", len(m.Items))
	}
	if len(m.Items) == 0 {
		return "Notificação de vulnerabilidade"
	}
	it := m.Items[0]
	return fmt.Sprintf("[%s] %s", SeverityLabel(it.Severity), OneLine(it.Title(), 0))
}

// markdownBody renderiza o corpo da mensagem e retorna o corpo e o **número de itens efetivamente escritos**.
//
// O valor de retorno kept é o número de itens que esta entrega realmente alcançou, e o chamador só marca os primeiros kept
// como entregues — os itens barrados fora pelo limite de tamanho do canal precisam ficar para o próximo lote, em vez de serem
// marcados como sucesso junto. Esta é justamente a origem da "perda silenciosa": a mensagem foi truncada, mas o registro de entrega mostra tudo entregue,
// sem nenhum lugar onde se veja que a segunda metade nunca foi enviada.
//
// maxBytes<=0 significa sem limite.
func markdownBody(m Message, maxBytes int) (string, int) {
	if !m.Batch {
		if len(m.Items) == 0 {
			return "", 0
		}
		var b strings.Builder
		writeItem(&b, m.Items[0], "", true)
		// Mesmo que uma única mensagem seja longa demais, ela é enviada assim mesmo (o truncamento final cobre): parte das informações de uma vulnerabilidade
		// é melhor do que não enviar nenhuma.
		return TruncateBytes(b.String(), maxBytes), 1
	}

	footer := ""
	if m.HomeURL != "" {
		footer = fmt.Sprintf("\n[Ver todos na plataforma](%s)\n", m.HomeURL)
	}
	kept := packItemCount(m.Items, maxBytes, markdownReservedBytes, footer, byteSize, func(it Item, idx int) string {
		var b strings.Builder
		writeItem(&b, it, fmt.Sprintf("%d. ", idx+1), false)
		return b.String()
	})

	items := m.Items[:kept]
	var b strings.Builder
	b.WriteString(markdownBatchIntro(m, items, len(m.Items)))
	for i, it := range items {
		writeItem(&b, it, fmt.Sprintf("%d. ", i+1), false)
	}
	b.WriteString(footer)
	return TruncateBytes(b.String(), maxBytes), kept
}

// markdownBatchIntro renderiza o início da mensagem consolidada: janela de tempo, contagem e distribuição por severidade.
// Com isso, quem recebe o consolidado consegue julgar se o lote precisa de atenção imediata sem abrir a plataforma.
//
// items são os itens **efetivamente acomodados** e total é o total que o lote deveria ter. Quando diferem, é preciso dizer explicitamente
// "quantos itens estão na próxima mensagem" — caso contrário o leitor acharia que o número escrito no cabeçalho é tudo,
// e os itens seguintes, nunca enviados, simplesmente não existiriam na interface.
func markdownBatchIntro(m Message, items []Item, total int) string {
	var b strings.Builder
	if m.WindowMinutes > 0 {
		fmt.Fprintf(&b, "**%[2]d nova(s) vulnerabilidade(s) nos últimos %[1]d minutos**", m.WindowMinutes, total)
	} else {
		fmt.Fprintf(&b, "**%d nova(s) vulnerabilidade(s)**", total)
	}
	if extra := total - len(items); extra > 0 {
		fmt.Fprintf(&b, " (esta mensagem mostra os primeiros %d; os outros %d seguem na próxima)", len(items), extra)
	}
	// Apresenta a distribuição por severidade, para o leitor ver de relance se há itens graves. Conta apenas os itens **realmente contidos nesta mensagem**,
	// garantindo que "grave 3" coincida com os itens que se conseguem contar abaixo.
	counts := map[string]int{}
	for _, it := range items {
		counts[it.Severity]++
	}
	var parts []string
	for _, sev := range []string{"critical", "high", "medium", "low"} {
		if n := counts[sev]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", SeverityLabel(sev), n))
		}
	}
	if len(parts) > 0 {
		b.WriteString("\n" + strings.Join(parts, " · "))
	}
	b.WriteString("\n\n")
	return b.String()
}

// writeItem renderiza um único item de vulnerabilidade.
//
// prefix serve para o número de ordem na lista consolidada; com single=true renderiza a versão completa (com resumo e link de retorno),
// e na lista consolidada renderiza só uma linha de resumo — senão um consolidado de 50 itens viraria um documento longo.
//
// Todo conteúdo vindo de fora (título/tipo/ativo/resumo) passa por markdownText:
// uma linha só + escape. O link de retorno é montado a partir de public_base_url, configurado pelo administrador, e não é conteúdo não confiável,
// além de precisar ser um link clicável, por isso é emitido como está.
func writeItem(b *strings.Builder, it Item, prefix string, single bool) {
	line := fmt.Sprintf("%s**%s · %s**", prefix, SeverityLabel(it.Severity), markdownText(it.Title(), 0))
	if !single {
		// Modo consolidado: apresentação em uma linha, com ativos e resumo comprimidos logo em seguida.
		var extras []string
		if a := assetLine(it.Assets, maxAssetsShown); a != "" {
			extras = append(extras, markdownText(a, 0))
		}
		if it.Summary != "" {
			extras = append(extras, markdownText(it.Summary, 60))
		}
		if len(extras) > 0 {
			line += " — " + strings.Join(extras, " · ")
		}
		b.WriteString(line + "\n")
		return
	}
	b.WriteString(line + "\n")
	if it.IsStatusChange() {
		fmt.Fprintf(b, "**Mudança de status**: %s → %s\n",
			markdownText(StatusLabel(it.FromStatus), 0), markdownText(StatusLabel(it.ToStatus), 0))
	}
	if it.VulnClass != "" && it.VulnClass != it.Title() {
		fmt.Fprintf(b, "**Tipo**: %s\n", markdownText(it.VulnClass, 0))
	}
	if a := assetLine(it.Assets, maxAssetsShown); a != "" {
		fmt.Fprintf(b, "**Ativos**: %s\n", markdownText(a, 0))
	}
	if it.Summary != "" {
		if s := markdownText(it.Summary, maxSummaryRunes); s != "" {
			fmt.Fprintf(b, "**Resumo**: %s\n", s)
		}
	}
	if it.DetailURL != "" {
		fmt.Fprintf(b, "[Ver detalhes](%s)\n", it.DetailURL)
	}
}
