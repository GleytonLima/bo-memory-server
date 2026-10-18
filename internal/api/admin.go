package api

import (
	"fmt"
	"net/http"
	"time"

	"bo-memory-server/internal/auth"
)

// listWorkspaces: só os que o usuário pode ver (admin vê todos).
func (s *Server) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	items, err := s.memory.Workspaces(r.Context())
	if err != nil {
		fail(w, err)
		return
	}
	a, err := s.access(r)
	if err != nil {
		fail(w, err)
		return
	}
	out := items[:0]
	for _, it := range items {
		if a.Can(&it.Key) {
			out = append(out, it)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// members (admin): quem pertence ao workspace — e-mails ou *@dominio; vazio = aberto.
func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) {
	items, err := s.memory.Members(r.Context(), r.PathValue("key"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": items})
}

func (s *Server) setMembers(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Members []string `json:"members"`
	}
	if !decode(w, r, &body) {
		return
	}
	items, err := s.memory.SetMembers(r.Context(), r.PathValue("key"), body.Members, auth.UserFrom(r.Context()).ID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": items})
}

func (s *Server) listFolders(w http.ResponseWriter, r *http.Request) {
	items, err := s.memory.Folders(r.Context())
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

const maxTokenDays = 365

// createToken: para si mesmo; o admin pode emitir para outro usuário (userId). O valor só aparece aqui.
func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name          string `json:"name"`
		UserID        string `json:"userId"`
		ExpiresInDays int    `json:"expiresInDays"` // 1 a 365; 0 = o padrão (BO_TOKEN_TTL_DAYS)
	}
	if !decode(w, r, &body) {
		return
	}
	// pela API, qualquer token vence: uma sessão SSO não vira acesso eterno (quem sai da empresa perde o acesso)
	ttl := s.auth.PersonalTTL
	if body.ExpiresInDays != 0 {
		if body.ExpiresInDays < 1 || body.ExpiresInDays > maxTokenDays {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("Validade entre 1 e %d dias.", maxTokenDays))
			return
		}
		ttl = time.Duration(body.ExpiresInDays) * 24 * time.Hour
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
	token, id, expiresAt, err := s.auth.CreateTokenFor(r.Context(), target, body.Name, ttl)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "token": token, "expiresAt": expiresAt})
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.RevokeToken(r.Context(), r.PathValue("id"), auth.UserFrom(r.Context())); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
