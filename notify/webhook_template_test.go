package notify

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Este arquivo trava os **limites de capacidade** do template do Webhook genérico.
//
// É o único lugar de todo o pacote em que "uma string fornecida pelo usuário é avaliada como código", então é preciso deixar explícito o que ela pode fazer e o que
// não pode, e fixar essas propriedades com testes — senão, no futuro, alguém que acrescente de passagem um método ao contexto do template,
// ou um readFile ao FuncMap, amplia em silêncio a superfície de capacidade, enquanto o diff parece
// apenas uma pequena função inofensiva.

// TestTemplateContextHasNoMethods é a mais importante.
//
// O text/template chama métodos exportados ({{.Foo}} tanto lê campo quanto chama método). Portanto, assim que o contexto do template
// alcança **qualquer** tipo com métodos exportados, esses métodos ficam expostos ao autor do template.
// O contexto deste recurso é deliberadamente só dados puros (só campos exportados, zero métodos).
//
// Se esta falhar: significa que alguém acrescentou um método a webhookTemplateData / webhookItem.
// Antes de decidir liberar, pense bem se esse método poderia ser usado pelo template para ler algo que não se quer expor.
func TestTemplateContextHasNoMethods(t *testing.T) {
	for _, v := range []any{webhookTemplateData{}, webhookItem{}} {
		typ := reflect.TypeOf(v)
		if n := typ.NumMethod(); n != 0 {
			var names []string
			for i := 0; i < n; i++ {
				names = append(names, typ.Method(i).Name)
			}
			t.Fatalf("%s expõe %d métodos (%s): o text/template pode chamá-los, "+
				"o que equivale a abrir a capacidade desses métodos ao autor do template", typ.Name(), n, strings.Join(names, ", "))
		}
	}
}

// TestTemplateFuncsAreMinimal trava o conjunto de funções expostas ao template.
//
// Cada função a mais no FuncMap é uma capacidade a mais. Atualmente há só json / jsons, que servem para serializar valores
// como fragmentos JSON — não leem arquivos, não fazem requisições, não executam comandos.
func TestTemplateFuncsAreMinimal(t *testing.T) {
	var got []string
	for name := range webhookTemplateFuncs {
		got = append(got, name)
	}
	sort.Strings(got)
	want := []string{"json", "jsons"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("o conjunto de funções do template mudou: obtido %v, esperado %v. Antes de acrescentar funções, confirme que não ampliam a superfície de capacidade "+
			"(não podem ler/gravar arquivos, não podem iniciar requisições de rede, não podem executar comandos)", got, want)
	}
}

// TestTemplateCannotReachUnknownData cobre o acesso fora dos limites no template:
// acessar algo que não existe deve falhar, e não ecoar alguma coisa; e a mensagem de falha não pode revelar dados internos.
func TestTemplateCannotReachUnknownData(t *testing.T) {
	_, err := renderWebhookBody(`{"x": {{.Environment}}, "y": {{.Env}}}`, singleMsg())
	if err == nil {
		t.Fatal("acessar um campo inexistente deveria dar erro")
	}
	// O erro não pode conter conteúdo real do contexto do template (título/resumo da vulnerabilidade).
	for _, leak := range []string{"SQL注入", "参数 id"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("o erro do template vazou o conteúdo da mensagem %q: %v", leak, err)
		}
	}
}

// TestTemplateRenderFailsPermanently: um template escrito errado é erro de configuração, e repetir não se cura sozinho.
// Se fosse julgado passível de nova tentativa, um template ruim faria cada entrega gastar três rodadas de recuo à toa.
func TestTemplateRenderFailsPermanently(t *testing.T) {
	cfg := map[string]any{
		"url":           "https://example.com/hook",
		"body_template": `{{.Items.`,
	}
	if err := (webhookChannel{}).Validate(cfg); err == nil {
		t.Fatal("o erro de sintaxe do template deveria ser barrado já ao salvar")
	}
	// Mesmo contornando a validação e entregando direto, deve ser julgado falha permanente e não repetido.
	_, err := (webhookChannel{}).Send(context.Background(), cfg, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("template ruim deveria ser julgado falha permanente, obtido %v", err)
	}
}

// TestTemplateCanOnlyProduceJSON cobre a restrição "o resultado renderizado do template deve ser um JSON válido".
// Ela também barra, de passagem, usos como "gerar texto puro com o template para acionar outro protocolo".
func TestTemplateCanOnlyProduceJSON(t *testing.T) {
	// Template válido passa.
	ok := map[string]any{"url": "https://example.com/hook", "body_template": `{"t":{{json .Title}}}`}
	if err := (webhookChannel{}).Validate(ok); err != nil {
		t.Fatalf("template válido deveria passar na validação: %v", err)
	}
	// Quando renderiza algo que não é JSON, deve recusar (e não enviar como está).
	bad := map[string]any{"url": "http://127.0.0.1:1/hook", "body_template": `not json {{.Count}}`}
	_, err := (webhookChannel{}).Send(context.Background(), bad, singleMsg())
	if err == nil || !IsPermanent(err) {
		t.Fatalf("renderizar algo que não é JSON deveria ser julgado falha permanente, obtido %v", err)
	}
	if !strings.Contains(err.Error(), "JSON válido") {
		t.Errorf("a mensagem de erro deveria explicar que é um problema de JSON, obtido %v", err)
	}
}
