package api

import (
	"net/http"

	"bo-memory-server/internal/auth"
)

func (s *Server) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	items, err := s.memory.Workspaces(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var body struct{ Key, Name string }
	if !decode(w, r, &body) {
		return
	}
	if err := s.memory.CreateWorkspace(r.Context(), body.Key, body.Name); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, body)
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.auth.ListUsers(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": users})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Email   string `json:"email"`
		IsAdmin bool   `json:"isAdmin"`
	}
	if !decode(w, r, &body) {
		return
	}
	u, err := s.auth.CreateUser(r.Context(), body.Name, body.Email, body.IsAdmin)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

// listTokens: o admin vê todos; os demais, só os próprios.
func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	owner := user.ID
	if user.IsAdmin {
		owner = ""
	}
	items, err := s.auth.ListTokens(r.Context(), owner)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// createToken: para si mesmo; o admin pode emitir para outro usuário (userId). O valor só aparece aqui.
func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		UserID string `json:"userId"`
	}
	if !decode(w, r, &body) {
		return
	}
	user := auth.UserFrom(r.Context())
	target := user.ID
	if body.UserID != "" && body.UserID != user.ID {
		if !user.IsAdmin {
			writeError(w, http.StatusForbidden, "Apenas administradores emitem tokens para outros usuários.")
			return
		}
		target = body.UserID
	}
	token, id, err := s.auth.CreateToken(r.Context(), target, body.Name)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id, "token": token})
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.RevokeToken(r.Context(), r.PathValue("id"), auth.UserFrom(r.Context())); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
