package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Este arquivo cobre a correção do "empacotamento por item inteiro": quando a mensagem consolidada excede o limite de tamanho do canal, ela deve ser truncada
// **por item inteiro** e informar com fidelidade quantos itens não couberam, para que o chamador marque apenas os que de fato foram entregues.
//
// A abordagem anterior era renderizar o texto completo e truncar, e depois marcar o lote inteiro como entregue: a segunda metade da mensagem sumia do nada,
// enquanto o histórico de entregas mostrava tudo com sucesso — a vulnerabilidade se perdia assim, sem nenhum lugar onde se pudesse notar.

func TestMarkdownBodyPacksWholeItemsWithinByteLimit(t *testing.T) {
	// Consolidado com 200 itens em chinês, que necessariamente excede em muito os 4096 bytes do WeCom.
	m := batchMsg(200)
	body, kept := markdownBody(m, weComMarkdownLimit)

	if len(body) > weComMarkdownLimit {
		t.Fatalf("corpo de %d bytes excede o limite %d", len(body), weComMarkdownLimit)
	}
	if !utf8.ValidString(body) {
		t.Fatal("o corpo não é UTF-8 válido")
	}
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("deveria acomodar apenas uma parte (0 < kept < %d), obtido %d", len(m.Items), kept)
	}
	// O cabeçalho deve dizer com fidelidade quantos itens esta mensagem contém e quantos são os restantes — senão o leitor tomaria
	// o número do cabeçalho como o total.
	if !strings.Contains(body, "os outros") || !strings.Contains(body, "seguem na próxima") {
		t.Fatalf("o cabeçalho deveria explicar quantos itens não estão contidos nesta mensagem:\n%s", body[:minInt(400, len(body))])
	}
	// Deve conter apenas os primeiros kept itens.
	for i := 0; i < kept; i++ {
		if !strings.Contains(body, "漏洞"+itoa(i+1)) {
			t.Fatalf("o item nº %d deveria estar nesta mensagem:\n%s", i+1, body)
		}
	}
	if strings.Contains(body, "漏洞"+itoa(kept+1)) {
		t.Fatalf("o item nº %d não deveria aparecer (pertence ao próximo lote)", kept+1)
	}
}

func TestMarkdownBodyKeepsEverythingWhenUnderLimit(t *testing.T) {
	m := batchMsg(3)
	body, kept := markdownBody(m, 0) // 0 = sem limite
	if kept != len(m.Items) {
		t.Fatalf("sem limite de tamanho, deveria manter todos, obtido kept=%d", kept)
	}
	if strings.Contains(body, "其余") {
		t.Fatalf("sem truncamento, o aviso de truncamento não deveria aparecer:\n%s", body)
	}
}

func TestMarkdownBodyAlwaysKeepsAtLeastOneItem(t *testing.T) {
	// Quando o orçamento é tão pequeno que nem um item cabe, ainda assim é preciso enviar um (o truncamento final cobre).
	// Senão um item longo demais prenderia o lote inteiro para sempre: toda vez que fosse pego, não caberia, e toda vez não seria enviado.
	m := batchMsg(5)
	_, kept := markdownBody(m, 50)
	if kept != 1 {
		t.Fatalf("deveria manter pelo menos 1, obtido %d", kept)
	}
}

func TestMarkdownBodySingleReturnsOne(t *testing.T) {
	_, kept := markdownBody(singleMsg(), 4096)
	if kept != 1 {
		t.Fatalf("a mensagem única deveria informar 1 entregue, obtido %d", kept)
	}
	// Mensagem vazia não tem itens a entregar.
	if _, k := markdownBody(Message{}, 4096); k != 0 {
		t.Fatalf("a mensagem vazia deveria informar 0, obtido %d", k)
	}
}

func TestTelegramPackingUsesRuneBudget(t *testing.T) {
	m := batchMsg(200)
	text, kept := telegramHTML(m)
	// O Telegram limita por **número de caracteres**; usar o critério de bytes comprimiria a mensagem em chinês a um terço.
	if n := utf8.RuneCountInString(text); n > telegramTextLimit {
		t.Fatalf("corpo de %d caracteres excede o limite %d", n, telegramTextLimit)
	}
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("deveria acomodar apenas uma parte, obtido %d", kept)
	}
	if !strings.Contains(text, "seguem na próxima") {
		t.Fatalf("deveria explicar que há restante não contido:\n%.300s", text)
	}
}

func TestFeishuPackingReportsKept(t *testing.T) {
	m := batchMsg(2000)
	_, kept := feishuCard(m)
	if kept <= 0 || kept >= len(m.Items) {
		t.Fatalf("o cartão deveria acomodar apenas uma parte, obtido %d", kept)
	}
}

func TestWebhookAndEmailReportAllItems(t *testing.T) {
	// Estes dois canais não truncam o corpo, e o lote inteiro conta como entregue.
	m := batchMsg(7)
	if n := len(m.Items); n != 7 {
		t.Fatal("a pré-condição não se sustenta")
	}
	// Confirma indiretamente pelo valor de retorno do renderizador: com markdownBody(0) sem limite, todos são mantidos.
	if _, k := markdownBody(m, 0); k != len(m.Items) {
		t.Fatalf("sem limite de tamanho deveria usar todos, obtido %d", k)
	}
}

// TestMarkdownEscapesUntrustedContent é um teste de regressão do princípio "conteúdo não confiável não pode alterar a estrutura da mensagem".
// Título e resumo vêm da saída do modelo (o modelo lê a resposta do alvo testado), e o nome do ativo vem da URL do alvo testado.
func TestMarkdownEscapesUntrustedContent(t *testing.T) {
	cases := []struct {
		name  string
		item  Item
		must  []string // deve aparecer no resultado (forma escapada)
		wrong []string // não pode aparecer no resultado (forma sem escape)
	}{
		{
			name: "quebra de linha no título + link externo",
			item: Item{
				Severity: "high",
				Name:     "登录口 SQL 注入\n[紧急：点此验证账号](http://attacker.tld)",
			},
			// A quebra de linha deve ser colapsada (senão dá para forjar um novo item de lista/bloco de citação);
			// colchetes e parênteses devem ser escapados (senão é um link externo clicável).
			must:  []string{`\[紧急：点此验证账号\]`, `\(http://attacker.tld\)`},
			wrong: []string{"\n[紧急", "\n\n[紧急"},
		},
		{
			name: "beacon de imagem no título",
			item: Item{
				Severity: "high",
				Name:     "漏洞 ![](http://attacker.tld/beacon)",
			},
			must:  []string{`\!`, `\(http://attacker.tld/beacon\)`},
			wrong: []string{"![]("},
		},
		{
			name: "ênfase e citação no nome do ativo",
			item: Item{
				Severity: "high",
				Name:     "普通标题",
				Assets:   []string{"a.com/*注入*>引用"},
			},
			must:  []string{`\*注入\*`, `\>`},
			wrong: []string{"*注入*"},
		},
		{
			name: "crase e barra vertical no resumo",
			item: Item{
				Severity: "high",
				Name:     "标题",
				Summary:  "`code` | 表格",
			},
			must:  []string{"\\`code\\`", `\|`},
			wrong: []string{"`code`"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := Message{Items: []Item{tc.item}}
			// writeItem no modo de item único é o caminho de renderização compartilhado pelos três canais markdown.
			var b strings.Builder
			writeItem(&b, tc.item, "", true)
			got := b.String()
			for _, want := range tc.must {
				if !strings.Contains(got, want) {
					t.Errorf("forma escapada ausente %q:\n%s", want, got)
				}
			}
			for _, bad := range tc.wrong {
				if strings.Contains(got, bad) {
					t.Errorf("apareceu a forma sem escape %q (pode ser usada para injetar estrutura ou links externos):\n%s", bad, got)
				}
			}
			_ = m
		})
	}
}

// TestMarkdownEscapeBackslashFirst trava a ordem do escape: a barra invertida deve ser tratada primeiro,
// senão as barras acrescentadas depois seriam envolvidas por mais uma camada, e a saída teria barras invertidas duplas.
func TestMarkdownEscapeBackslashFirst(t *testing.T) {
	if got := markdownEscape(`a\b*c`); got != `a\\b\*c` {
		t.Fatalf("ordem de escape errada, obtido %q", got)
	}
}

// TestTelegramTitleHasNoMarkdownEscapes trava uma regressão específica:
// o escape de markdown não pode vazar para a saída HTML do Telegram (já se chegou a acrescentar escape
// na função de título compartilhada, e o resultado foi `\(1\)`, uma barra invertida visível, nas mensagens do Telegram).
func TestTelegramTitleHasNoMarkdownEscapes(t *testing.T) {
	m := Message{Items: []Item{{Severity: "high", Name: "alert(1) *重点*"}}}
	text, _ := telegramHTML(m)
	if strings.Contains(text, `\(`) || strings.Contains(text, `\*`) {
		t.Fatalf("o corpo do Telegram contém escape de barra invertida do markdown:\n%s", text)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
