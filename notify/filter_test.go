package notify

import "testing"

func TestParseFilterMalformedFallsBackToMatchAll(t *testing.T) {
	// JSON malformado, entrada vazia, campos de tipo errado — todos devem degradar para um Filter de valor zero,
	// ou seja, "sem filtro". Esta invariante é o ponto de aplicação do princípio "melhor enviar a mais do que deixar de enviar":
	// se aqui se passasse a dar erro ou a interpretar pela metade, um único caractere errado na configuração do usuário descartaria em silêncio todas as notificações de alta severidade.
	cases := []struct {
		name string
		raw  string
	}{
		{"entrada vazia", ""},
		{"JSON inválido", `{not json`},
		{"JSON truncado", `{"min_severity":`},
		{"tipo incompatível", `{"min_severity": 123, "task_ids": "abc"}`},
		{"o topo é um array", `[1,2,3]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := ParseFilter([]byte(tc.raw))
			if f.MinSeverity != "" || len(f.TaskIDs) != 0 || len(f.AssetIDs) != 0 {
				t.Fatalf("a configuração malformada deveria degradar para um Filter de valor zero, obtido %+v", f)
			}
			// O Filter de valor zero deve corresponder a qualquer evento.
			ev := Snapshot{Kind: EventFindingCreated, Severity: "low", VulnClass: "XSS"}
			if !Match(f, ev) {
				t.Fatal("o Filter de valor zero deveria corresponder a todos os eventos")
			}
		})
	}
}

func TestMatchSeverityThreshold(t *testing.T) {
	ev := func(sev string) Snapshot {
		return Snapshot{Kind: EventFindingCreated, Severity: sev}
	}
	cases := []struct {
		min    string
		sev    string
		expect bool
	}{
		{"", "low", true},
		{"", "critical", true},
		{"high", "critical", true},
		{"high", "high", true},
		{"high", "medium", false},
		{"high", "low", false},
		{"critical", "high", false},
		{"critical", "critical", true},
		// Severidade desconhecida tem ordinal 0 e deve ser barrada por qualquer limiar não vazio (na dúvida, não envia).
		{"low", "", false},
		{"low", "unknown", false},
		{"", "", true},
	}
	for _, tc := range cases {
		got := Match(Filter{MinSeverity: tc.min}, ev(tc.sev))
		if got != tc.expect {
			t.Errorf("min=%q sev=%q: esperava %v, obtido %v", tc.min, tc.sev, tc.expect, got)
		}
	}
}

func TestMatchScopeRestrictions(t *testing.T) {
	ev := Snapshot{
		Kind:      EventFindingCreated,
		Severity:  "high",
		TaskID:    7,
		AssetIDs:  []int64{10, 20},
		VulnClass: "SQL注入",
	}
	cases := []struct {
		name   string
		filter Filter
		expect bool
	}{
		{"escopo vazio = sem restrição", Filter{}, true},
		{"tarefa corresponde", Filter{TaskIDs: []int64{7}}, true},
		{"tarefa não corresponde", Filter{TaskIDs: []int64{8}}, false},
		{"várias tarefas, incluindo uma que corresponde", Filter{TaskIDs: []int64{8, 7}}, true},
		{"ativo com interseção", Filter{AssetIDs: []int64{20, 99}}, true},
		{"ativo sem interseção", Filter{AssetIDs: []int64{99}}, false},
		{"tarefa e ativo correspondem ao mesmo tempo", Filter{TaskIDs: []int64{7}, AssetIDs: []int64{10}}, true},
		{"tarefa corresponde mas o ativo não", Filter{TaskIDs: []int64{7}, AssetIDs: []int64{99}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Match(tc.filter, ev); got != tc.expect {
				t.Errorf("esperava %v, obtido %v", tc.expect, got)
			}
		})
	}
}

func TestMatchVulnClassKeywords(t *testing.T) {
	ev := func(class string) Snapshot {
		return Snapshot{Kind: EventFindingCreated, Severity: "high", VulnClass: class}
	}
	cases := []struct {
		name   string
		filter Filter
		class  string
		expect bool
	}{
		{"include vazio = aceita tudo", Filter{}, "任意类型", true},
		{"include corresponde", Filter{VulnClassInclude: []string{"SQL"}}, "SQL注入", true},
		{"include não corresponde", Filter{VulnClassInclude: []string{"命令执行"}}, "SQL注入", false},
		{"include com várias palavras, qualquer uma corresponde", Filter{VulnClassInclude: []string{"命令执行", "SQL"}}, "SQL注入", true},
		{"sem distinção de maiúsculas e minúsculas", Filter{VulnClassInclude: []string{"sql"}}, "SQL注入", true},
		{"exclude corresponde, então exclui", Filter{VulnClassExclude: []string{"信息泄露"}}, "信息泄露", false},
		{"exclude não corresponde, então libera", Filter{VulnClassExclude: []string{"信息泄露"}}, "SQL注入", true},
		// A exclusão tem prioridade sobre a inclusão: quando ambas correspondem, o evento deve ficar de fora.
		{"exclusão tem prioridade sobre inclusão", Filter{
			VulnClassInclude: []string{"SQL"},
			VulnClassExclude: []string{"注入"},
		}, "SQL注入", false},
		// Palavra-chave só com espaço em branco deve ser ignorada, senão degeneraria em "corresponde a todas as strings que contêm espaço".
		{"palavra-chave em branco é ignorada", Filter{VulnClassInclude: []string{"", "  "}}, "SQL注入", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Match(tc.filter, ev(tc.class)); got != tc.expect {
				t.Errorf("esperava %v, obtido %v", tc.expect, got)
			}
		})
	}
}

func TestMatchStatusChangeRequiresOptIn(t *testing.T) {
	ev := Snapshot{Kind: EventFindingStatusChanged, Severity: "critical", FromStatus: "pending", ToStatus: "fixed"}
	// Desligado por padrão: para a maioria das pessoas, "enviar vulnerabilidades" significa descobrir uma nova vulnerabilidade, e não um registro corrido de status.
	if Match(Filter{MinSeverity: "low"}, ev) {
		t.Fatal("o evento de mudança de status deveria ser ignorado quando não habilitado")
	}
	if !Match(Filter{OnStatusChange: true}, ev) {
		t.Fatal("com on_status_change habilitado, o evento de mudança de status deveria corresponder")
	}
	// O evento de criação não é afetado por on_status_change.
	created := Snapshot{Kind: EventFindingCreated, Severity: "critical"}
	if !Match(Filter{MinSeverity: "low"}, created) {
		t.Fatal("o evento de criação não deveria depender de on_status_change")
	}
}
