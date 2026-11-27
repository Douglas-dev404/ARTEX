package db

import "fmt"

// builtinRuleTranslation liga o texto original (chinês) de uma regra de interceptação
// embutida ao texto em português que os seeds passaram a gravar.
type builtinRuleTranslation struct {
	oldName, newName       string
	oldMessage, newMessage string
}

// builtinRulesPtBR lista as regras semeadas por seedDefaultInterceptRules (v1, v2 e v3).
// Os seeds só rodam uma vez por banco, então bancos criados antes da localização
// continuam com os nomes em chinês. translateBuiltinInterceptRulesPtBR usa esta
// lista para traduzi-los sem tocar no que o usuário personalizou.
var builtinRulesPtBR = []builtinRuleTranslation{
	{"[内置] 递归强制删除 rm -rf", "[Embutida] Exclusão recursiva forçada rm -rf",
		"禁止执行递归强制删除（rm -rf / rm --recursive），可能永久损坏系统或靶机环境",
		"Proibido executar exclusão recursiva forçada (rm -rf / rm --recursive): pode danificar de forma permanente o sistema ou o ambiente alvo"},
	{"[内置] 删除系统关键目录", "[Embutida] Exclusão de diretórios críticos do sistema",
		"禁止删除系统关键路径",
		"Proibido excluir caminhos críticos do sistema"},
	{"[内置] 磁盘格式化 mkfs", "[Embutida] Formatação de disco mkfs",
		"禁止格式化磁盘（mkfs）",
		"Proibido formatar disco (mkfs)"},
	{"[内置] 覆写磁盘设备 dd", "[Embutida] Sobrescrita de dispositivo de disco dd",
		"禁止使用 dd 覆写磁盘设备",
		"Proibido usar dd para sobrescrever dispositivo de disco"},
	{"[内置] Fork 炸弹", "[Embutida] Fork bomb",
		"禁止执行 Fork 炸弹",
		"Proibido executar fork bomb"},
	{"[内置] 关机 / 重启", "[Embutida] Desligar / reiniciar",
		"禁止执行关机或重启命令",
		"Proibido executar comandos de desligar ou reiniciar"},
	{"[内置] 杀死全部进程", "[Embutida] Matar todos os processos",
		"禁止 kill -9 -1 或 killall -9（杀死所有进程）",
		"Proibido kill -9 -1 ou killall -9 (mata todos os processos)"},
	{"[内置] 磁盘擦除 shred / wipe", "[Embutida] Apagamento de disco shred / wipe",
		"禁止对磁盘设备执行 shred/wipe 擦除",
		"Proibido apagar dispositivo de disco com shred/wipe"},
	{"[内置] 清空防火墙规则", "[Embutida] Limpeza das regras de firewall",
		"禁止清空防火墙规则（iptables -F / nft flush）",
		"Proibido limpar as regras de firewall (iptables -F / nft flush)"},
	{"[内置] SQL DROP DATABASE / TABLE / SCHEMA", "[Embutida] SQL DROP DATABASE / TABLE / SCHEMA",
		"禁止执行 DROP 操作，可能不可逆地销毁数据库对象",
		"Proibido executar DROP: pode destruir objetos do banco de forma irreversível"},
	{"[内置] SQL TRUNCATE", "[Embutida] SQL TRUNCATE",
		"禁止执行 TRUNCATE，可能清空数据表所有数据",
		"Proibido executar TRUNCATE: pode apagar todos os dados da tabela"},
	{"[内置] MongoDB drop / dropDatabase", "[Embutida] MongoDB drop / dropDatabase",
		"禁止执行 MongoDB drop 操作",
		"Proibido executar drop no MongoDB"},
	{"[内置] Redis FLUSHALL / FLUSHDB", "[Embutida] Redis FLUSHALL / FLUSHDB",
		"禁止执行 Redis FLUSHALL / FLUSHDB，可能清空全部缓存数据",
		"Proibido executar FLUSHALL / FLUSHDB no Redis: pode apagar todo o cache"},
	{"[内置] curl / wget 发送 DELETE 请求", "[Embutida] curl / wget enviando requisição DELETE",
		"禁止通过 curl/wget 发送 HTTP DELETE 请求，可能删除目标系统数据",
		"Proibido enviar HTTP DELETE com curl/wget: pode excluir dados do sistema alvo"},
	{"[内置] Python HTTP 客户端 DELETE（requests/httpx/aiohttp）", "[Embutida] DELETE por cliente HTTP Python (requests/httpx/aiohttp)",
		"禁止使用 Python HTTP 客户端发送 DELETE 请求",
		"Proibido enviar requisição DELETE com cliente HTTP Python"},
	{"[内置] 脚本中声明 HTTP DELETE 方法（JS/通用）", "[Embutida] Método HTTP DELETE declarado em script (JS/genérico)",
		"禁止在脚本中声明并发送 HTTP DELETE 请求",
		"Proibido declarar e enviar requisição HTTP DELETE em script"},
	{"[内置] 批量清空 / 清除接口路径", "[Embutida] Caminhos de endpoint de limpeza em massa",
		"禁止调用批量清空或销毁类接口（/clear /wipe /flush /purge 等）",
		"Proibido chamar endpoints de limpeza ou destruição em massa (/clear /wipe /flush /purge etc.)"},
	{"[内置] 破坏性系统命令", "[Embutida] Comandos destrutivos do sistema",
		"破坏性命令被拒绝（rm -rf / / mkfs / dd / fork bomb / 关机重启 / 覆写磁盘设备）",
		"Comando destrutivo recusado (rm -rf / / mkfs / dd / fork bomb / desligar ou reiniciar / sobrescrever disco)"},
	{"[内置] 数据外泄管道", "[Embutida] Pipe de exfiltração de dados",
		"疑似数据外泄管道被拒绝（命令输出经 curl/wget/nc 外传）",
		"Possível pipe de exfiltração recusado (saída do comando enviada para fora por curl/wget/nc)"},
	{"[内置] 删除类接口路径", deleteEndpointRuleName,
		"禁止调用删除类接口（/delete /remove /unlink /erase 等），不论使用哪种 HTTP 方法——多数应用的删除接口用 GET/POST 就能触发，同样会真实删除目标数据",
		deleteEndpointRuleMessage},
}

const (
	deleteEndpointRuleName    = "[Embutida] Caminhos de endpoint de exclusão"
	deleteEndpointRuleMessage = "Proibido chamar endpoints de exclusão (/delete /remove /unlink /erase etc.), qualquer que seja o método HTTP: na maioria das aplicações o endpoint de exclusão funciona com GET/POST e apaga dados reais do alvo"
)

// translateBuiltinInterceptRulesPtBR traduz, uma única vez por banco, as regras
// embutidas que ainda estão com o texto original. Só renomeia linhas cujo nome é
// exatamente o original, e só troca a mensagem se ela também é exatamente a original:
// regra renomeada ou mensagem editada pelo usuário fica como está.
func (d *DB) translateBuiltinInterceptRulesPtBR() error {
	if v, _, _ := d.GetSetting("intercept_rules_ptbr_v1"); v == "done" {
		return nil
	}
	for _, r := range builtinRulesPtBR {
		if _, err := d.Exec(`UPDATE intercept_rules SET message = $2 WHERE name = $1 AND message = $3`,
			r.oldName, r.newMessage, r.oldMessage); err != nil {
			return fmt.Errorf("rule %q message: %w", r.oldName, err)
		}
		if _, err := d.Exec(`UPDATE intercept_rules SET name = $2 WHERE name = $1`, r.oldName, r.newName); err != nil {
			return fmt.Errorf("rule %q name: %w", r.oldName, err)
		}
	}
	return d.SetSetting("intercept_rules_ptbr_v1", "done")
}
