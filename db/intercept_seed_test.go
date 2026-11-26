package db

import (
	"regexp"
	"testing"
)

// A regra embutida "caminho de API de exclusão" casa com a string JSON inteira de tool_input, por isso os casos são dados diretamente
// na forma JSON, igual ao subject que o Interceptor realmente recebe.
func TestDeleteEndpointPathPattern(t *testing.T) {
	re := regexp.MustCompile(deleteEndpointPathPattern)

	hit := []string{
		`{"command":"curl -s 'http://t.com/api/user/delete?id=1'"}`,    // GET na API de exclusão
		`{"command":"curl -X POST http://t.com/admin/delete -d id=1"}`, // POST na API de exclusão
		`{"command":"curl 'http://t.com/api/deleteAll'"}`,
		`{"command":"curl 'http://t.com/api/delete_user?id=1'"}`,
		`{"command":"curl 'http://t.com/api/delete-user?id=1'"}`,
		`{"url":"http://t.com/api/remove?id=1"}`,
		`{"command":"curl http://t.com/files/unlink/3"}`,
		`{"command":"curl http://t.com/api/del?id=2"}`,
		`{"command":"curl -X POST http://t/v1/erase"}`,
		`{"command":"curl http://t/admin/destroyAll"}`, // a regra de caminho da v1 não permite sufixo, aqui é acrescentado
	}
	for _, s := range hit {
		if !re.MatchString(s) {
			t.Errorf("deveria ter casado mas foi liberado: %s", s)
		}
	}

	// O verbo deve ser seguido de separador, para que caminhos somente leitura como /delivery e /details não sejam interceptados por engano.
	miss := []string{
		`{"command":"curl 'http://t.com/api/delivery?id=1'"}`,
		`{"command":"curl 'http://t.com/order/details'"}`,
		`{"command":"curl 'http://t.com/api/delta/sync'"}`,
		`{"command":"curl 'http://t.com/user/delegate'"}`,
		`{"command":"curl 'http://delete.example.com/'"}`, // o verbo de exclusão aparece no domínio e não no caminho
		`{"command":"curl 'http://t.com/remote/status'"}`,
		`{"command":"nmap -p80 10.0.0.1"}`,
	}
	for _, s := range miss {
		if re.MatchString(s) {
			t.Errorf("interceptado por engano: %s", s)
		}
	}
}
