package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/memory"
)

// Acesso por membros dos workspaces (ver memory/members.go).

func (s *Server) access(r *http.Request) (memory.Access, error) {
	u := auth.UserFrom(r.Context())
	return s.memory.AccessFor(r.Context(), u.Email, u.IsAdmin)
}

func forbidden(ws string) string { return fmt.Sprintf("Você não é membro do projeto %q.", ws) }

// denied devolve a mensagem do primeiro workspace não permitido ("" se todos são).
func denied(a memory.Access, workspaces ...*string) string {
	for _, ws := range workspaces {
		if !a.Can(ws) {
			return forbidden(*ws)
		}
	}
	return ""
}

// allowAll responde 403 (e devolve false) se algum dos workspaces não for permitido.
func (s *Server) allowAll(w http.ResponseWriter, r *http.Request, workspaces ...*string) bool {
	a, err := s.access(r)
	if err != nil {
		fail(w, err)
		return false
	}
	if msg := denied(a, workspaces...); msg != "" {
		writeError(w, http.StatusForbidden, msg)
		return false
	}
	return true
}

// workspaceOf: o workspace atual da memória (nil se não existe ou não tem).
func (s *Server) workspaceOf(r *http.Request, id string) (*string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return nil, nil // memória nova (o servidor gera o id) ou id inválido (a validação recusa depois)
	}
	cur, err := s.memory.Get(r.Context(), id)
	if errors.Is(err, memory.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return cur.Workspace, nil
}

// allowMemory: a memória existente (se houver) e o workspace pedido precisam ser permitidos.
func (s *Server) allowMemory(w http.ResponseWriter, r *http.Request, id string, target *string) bool {
	existing, err := s.workspaceOf(r, id)
	if err != nil {
		fail(w, err)
		return false
	}
	return s.allowAll(w, r, existing, target)
}

// allowed: só as memórias de workspaces permitidos (as de pasta também têm o workspace de origem).
func allowed(a memory.Access, items []memory.Memory) []memory.Memory {
	out := make([]memory.Memory, 0, len(items))
	for _, m := range items {
		if a.Can(m.Workspace) {
			out = append(out, m)
		}
	}
	return out
}

// visible: só os workspaces permitidos (pull e listagens).
func visible(a memory.Access, workspaces []string) []string {
	out := []string{}
	for _, ws := range workspaces {
		if a.Can(&ws) {
			out = append(out, ws)
		}
	}
	return out
}
