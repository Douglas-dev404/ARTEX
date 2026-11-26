package notify

import (
	"errors"
	"strings"
	"testing"
)

func TestMaskedValueHidesBodyButKeepsTailHint(t *testing.T) {
	const secret = "https://oapi.dingtalk.com/robot/send?access_token=abcdef123456"
	got := MaskedValue(secret)
	if strings.Contains(got, "abcdef123456") {
		t.Fatalf("o valor mascarado vazou a credencial completa: %q", got)
	}
	if strings.Contains(got, "oapi.dingtalk.com") {
		t.Fatalf("o valor mascarado não deveria expor o corpo do endereço: %q", got)
	}
	// Os 6 últimos caracteres devem ser mantidos, para o usuário reconhecer qual é o robô.
	if !strings.HasSuffix(got, "123456") {
		t.Fatalf("deveria manter os 6 últimos caracteres como dica de identificação: %q", got)
	}
	if !IsMasked(got) {
		t.Fatalf("o valor mascarado precisa ser reconhecido por IsMasked: %q", got)
	}
}

func TestMaskedValueShortSecretGivesNoHint(t *testing.T) {
	// Se uma credencial curta também expusesse os 6 últimos caracteres, equivaleria a expor a credencial inteira.
	for _, s := range []string{"abc", "abcdef", ""} {
		got := MaskedValue(s)
		if got != MaskedPrefix {
			t.Fatalf("a credencial de tamanho %d não deveria dar dica do final, obtido %q", len(s), got)
		}
		if s != "" && strings.Contains(got, s) {
			t.Fatalf("o valor mascarado contém o valor original: %q", got)
		}
	}
}

func TestMaskConfigMasksOnlySecrets(t *testing.T) {
	cfg := map[string]any{
		"webhook": "https://example.com/hook?token=SECRETVALUE",
		"secret":  "SECtest123456",
		"port":    float64(587),
		"host":    "smtp.example.com",
	}
	masked := MaskConfig(KindDingTalk, cfg)
	for _, k := range []string{"webhook", "secret"} {
		s, _ := masked[k].(string)
		if !IsMasked(s) {
			t.Errorf("%s deveria ser mascarado, obtido %q", k, s)
		}
	}
	// Campos que não são credencial devem ser mantidos como estão, senão a UI não consegue exibi-los.
	if masked["port"] != float64(587) {
		t.Errorf("o campo port, que não é credencial, não deveria ser alterado: %v", masked["port"])
	}
}

func TestMaskConfigUnknownKindReturnsEmpty(t *testing.T) {
	// Quando o tipo de canal não é reconhecido, é melhor a UI mostrar uma configuração vazia do que devolver o conteúdo original que possivelmente contém credenciais.
	got := MaskConfig("nope", map[string]any{"webhook": "https://x/y?token=LEAK"})
	if len(got) != 0 {
		t.Fatalf("tipo de canal desconhecido deveria retornar configuração vazia, obtido %v", got)
	}
}

func TestMaskConfigDoesNotMutateInput(t *testing.T) {
	// A máscara é um comportamento da camada de exibição e não pode, ao contrário, alterar o valor real do banco.
	cfg := map[string]any{"webhook": "https://example.com/hook", "secret": "SECtest123456"}
	_ = MaskConfig(KindDingTalk, cfg)
	if IsMasked(cfg["secret"].(string)) {
		t.Fatal("MaskConfig modificou o argumento, o que faria a credencial real ser sobrescrita pelo valor mascarado")
	}
}

func TestMergeConfigKeepsStoredOnMaskedIncoming(t *testing.T) {
	stored := map[string]any{"webhook": "https://real/hook", "secret": "REALSECRET", "method": "POST"}
	// O usuário só alterou o method, e o navegador enviou o valor mascarado + o novo method.
	incoming := map[string]any{
		"webhook": MaskedValue("https://real/hook"),
		"secret":  MaskedValue("REALSECRET"),
		"method":  "PUT",
	}
	got := MergeConfig(stored, incoming)
	if got["webhook"] != "https://real/hook" || got["secret"] != "REALSECRET" {
		t.Fatalf("o campo mascarado deveria manter o valor original do banco, obtido %v", got)
	}
	if got["method"] != "PUT" {
		t.Fatalf("o campo modificado deveria valer, obtido %v", got["method"])
	}
}

func TestMergeConfigEmptyStringClears(t *testing.T) {
	stored := map[string]any{"webhook": "https://real/hook", "secret": "REALSECRET"}
	got := MergeConfig(stored, map[string]any{"secret": ""})
	if _, ok := got["secret"]; ok {
		t.Fatalf("string vazia deveria limpar o campo, obtido %v", got)
	}
	// Campos não mencionados são mantidos (semântica de atualização parcial).
	if got["webhook"] != "https://real/hook" {
		t.Fatalf("campos não mencionados deveriam ser mantidos, obtido %v", got)
	}
}

func TestMergeConfigKeepsUnmentionedStoredKeys(t *testing.T) {
	stored := map[string]any{"host": "smtp.example.com", "port": float64(587), "password": "pw"}
	got := MergeConfig(stored, map[string]any{"port": float64(465)})
	if got["host"] != "smtp.example.com" || got["password"] != "pw" {
		t.Fatalf("campos não mencionados deveriam ser mantidos, obtido %v", got)
	}
	if got["port"] != float64(465) {
		t.Fatalf("campos mencionados deveriam ser atualizados, obtido %v", got["port"])
	}
}

// TestPrepareConfigUpdateBlocksDestinationSwap é a invariante de segurança mais importante deste pacote:
// **alterar o endereço de destino não pode levar junto a credencial antiga**.
//
// Estes casos usam justamente entradas com a forma do ataque (alterar só o endereço, sem dizer nada sobre a credencial),
// e não "a entrada correta da lógica de defesa" — testando só esta última, a defesa passaria toda verde mesmo sem estar em vigor.
func TestPrepareConfigUpdateBlocksDestinationSwap(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		stored   map[string]any
		incoming map[string]any
		// wantMissing é a chave de credencial que se espera ser nomeada.
		wantMissing string
	}{
		{
			name: "Webhook genérico altera o endereço querendo manter o cabeçalho Authorization",
			kind: KindWebhook,
			stored: map[string]any{
				"url":     "https://legit.example.com/hook",
				"headers": map[string]any{"Authorization": "Bearer REAL-TOKEN"},
			},
			incoming:    map[string]any{"url": "https://attacker.tld/c"},
			wantMissing: "headers",
		},
		{
			name:        "Telegram altera o base_url querendo enviar o Bot Token ao seu próprio endpoint",
			kind:        KindTelegram,
			stored:      map[string]any{"bot_token": "123456:REAL", "chat_id": "1", "base_url": "https://api.telegram.org"},
			incoming:    map[string]any{"base_url": "https://attacker.tld"},
			wantMissing: "bot_token",
		},
		{
			name:        "e-mail altera o host SMTP querendo entregar a senha",
			kind:        KindEmail,
			stored:      map[string]any{"host": "smtp.corp.com", "port": 587, "password": "REALPW", "from": "a@b.c", "to": []any{"d@e.f"}},
			incoming:    map[string]any{"host": "smtp.attacker.tld"},
			wantMissing: "password",
		},
		{
			name:        "e-mail desligando o TLS também precisa se manifestar de novo sobre a senha",
			kind:        KindEmail,
			stored:      map[string]any{"host": "smtp.corp.com", "port": 587, "tls": false, "password": "REALPW", "from": "a@b.c", "to": []any{"d@e.f"}},
			incoming:    map[string]any{"tls": true},
			wantMissing: "password",
		},
		{
			// Valor mascarado = "manter a credencial antiga"; no contexto de alteração de endereço também deve ser recusado.
			name:        "devolver a credencial mascarada + novo endereço",
			kind:        KindTelegram,
			stored:      map[string]any{"bot_token": "123456:REAL", "chat_id": "1", "base_url": "https://api.telegram.org"},
			incoming:    map[string]any{"base_url": "https://attacker.tld", "bot_token": MaskedValue("123456:REAL")},
			wantMissing: "bot_token",
		},
		{
			name:        "DingTalk altera o Webhook querendo manter a chave de assinatura",
			kind:        KindDingTalk,
			stored:      map[string]any{"webhook": "https://oapi.dingtalk.com/robot/send?access_token=OLD", "secret": "REALSEC"},
			incoming:    map[string]any{"webhook": "https://attacker.tld/hook"},
			wantMissing: "secret",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged, err := PrepareConfigUpdate(tc.kind, tc.stored, tc.incoming)
			if err == nil {
				t.Fatalf("alterou o endereço sem se manifestar de novo sobre a credencial, deveria ser recusado; obtida a configuração %v", merged)
			}
			var target *ErrDestinationChangedWithoutCredentials
			if !errors.As(err, &target) {
				t.Fatalf("deveria retornar um tipo de erro específico para que a interface dê uma dica acionável, obtido %T: %v", err, err)
			}
			found := false
			for _, m := range target.Missing {
				if m == tc.wantMissing {
					found = true
				}
			}
			if !found {
				t.Fatalf("deveria nomear a chave de credencial ausente %q, obtido %v", tc.wantMissing, target.Missing)
			}
			// A mensagem de erro deve orientar o operador sobre como corrigir.
			if !strings.Contains(err.Error(), tc.wantMissing) {
				t.Errorf("a mensagem de erro deveria mencionar %q: %v", tc.wantMissing, err)
			}
		})
	}
}

// TestPrepareConfigUpdateAllowsLegitimateEdits é o caso inverso: edições normais não podem ser barradas por engano,
// senão esta proteção seria contornada ou removida por ser "irritante demais".
func TestPrepareConfigUpdateAllowsLegitimateEdits(t *testing.T) {
	cases := []struct {
		name     string
		kind     string
		stored   map[string]any
		incoming map[string]any
	}{
		{
			name:     "só altera o nome (configuração devolvida como está)",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://legit.example.com/hook", "headers": map[string]any{"Authorization": "Bearer REAL"}},
			incoming: map[string]any{"url": MaskedValue("https://legit.example.com/hook")},
		},
		{
			name:     "só altera o método da requisição, sem mexer no endereço nem na credencial",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://legit.example.com/hook", "method": "POST"},
			incoming: map[string]any{"method": "PUT"},
		},
		{
			name:     "troca o endereço e **ao mesmo tempo** fornece a nova credencial",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://old.example.com/hook", "headers": map[string]any{"Authorization": "Bearer OLD"}},
			incoming: map[string]any{"url": "https://new.example.com/hook", "headers": map[string]any{"Authorization": "Bearer NEW"}},
		},
		{
			name:     "troca o endereço e declara explicitamente que não precisa mais de credencial",
			kind:     KindWebhook,
			stored:   map[string]any{"url": "https://old.example.com/hook", "headers": map[string]any{"Authorization": "Bearer OLD"}},
			incoming: map[string]any{"url": "https://new.example.com/hook", "headers": ""},
		},
		{
			name:     "Telegram altera o chat_id (não é destino)",
			kind:     KindTelegram,
			stored:   map[string]any{"bot_token": "t", "chat_id": "1", "base_url": "https://api.telegram.org"},
			incoming: map[string]any{"chat_id": "-100200"},
		},
		{
			name:     "e-mail altera os destinatários (não é destino)",
			kind:     KindEmail,
			stored:   map[string]any{"host": "smtp.corp.com", "port": 587, "password": "PW", "from": "a@b.c", "to": []any{"x@y.z"}},
			incoming: map[string]any{"to": []any{"new@y.z"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			merged, err := PrepareConfigUpdate(tc.kind, tc.stored, tc.incoming)
			if err != nil {
				t.Fatalf("edição legítima barrada por engano: %v", err)
			}
			if merged == nil {
				t.Fatal("deveria retornar o resultado da mesclagem")
			}
		})
	}
}

// TestPrepareConfigUpdatePortTypeTolerance cobre um detalhe fácil de julgar errado:
// a porta enviada pelo front-end é um JSON number (float64), e a lida de volta do banco também é float64,
// mas o tipo dos dois valores pode ser diferente (como int vs float64). Comparar com == julgaria "não alterou" como "alterou",
// e mostraria "preencha a senha de novo" para quem só mudou o nome — falsos alarmes fazem as pessoas deixarem de confiar nesta proteção.
func TestPrepareConfigUpdatePortTypeTolerance(t *testing.T) {
	stored := map[string]any{"host": "smtp.corp.com", "port": float64(587), "password": "PW"}
	// A mesma porta, enviada na forma int.
	if _, err := PrepareConfigUpdate(KindEmail, stored, map[string]any{"port": 587}); err != nil {
		t.Fatalf("valores de porta iguais (só o tipo difere) não deveriam ser julgados como alteração de endereço: %v", err)
	}
	// Se de fato trocou a porta, deve ser barrado.
	if _, err := PrepareConfigUpdate(KindEmail, stored, map[string]any{"port": 25}); err == nil {
		t.Fatal("a alteração de porta deveria ser barrada")
	}
}

// TestPrepareConfigUpdateSurvivesRepeatedSaveWithBlankDestination cobre o caminho dos "campos de
// destino que podem ficar vazios": o base_url do Telegram vazio significa usar o endereço da API oficial.
//
// Antes, isto fazia o canal falhar para sempre ao salvar a partir do segundo salvamento:
//
//	na criação o banco grava base_url:"" (o caminho de criação grava direto a config enviada pelo front-end, sem passar pelo MergeConfig)
//	→ no primeiro salvamento, o MergeConfig trata a string vazia como limpeza explícita e dá delete na chave
//	→ no segundo salvamento, incoming continua "" e stored já não tem essa chave, sendo julgado como "o endereço mudou"
//	→ bot_token é o valor mascarado devolvido → 400 "O endereço de destino foi alterado; preencha novamente também os campos de credencial"
//
// O usuário não mudou nada, mas daí em diante nunca mais consegue salvar, a menos que cole de novo o Bot Token.
func TestPrepareConfigUpdateSurvivesRepeatedSaveWithBlankDestination(t *testing.T) {
	stored := map[string]any{"bot_token": "123:ABC", "chat_id": "-100", "base_url": ""}

	// O buildConfig() do front-end envia um valor para cada definição de campo deste canal: a credencial volta com a máscara preenchida,
	// e a caixa de texto vazia envia string vazia. Aqui se reproduz por inteiro a saída dele, e não apenas "as chaves alteradas".
	submit := func() map[string]any {
		return map[string]any{
			"bot_token": MaskedValue("123:ABC"),
			"chat_id":   "-100",
			"base_url":  "",
		}
	}

	// Primeiro salvamento: só alterou o nome do canal, config devolvida como está.
	merged, err := PrepareConfigUpdate(KindTelegram, stored, submit())
	if err != nil {
		t.Fatalf("primeiro salvamento barrado por engano: %v", err)
	}
	if _, ok := merged["base_url"]; ok {
		t.Fatal("a premissa mudou: a string vazia deveria ser apagada pelo MergeConfig — este caso quer justamente cobrir o passo \"depois que a chave desaparece\"")
	}

	// Segundo salvamento: o conteúdo enviado é idêntico ao anterior, o usuário não alterou nada.
	merged2, err := PrepareConfigUpdate(KindTelegram, merged, submit())
	if err != nil {
		t.Fatalf("segundo salvamento barrado por engano (o usuário não alterou nada): %v", err)
	}
	// Terceiro, para confirmar que não é "errar só uma vez" e que o salvamento é estável.
	if _, err := PrepareConfigUpdate(KindTelegram, merged2, submit()); err != nil {
		t.Fatalf("terceiro salvamento barrado por engano: %v", err)
	}
	// A credencial deve ser mantida o tempo todo, sem ser limpa de roldão pela lógica da string vazia.
	if got := merged2["bot_token"]; got != "123:ABC" {
		t.Fatalf("o Bot Token deveria manter o valor original, obtido %v", got)
	}
}

// TestPrepareConfigUpdateStillGuardsBlankDestinationChanges é a asserção par do caso anterior:
// tratar string vazia e "chave inexistente" como equivalentes **não pode** deixar passar também uma alteração real de endereço.
// As duas direções são caminhos reais de saída de credencial — o Bot Token do Telegram vai no path da URL,
// e trocar o base_url equivale a entregar o Token ao novo endereço.
func TestPrepareConfigUpdateStillGuardsBlankDestinationChanges(t *testing.T) {
	// Direção um: de "vazio" (endereço oficial) para um endereço próprio.
	official := map[string]any{"bot_token": "123:ABC", "chat_id": "-100"}
	if _, err := PrepareConfigUpdate(KindTelegram, official, map[string]any{
		"bot_token": MaskedValue("123:ABC"),
		"base_url":  "https://tg-proxy.attacker.tld",
	}); err == nil {
		t.Fatal("trocar do endereço oficial para um endereço próprio deve exigir preencher o Token de novo")
	}

	// Direção dois: limpar o endereço próprio (= voltar à API oficial) também é alteração de endereço.
	proxied := map[string]any{"bot_token": "123:ABC", "base_url": "https://proxy.internal/bot"}
	if _, err := PrepareConfigUpdate(KindTelegram, proxied, map[string]any{
		"bot_token": MaskedValue("123:ABC"),
		"base_url":  "",
	}); err == nil {
		t.Fatal("limpar o endereço próprio (voltar à API oficial) também é alteração de endereço e deve exigir preencher o Token de novo")
	}
}

func TestDestinationKeysDeclaredForEveryKind(t *testing.T) {
	// Pelo mesmo motivo de SecretKeys: se um canal esquecer de declarar as chaves de destino, o PrepareConfigUpdate não o protege.
	for kind, ch := range registry {
		if len(ch.DestinationKeys()) == 0 {
			t.Errorf("o canal %s não declarou chaves de destino, a proteção contra credencial levada junto com a alteração de endereço é inútil para ele", kind)
		}
		if len(ch.SecretKeys()) == 0 {
			t.Errorf("o canal %s não declarou chaves de credencial", kind)
		}
	}
}

func TestSecretKeysDeclaredForEveryKind(t *testing.T) {
	// O compilador já obriga cada canal a implementar SecretKeys; aqui se confirma de novo que "nenhum canal fica sem declarar nada"
	// na máscara — um canal que retorna slice vazio significa que suas credenciais são ecoadas em texto claro para o navegador.
	expect := map[string]bool{
		KindDingTalk: true, KindFeishu: true, KindWeCom: true,
		KindWebhook: true, KindTelegram: true, KindEmail: true,
	}
	for kind, ch := range registry {
		if !expect[kind] {
			t.Errorf("o canal %s não está registrado nas expectativas de máscara do teste", kind)
			continue
		}
		if len(ch.SecretKeys()) == 0 {
			t.Errorf("o canal %s não declarou nenhum campo de credencial, e sua configuração seria ecoada em texto claro", kind)
		}
	}
}

// TestPrepareConfigUpdateRejectsMaskedInContainer cobre uma brecha apontada pela auditoria:
// quando a sentinela de máscara é enfiada numa estrutura **que não é string** (como webhook.headers, que é um objeto),
// o MergeConfig só reconhece como máscara "uma string com o prefixo", e o literal "__masked__" seria tratado como
// valor de cabeçalho real e gravado no banco — a autenticação posterior falharia em silêncio, sem nenhum erro.
func TestPrepareConfigUpdateRejectsMaskedInContainer(t *testing.T) {
	stored := map[string]any{
		"url":     "https://legit.example.com/hook",
		"headers": map[string]any{"Authorization": "Bearer REAL"},
	}
	// Sentinela de máscara embutida dentro do objeto.
	incoming := map[string]any{
		"headers": map[string]any{"Authorization": MaskedPrefix},
	}
	if _, err := PrepareConfigUpdate(KindWebhook, stored, incoming); err == nil {
		t.Fatal("a sentinela de máscara embutida na estrutura deveria ser recusada (senão o literal seria gravado no banco)")
	}
	// Enviar o objeto por inteiro (um novo valor real) é aceito normalmente.
	ok := map[string]any{"headers": map[string]any{"Authorization": "Bearer NEW"}}
	if _, err := PrepareConfigUpdate(KindWebhook, stored, ok); err != nil {
		t.Fatalf("o envio normal de novos cabeçalhos de requisição não deveria ser barrado: %v", err)
	}
}
