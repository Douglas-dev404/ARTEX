package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Autumn-27/artex/db"
)

// Interface de CRUD manual de "Gestão de metas" da visão geral. Grava os mesmos nós goal da ferramenta set_goals do lado do agent,
// mas a entrada é o humano adicionando/removendo/editando direto na UI; após criar/modificar reutiliza a lógica de "reviver tarefa" (admitTask resume:
// terminal→running, remove pausa, enfileira se preciso); a exclusão não revive (decisão de produto). Todo handler de alteração passa por
// beginTaskOperation/decInflight, evitando corrida com a exclusão de tarefa (igual ao CRUD de intenções).

// listGoals retorna todas as metas desta tarefa (com text/vulnclass/state separados), para o card de gestão de metas renderizar.
func (s *Server) listGoals(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	goals, err := t.Store.ListByKind(db.KindGoal, 10000)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"goals": goalDTOs(goals)})
}

// addGoal adiciona manualmente uma meta: grava no banco (pendurada sob spawns da raiz da tarefa) → registra um gatilho "meta adicionada" que acorda o
// planner → revive a tarefa, para o planner reavaliar se foi atingida com base na nova meta.
func (s *Server) addGoal(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if !s.engine.beginTaskOperation(t.ID) {
		writeErr(w, 409, "a tarefa está sendo excluída, não é possível adicionar meta")
		return
	}
	defer s.engine.decInflight(t.ID)

	var body struct {
		Text      string `json:"text"`
		VulnClass string `json:"vulnclass"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeErr(w, 400, "o conteúdo da meta não pode ser vazio")
		return
	}
	payload := map[string]any{"text": text}
	if vc := strings.TrimSpace(body.VulnClass); vc != "" {
		payload["vulnclass"] = vc
	}
	id, err := t.Store.AddGoal(payload, "human")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if of, _ := t.Store.OriginFactID(); of > 0 && id > 0 {
		_ = t.Store.Link(of, db.RelSpawns, id) // goal descends from the task root (origin fact)
	}
	t.NotifyGoal([]string{text}) // registra o gatilho "pessoa adicionou meta: …" e acorda o planner
	s.reviveTask(t)              // puxa de volta ao estado de execução a tarefa concluída/pausada para continuar rodando
	node, _ := t.Store.GetNode(id)
	if node == nil {
		writeErr(w, 500, "falha ao reler a meta depois de gravá-la")
		return
	}
	writeJSON(w, 200, goalDTO(node))
}

// editGoal modifica manualmente o texto de uma meta (e vulnclass): altera o banco → registra o gatilho "usuário alterou a meta de old para new"
// que acorda o planner → revive a tarefa, para o planner ajustar a direção com base na nova meta.
func (s *Server) editGoal(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if !s.engine.beginTaskOperation(t.ID) {
		writeErr(w, 409, "a tarefa está sendo excluída, não é possível modificar meta")
		return
	}
	defer s.engine.decInflight(t.ID)

	gid, err := strconv.ParseInt(r.PathValue("gid"), 10, 64)
	if err != nil || gid <= 0 {
		writeErr(w, 400, "bad goal id")
		return
	}
	var body struct {
		Text      string `json:"text"`
		VulnClass string `json:"vulnclass"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, 400, "invalid JSON")
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeErr(w, 400, "o conteúdo da meta não pode ser vazio")
		return
	}
	node, err := t.Store.GetNode(gid)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if node == nil || node.Kind != db.KindGoal {
		writeErr(w, 404, "a meta não existe")
		return
	}
	oldText := goalDTO(node).Text
	if err := t.Store.UpdateGoalPayload(gid, text, strings.TrimSpace(body.VulnClass)); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	t.NotifyGoalEdited(oldText, text) // registra o gatilho "pessoa alterou a meta de old para new" e acorda o planner
	s.reviveTask(t)                   // igual à adição: revive a tarefa para reavaliar com base na nova meta
	updated, _ := t.Store.GetNode(gid)
	if updated == nil {
		writeErr(w, 500, "falha ao reler a meta depois de atualizá-la")
		return
	}
	writeJSON(w, 200, goalDTO(updated))
}

// deleteGoal exclui manualmente uma meta (exclusão física, em cascata remove arestas/âncoras): apaga do banco → registra o gatilho "usuário excluiu esta meta X"
// que acorda o planner para reavaliar as metas restantes. Por decisão de produto, a exclusão [NÃO] revive a tarefa.
func (s *Server) deleteGoal(w http.ResponseWriter, r *http.Request) {
	t, ok := s.m.Task(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "task not found")
		return
	}
	if !s.engine.beginTaskOperation(t.ID) {
		writeErr(w, 409, "a tarefa está sendo excluída, não é possível excluir meta")
		return
	}
	defer s.engine.decInflight(t.ID)

	gid, err := strconv.ParseInt(r.PathValue("gid"), 10, 64)
	if err != nil || gid <= 0 {
		writeErr(w, 400, "bad goal id")
		return
	}
	node, err := t.Store.GetNode(gid)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if node == nil || node.Kind != db.KindGoal {
		writeErr(w, 404, "a meta não existe")
		return
	}
	text := goalDTO(node).Text
	if err := t.Store.DeleteGoal(gid); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	t.NotifyGoalDeleted(text) // registra o gatilho "pessoa excluiu esta meta: …" e acorda o planner (sem reviver a tarefa)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
