package api

import (
	"net/http"

	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/task"
)

// Quadro de tarefas do workspace — docs/API.md → "Quadro de tarefas". Workspace restrito: só membros.

const maxTaskChanges = 500

func (s *Server) canFunc(r *http.Request) (func(string) bool, error) {
	a, err := s.access(r)
	if err != nil {
		return nil, err
	}
	return func(ws string) bool { return a.Can(&ws) }, nil
}

func (s *Server) pullTasks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Since      int64    `json:"since"`
		Workspaces []string `json:"workspaces"`
	}
	if !decode(w, r, &body) {
		return
	}
	can, err := s.canFunc(r)
	if err != nil {
		fail(w, err)
		return
	}
	res, err := s.tasks.Pull(r.Context(), body.Since, body.Workspaces, can)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) pushTasks(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Changes []task.Change `json:"changes"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Changes) > maxTaskChanges {
		writeError(w, http.StatusBadRequest, "No máximo 500 mudanças por envio.")
		return
	}
	can, err := s.canFunc(r)
	if err != nil {
		fail(w, err)
		return
	}
	results, err := s.tasks.Push(r.Context(), body.Changes, auth.UserFrom(r.Context()).ID, can)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
