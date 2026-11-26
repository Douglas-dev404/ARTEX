package db

import (
	"fmt"
	"testing"
	"time"
)

// InsertSettingIfAbsent é a salvaguarda de auth.password_hash: a verificação GetSetting do chamador pode falhar
// por erro do banco, e também pode ser furada por requisições concorrentes (o bcrypt leva dezenas de milissegundos), então a garantia de "só pode ser
// definido na primeira vez" precisa se apoiar na restrição de chave primária e não no if da camada superior.
func TestInsertSettingIfAbsentDoesNotOverwrite(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v)", err)
	}
	defer d.Close()

	// Usa uma key exclusiva do teste, nunca tocando o auth.password_hash real do banco de desenvolvimento.
	key := fmt.Sprintf("test.insert_if_absent.%d", time.Now().UnixNano())
	defer func() { _, _ = d.Exec(`DELETE FROM settings WHERE key=$1`, key) }()

	inserted, err := d.InsertSettingIfAbsent(key, "first")
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("a primeira gravação deveria retornar inserted=true")
	}

	inserted, err = d.InsertSettingIfAbsent(key, "second")
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Fatal("quando a key já existe deveria retornar inserted=false")
	}

	got, ok, err := d.GetSetting(key)
	if err != nil || !ok {
		t.Fatalf("GetSetting: ok=%v err=%v", ok, err)
	}
	if got != "first" {
		t.Fatalf("o valor foi sobrescrito para %q, deveria permanecer %q", got, "first")
	}
}
