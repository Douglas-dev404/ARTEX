package server

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Autumn-27/artex/selfupdate"
)

// releaseCache é a camada que protege a cota do GitHub: a API sem autenticação só tem 60 vezes/hora/IP,
// e o aviso "há nova versão" da barra superior consulta uma vez a cada carga completa da página. Quando o cache falha, o usuário abrindo várias
// abas esgota a cota, e depois, quando se quer mesmo atualizar, não dá para consultar.

func newTestCache(fetch func(context.Context, *http.Client) (*selfupdate.Release, error)) *releaseCache {
	return &releaseCache{fetch: fetch}
}

func TestReleaseCacheServesFromCache(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return &selfupdate.Release{TagName: "v0.3.8"}, nil
	})

	for range 5 {
		rel, err := c.get(t.Context(), nil, false)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if rel.TagName != "v0.3.8" {
			t.Fatalf("TagName = %q", rel.TagName)
		}
	}
	if calls != 1 {
		t.Errorf("5 consultas deveriam ir à origem só 1 vez, foram %d vezes", calls)
	}
}

func TestReleaseCacheForceBypasses(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return &selfupdate.Release{TagName: "v0.3.8"}, nil
	})

	if _, err := c.get(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}
	// Quando o usuário clica em "verificar atualização" precisa receber o resultado em tempo real, senão uma versão recém-publicada só apareceria quando o cache expirasse.
	if _, err := c.get(t.Context(), nil, true); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("force deveria contornar o cache, esperado 2 idas à origem, foram %d vezes", calls)
	}
}

func TestReleaseCacheExpiresAfterTTL(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return &selfupdate.Release{TagName: "v0.3.8"}, nil
	})

	if _, err := c.get(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}
	// Recua o horário gravado para logo depois de expirar, simulando o TTL vencendo.
	c.at = time.Now().Add(-releaseTTL - time.Second)
	if _, err := c.get(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Errorf("após o TTL expirar deveria voltar à origem, esperado 2 vezes, foram %d vezes", calls)
	}
}

func TestReleaseCacheUsesShorterTTLForErrors(t *testing.T) {
	calls := 0
	c := newTestCache(func(context.Context, *http.Client) (*selfupdate.Release, error) {
		calls++
		return nil, errors.New("github inacessível")
	})

	if _, err := c.get(t.Context(), nil, false); err == nil {
		t.Fatal("esperava-se que retornasse erro")
	}
	// O resultado de falha também deve ser guardado em cache por um instante, senão, com o GitHub inacessível, cada carga de página espera um timeout à toa.
	if _, err := c.get(t.Context(), nil, false); err == nil {
		t.Fatal("esperava-se que retornasse erro")
	}
	if calls != 1 {
		t.Errorf("o erro deveria ficar em cache por pouco tempo, esperado 1 ida à origem, foram %d vezes", calls)
	}

	// Mas o TTL do erro deve ser nitidamente menor que o do sucesso, para que ao voltar a rede ele se cure logo.
	if releaseErrTTL >= releaseTTL {
		t.Fatalf("o TTL do erro (%v) deve ser menor que o TTL do sucesso (%v)", releaseErrTTL, releaseTTL)
	}
	c.at = time.Now().Add(-releaseErrTTL - time.Second)
	if _, err := c.get(t.Context(), nil, false); err == nil {
		t.Fatal("esperava-se que retornasse erro")
	}
	if calls != 2 {
		t.Errorf("após o TTL do erro expirar deveria tentar de novo, esperado 2 vezes, foram %d vezes", calls)
	}
}

func TestReleaseCacheDoesNotPoisonOnCallerCancel(t *testing.T) {
	good := &selfupdate.Release{TagName: "v0.3.8"}
	c := newTestCache(func(ctx context.Context, _ *http.Client) (*selfupdate.Release, error) {
		return good, nil
	})
	if _, err := c.get(t.Context(), nil, false); err != nil {
		t.Fatal(err)
	}

	// O visitante fechar a aba cancela a requisição. Isso não significa que o GitHub tem problema, jamais se pode gravar "cancelado"
	// no cache — senão pelos 30 minutos seguintes cada visitante receberia um erro misterioso.
	c.fetch = func(ctx context.Context, _ *http.Client) (*selfupdate.Release, error) {
		return nil, ctx.Err()
	}
	c.at = time.Now().Add(-releaseTTL - time.Second) // faz o cache expirar, forçando-o a ir à origem

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.get(ctx, nil, false); err == nil {
		t.Fatal("quando o chamador cancelou, o erro deveria ser repassado a ele")
	}

	// Invariante-chave: a chamada cancelada não deixa nenhum rastro — no cache não há esse erro de "cancelado",
	// e o bom resultado anterior continua preservado.
	if c.err != nil {
		t.Fatalf("o erro de cancelamento não deveria ser gravado no cache, obtido %v", c.err)
	}
	if c.rel == nil || c.rel.TagName != "v0.3.8" {
		t.Fatalf("o cache deveria preservar o bom resultado anterior, obtido %+v", c.rel)
	}

	// Aquele cancelamento não trouxe nenhum dado novo, então o próximo visitante deve voltar à origem — e conseguir o resultado normalmente,
	// sem ser prejudicado pelo cancelamento anterior.
	c.fetch = func(context.Context, *http.Client) (*selfupdate.Release, error) {
		return good, nil
	}
	rel, err := c.get(t.Context(), nil, false)
	if err != nil {
		t.Fatalf("uma requisição normal depois do cancelamento não deveria dar erro: %v", err)
	}
	if rel == nil || rel.TagName != "v0.3.8" {
		t.Fatalf("deveria obter um resultado normal, obtido %+v", rel)
	}
}
