package notify

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateBytesKeepsValidUTF8(t *testing.T) {
	// Esta é a invariante mais importante deste pacote. O WeCom limita por **bytes**, e o chinês usa 3 bytes/caractere;
	// qualquer implementação que corte por bytes às cegas parte um caractere ao meio, gerando UTF-8 inválido que a plataforma rejeita.
	// Usa-se entradas mistas chinês/inglês de comprimentos coprimos para bater em cada ponto de corte possível.
	inputs := []string{
		"中文测试内容",
		"混合 mixed 内容 content",
		"a中b文c测d试e",
		"🔴🟠🟡🔵", // emoji de 4 bytes, cortar errado fica mais evidente
		strings.Repeat("漏洞", 100),
	}
	for _, in := range inputs {
		for max := 1; max <= len(in)+2; max++ {
			got := TruncateBytes(in, max)
			if !utf8.ValidString(got) {
				t.Fatalf("entrada %q max=%d: produziu UTF-8 inválido %q", in, max, got)
			}
			if len(got) > max {
				t.Fatalf("entrada %q max=%d: resultado de %d bytes excede o limite", in, max, len(got))
			}
			// Quando não é truncado, o conteúdo não deve ser alterado.
			if len(in) <= max && got != in {
				t.Fatalf("entrada %q max=%d: não excedeu o limite mas o conteúdo foi alterado -> %q", in, max, got)
			}
		}
	}
}

func TestTruncateBytesZeroMeansUnlimited(t *testing.T) {
	long := strings.Repeat("x", 10000)
	if got := TruncateBytes(long, 0); got != long {
		t.Fatal("max=0 deveria significar sem limite")
	}
	if got := TruncateBytes(long, -5); got != long {
		t.Fatal("max<0 deveria significar sem limite")
	}
}

func TestTruncateBytesEllipsisBudget(t *testing.T) {
	// Quando max é menor que as próprias reticências, não se pode, ao acrescentá-las, acabar excedendo o limite.
	got := TruncateBytes("abcdefgh", 1)
	if len(got) > 1 {
		t.Fatalf("com max=1 o resultado %q de tamanho %d excede o limite", got, len(got))
	}
	// No caso normal deve vir com reticências.
	if got := TruncateBytes("abcdefgh", 5); !strings.HasSuffix(got, ellipsis) {
		t.Fatalf("esperava reticências, obtido %q", got)
	}
}

func TestTruncateRunesCountsCharactersNotBytes(t *testing.T) {
	// A diferença de critério em relação ao TruncateBytes deve ser mantida: o Telegram limita por caracteres,
	// e usar o critério de bytes cortaria a mensagem em chinês a apenas um terço.
	s := "一二三四五六七八九十"
	got := TruncateRunes(s, 5)
	if n := utf8.RuneCountInString(got); n != 5 {
		t.Fatalf("esperava 5 caracteres, obtido %d (%q)", n, got)
	}
	// A mesma string, pelo critério de bytes, deveria ser nitidamente mais curta.
	if utf8.RuneCountInString(TruncateBytes(s, 5)) >= 5 {
		t.Fatal("o critério de bytes não deveria produzir o mesmo número de caracteres que o critério de caracteres")
	}
}

func TestOneLineCollapsesWhitespace(t *testing.T) {
	got := OneLine("第一行\n\n第二行\t带制表   多空格", 0)
	if strings.ContainsAny(got, "\n\t") {
		t.Fatalf("deveria colapsar todo o espaço em branco, obtido %q", got)
	}
	if strings.Contains(got, "  ") {
		t.Fatalf("não deveria manter espaços consecutivos, obtido %q", got)
	}
	// Depois do truncamento ainda deve ser legível e válido.
	got = OneLine("一二三四五六七八九十", 4)
	if n := utf8.RuneCountInString(got); n != 4 {
		t.Fatalf("esperava 4 caracteres, obtido %d (%q)", n, got)
	}
}

func TestTruncateHTMLNeverCutsTagInHalf(t *testing.T) {
	// Truncar HTML direto pode cortar um fragmento como `<a href="htt`, e a plataforma rejeitaria a mensagem inteira.
	s := `<b>标题</b>正文正文正文<a href="https://example.com/very/long/path">查看详情</a>`
	for max := 1; max <= utf8.RuneCountInString(s)+2; max++ {
		got := TruncateHTML(s, max)
		if n := utf8.RuneCountInString(got); max > 0 && n > max {
			t.Fatalf("max=%d: resultado de %d caracteres excede o limite", max, n)
		}
		// O final não pode ter `<` não fechado (ou seja, no último trecho aparece `<` sem `>`).
		if lt := strings.LastIndex(got, "<"); lt >= 0 && !strings.Contains(got[lt:], ">") {
			t.Fatalf("max=%d: a tag do final foi cortada -> %q", max, got)
		}
	}
}

func TestAssetLineOmitsExcess(t *testing.T) {
	if got := assetLine(nil, 3); got != "" {
		t.Fatalf("sem ativos deveria retornar string vazia, obtido %q", got)
	}
	if got := assetLine([]string{"a", "b"}, 3); got != "a, b" {
		t.Fatalf("sem exceder o limite deveria listar todos, obtido %q", got)
	}
	// Ao exceder o limite é preciso indicar o total, senão o leitor não sabe quantos ativos ficaram sem listar.
	got := assetLine([]string{"a", "b", "c", "d", "e"}, 2)
	if !strings.Contains(got, "total: 5") {
		t.Fatalf("deveria indicar o total 5, obtido %q", got)
	}
}

func TestSeverityAndStatusLabels(t *testing.T) {
	if AtLeast("", "low") {
		t.Fatal("o ordinal da severidade vazia é 0 e deveria ser barrado por qualquer limiar")
	}
	if !AtLeast("critical", "") {
		t.Fatal("limiar vazio deveria deixar passar")
	}
	if got := StatusLabel("fixed"); got != "已修复" {
		t.Fatalf("mapeamento de status desconhecido, obtido %q", got)
	}
	// Status desconhecido é ecoado como está, sem inventar rótulo.
	if got := StatusLabel("weird_status"); got != "weird_status" {
		t.Fatalf("status desconhecido deveria ser ecoado como está, obtido %q", got)
	}
}

// TestTruncateHTMLNeverCutsEntity cobre uma omissão apontada pela auditoria: o truncamento não deve evitar só
// a tag cortada pela metade, mas também a entidade HTML cortada.
//
// Depois que `&amp;` é cortada em `&amp`, um parser que só reconhece entidades pode rejeitar a mensagem **inteira** —
// e mensagens consolidadas longas demais são comuns, o custo é alto demais.
func TestTruncateHTMLNeverCutsEntity(t *testing.T) {
	s := "aaaa&amp;bbbb&lt;cccc&quot;dddd"
	for max := 1; max <= utf8.RuneCountInString(s)+2; max++ {
		got := TruncateHTML(s, max)
		// O final não pode ter o fragmento de entidade "tem & mas não tem o ; correspondente".
		if amp := strings.LastIndex(got, "&"); amp >= 0 && !strings.Contains(got[amp:], ";") {
			t.Fatalf("max=%d: sobrou fragmento de entidade no final %q", max, got[amp:])
		}
		if strings.Contains(got, "&amp\x00") {
			t.Fatalf("max=%d: apareceu entidade malformada", max)
		}
	}
}
