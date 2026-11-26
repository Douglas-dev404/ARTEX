package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Autumn-27/artex/db"
	actool "github.com/Autumn-27/norma/tool"
)

// Ferramentas de operação da plataforma (para o agent Auto integrado): criar/editar skill, ferramentas personalizadas, MCP. Todas são host tools,
// semeadas na tabela tools, vinculadas por padrão ao auto, injetadas via hostTools. Reutilizam a lógica existente de db/sistema de arquivos.

func (s *Server) platformTools() []actool.CoreTool {
	return []actool.CoreTool{
		s.toolCreateSkill(),
		s.toolUpdateSkillFile(),
		s.toolCreateCustomTool(),
		s.toolUpdateCustomTool(),
		s.toolCreateMCP(),
		s.toolUpdateMCP(),
		s.toolDeleteAssetsByHost(),
	}
}

// platformToolKeys are the tool keys the Auto agent gets bound by default.
var platformToolKeys = []string{
	"create_skill", "update_skill_file",
	"create_custom_tool", "update_custom_tool",
	"create_mcp", "update_mcp",
	"delete_assets_by_host",
}

// ---- assets ----

// toolDeleteAssetsByHost hard-deletes every asset tied to one host (exact match).
// Platform-level (not a per-task tool): operates on the global, cross-task base de ativos.
func (s *Server) toolDeleteAssetsByHost() actool.CoreTool {
	return wrTool("delete_assets_by_host",
		"Exclui ativos por host exato: remove os domínios/subdomínios desse host, e os serviços (service) e interfaces (endpoint) sob eles.\n"+
			"O host precisa corresponder exatamente (minúsculas, sem espaços), não é busca aproximada/curinga.\n"+
			"Informar o domínio raiz (como example.com) apaga junto seus subdomínios e seus serviços/interfaces; informar um subdomínio (como a.example.com) ou IP apaga só esse próprio host e seus serviços/interfaces.\n"+
			"⚠️ Exclusão física, atua na base global de ativos (compartilhada entre tarefas), irreversível.",
		objSchema(map[string]any{
			"host": strParam("host a excluir: domínio/subdomínio/IP. Correspondência exata, como example.com ou a.example.com ou 1.2.3.4"),
		}, "host"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			as := s.assetStore()
			if as == nil {
				return actool.Errorf("a base de ativos não foi inicializada"), nil
			}
			var a struct {
				Host string `json:"host"`
			}
			_ = json.Unmarshal(in, &a)
			if strings.TrimSpace(a.Host) == "" {
				return actool.Errorf("host não pode ser vazio"), nil
			}
			counts, err := as.DeleteByHost(a.Host)
			if err != nil {
				return actool.Errorf("falha ao excluir: " + err.Error()), nil
			}
			var total int64
			for _, n := range counts {
				total += n
			}
			return jsonResult(map[string]any{
				"host":            a.Host,
				"deleted":         total,
				"deleted_by_type": counts,
			})
		})
}

// ---- skills ----

func (s *Server) toolCreateSkill() actool.CoreTool {
	return wrTool("create_skill",
		"Cria um novo skill (escreve SKILL.md, especificação agentskills.io). name em minúsculas/dígitos/hífens.",
		objSchema(map[string]any{
			"name":         strParam("nome do skill (começa com letra minúscula, letras/dígitos/hífens)"),
			"description":  strParam("descrição do skill (obrigatória, explica o que faz/quando usar)"),
			"instructions": strParam("Instruções em Markdown (opcional)"),
		}, "name", "description"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct{ Name, Description, Instructions string }
			_ = json.Unmarshal(in, &a)
			if !validSkillName(a.Name) {
				return actool.Errorf("nome de skill inválido (começa com letra minúscula, apenas letras/dígitos/hífens, ≤64)"), nil
			}
			if strings.TrimSpace(a.Description) == "" {
				return actool.Errorf("description é obrigatória"), nil
			}
			path := filepath.Join(s.skillDir, a.Name)
			if _, err := os.Stat(path); err == nil {
				return actool.Errorf("o skill já existe: " + a.Name), nil
			}
			if err := os.MkdirAll(path, 0o755); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			var b strings.Builder
			b.WriteString("---\n")
			fmt.Fprintf(&b, "name: %s\n", a.Name)
			fmt.Fprintf(&b, "description: %s\n", a.Description)
			b.WriteString("---\n")
			if strings.TrimSpace(a.Instructions) != "" {
				b.WriteString(a.Instructions)
			} else {
				fmt.Fprintf(&b, "## %s\n\n1. \n2. \n3. \n", a.Name)
			}
			if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(b.String()), 0o644); err != nil {
				_ = os.RemoveAll(path)
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("skill created: " + a.Name), nil
		})
}

func (s *Server) toolUpdateSkillFile() actool.CoreTool {
	return wrTool("update_skill_file",
		"Escreve/sobrescreve um arquivo dentro de um skill (padrão SKILL.md). Usado para modificar o conteúdo do skill ou adicionar scripts/referências.",
		objSchema(map[string]any{
			"name":    strParam("nome do skill"),
			"file":    strParam("caminho relativo (opcional, padrão SKILL.md, como scripts/run.py)"),
			"content": strParam("conteúdo completo do arquivo"),
		}, "name", "content"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a struct{ Name, File, Content string }
			_ = json.Unmarshal(in, &a)
			if !validSkillName(a.Name) {
				return actool.Errorf("nome de skill inválido"), nil
			}
			skillPath := filepath.Join(s.skillDir, a.Name)
			if _, err := os.Stat(skillPath); os.IsNotExist(err) {
				return actool.Errorf("o skill não existe: " + a.Name), nil
			}
			rel := strings.TrimSpace(a.File)
			if rel == "" {
				rel = "SKILL.md"
			}
			clean, msg := skillRelPath(rel)
			if msg != "" {
				return actool.Errorf("caminho inválido: " + msg), nil
			}
			full := filepath.Join(skillPath, clean)
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if err := os.WriteFile(full, []byte(a.Content), 0o644); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("skill file written: " + a.Name + "/" + clean), nil
		})
}

// ---- custom tools ----

type customToolToolInput struct {
	Key         string          `json:"key"`
	Description string          `json:"description"`
	Kind        string          `json:"kind"`
	Exec        json.RawMessage `json:"exec"`
	Schema      json.RawMessage `json:"schema"`
	Agents      []string        `json:"agents"`
	Deferred    bool            `json:"deferred"`
	Enabled     *bool           `json:"enabled"`
}

func customToolSchema(keyDesc string) map[string]any {
	return objSchema(map[string]any{
		"key":         strParam(keyDesc),
		"description": strParam("descrição enviada ao modelo"),
		"kind":        strParam("shell | command | script (somente Python) | http. shell = declaração de ambiente bash (só informa ao modelo que a ferramenta pode ser chamada diretamente no bash, sem exec/schema); os outros três exigem exec"),
		"exec":        map[string]any{"type": "object", "description": "especificação de execução (tipo shell não precisa): command→{command}; script→{code}; http→{method,url,headers,body,proxy,use_recording_proxy}"},
		"schema":      map[string]any{"type": "object", "description": "JSON-Schema dos parâmetros (shell/command/script podem ficar vazios; http é obrigatório e deve conter properties)"},
		"agents":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "keys dos agents vinculados (opcional)"},
		"deferred":    map[string]any{"type": "boolean", "description": "se é diferido (sem efeito no tipo shell; só ligue para ferramentas pouco usadas de command/script/http)"},
		"enabled":     map[string]any{"type": "boolean", "description": "se está habilitado (padrão true)"},
	}, "key", "kind")
}

func toDBTool(a customToolToolInput) *db.Tool {
	enabled := true
	if a.Enabled != nil {
		enabled = *a.Enabled
	}
	return &db.Tool{
		Key: a.Key, Description: a.Description, Schema: a.Schema, Agents: a.Agents,
		Enabled: enabled, Kind: a.Kind, Exec: a.Exec, Deferred: a.Deferred,
	}
}

func (s *Server) toolCreateCustomTool() actool.CoreTool {
	return wrTool("create_custom_tool", "[IMPORTANTE] Ao instalar uma ferramenta que a plataforma não tem, chame esta ferramenta para colocar a ferramenta instalada na plataforma, permitindo que a plataforma a chame! Cria uma ferramenta personalizada (shell/command/script/http). shell = declaração de ambiente bash, só precisa de key+description+agents, sem exec/schema.",
		customToolSchema("key da ferramenta (começa com letra minúscula, letras/dígitos/sublinhado)"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a customToolToolInput
			_ = json.Unmarshal(in, &a)
			a.Key = strings.TrimSpace(a.Key)
			if !reToolKey.MatchString(a.Key) {
				return actool.Errorf("key deve começar com letra minúscula e conter apenas minúsculas/dígitos/sublinhado"), nil
			}
			if a.Kind != "shell" && a.Kind != "command" && a.Kind != "script" && a.Kind != "http" {
				return actool.Errorf("kind deve ser shell / command / script / http"), nil
			}
			if a.Kind == "http" && !hasSchemaProps(a.Schema) {
				return actool.Errorf("ferramenta http deve informar o JSON Schema dos parâmetros (não pode ficar vazio)"), nil
			}
			if exist, _ := s.m.pg.GetTool(a.Key); exist != nil {
				return actool.Errorf("esta key já existe: " + a.Key), nil
			}
			if err := s.m.pg.CreateCustomTool(toDBTool(a)); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("custom tool created: " + a.Key), nil
		})
}

func (s *Server) toolUpdateCustomTool() actool.CoreTool {
	return wrTool("update_custom_tool", "Modifica uma ferramenta personalizada existente (por key).",
		customToolSchema("key da ferramenta personalizada a modificar"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a customToolToolInput
			_ = json.Unmarshal(in, &a)
			existing, _ := s.m.pg.GetTool(a.Key)
			if existing == nil || existing.System {
				return actool.Errorf("só é possível modificar ferramentas personalizadas: " + a.Key), nil
			}
			if a.Kind != "shell" && a.Kind != "command" && a.Kind != "script" && a.Kind != "http" {
				return actool.Errorf("kind deve ser shell / command / script / http"), nil
			}
			if a.Kind == "http" && !hasSchemaProps(a.Schema) {
				return actool.Errorf("ferramenta http deve informar o JSON Schema dos parâmetros (não pode ficar vazio)"), nil
			}
			if err := s.m.pg.UpdateCustomTool(toDBTool(a)); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text("custom tool updated: " + a.Key), nil
		})
}

// ---- MCP ----

type mcpToolInput struct {
	ID        int64           `json:"id"`
	Name      string          `json:"name"`
	Transport string          `json:"transport"`
	Command   string          `json:"command"`
	Args      json.RawMessage `json:"args"`
	Env       json.RawMessage `json:"env"`
	URL       string          `json:"url"`
	Enabled   *bool           `json:"enabled"`
	Insecure  *bool           `json:"insecure"`
}

func mcpSchema(withID bool) map[string]any {
	props := map[string]any{
		"name":      strParam("nome do servidor MCP"),
		"transport": strParam("stdio | http / sse"),
		"command":   strParam("comando de inicialização do stdio (como npx)"),
		"args":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "array de argumentos do comando"},
		"env":       map[string]any{"type": "object", "description": "variáveis de ambiente {KEY:VALUE}"},
		"url":       strParam("URL de http/sse"),
		"enabled":   map[string]any{"type": "boolean", "description": "se está habilitado (padrão true)"},
		"insecure":  map[string]any{"type": "boolean", "description": "http: ignora a verificação do certificado TLS (defina true com certificado autoassinado, padrão false)"},
	}
	required := []string{"name", "transport"}
	if withID {
		props["id"] = map[string]any{"type": "integer", "description": "id do servidor MCP a modificar"}
		required = []string{"id", "name", "transport"}
	}
	return objSchema(props, required...)
}

func (a mcpToolInput) toDB() *db.MCPServer {
	enabled := true
	if a.Enabled != nil {
		enabled = *a.Enabled
	}
	insecure := false
	if a.Insecure != nil {
		insecure = *a.Insecure
	}
	return &db.MCPServer{
		ID: a.ID, Name: a.Name, Transport: a.Transport, Command: a.Command,
		Args: a.Args, Env: a.Env, URL: a.URL, Enabled: enabled, Insecure: insecure,
	}
}

func (s *Server) toolCreateMCP() actool.CoreTool {
	return wrTool("create_mcp", "Cria um servidor MCP (stdio/http/sse). Depois de criado, suas ferramentas precisam ser autorizadas conforme a visibilidade por agent.",
		mcpSchema(false),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a mcpToolInput
			_ = json.Unmarshal(in, &a)
			a.ID = 0
			if strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Transport) == "" {
				return actool.Errorf("name / transport são obrigatórios"), nil
			}
			id, err := s.m.pg.SaveMCP(a.toDB())
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text(fmt.Sprintf("mcp created: id=%d name=%s", id, a.Name)), nil
		})
}

func (s *Server) toolUpdateMCP() actool.CoreTool {
	return wrTool("update_mcp", "Modifica um servidor MCP existente (por id).",
		mcpSchema(true),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			var a mcpToolInput
			_ = json.Unmarshal(in, &a)
			if a.ID == 0 {
				return actool.Errorf("id é obrigatório"), nil
			}
			if _, err := s.m.pg.SaveMCP(a.toDB()); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return actool.Text(fmt.Sprintf("mcp updated: id=%d", a.ID)), nil
		})
}
