package api

import (
	"net/http"

	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/memory"
	"bo-memory-server/internal/web"
)

type Server struct {
	auth   *auth.Store
	memory *memory.Store
}

// Handler monta as rotas: /healthz e a web sem autenticação; /v1/* exige token.
func Handler(authStore *auth.Store, memStore *memory.Store, webDir string) http.Handler {
	s := &Server{auth: authStore, memory: memStore}
	v1 := http.NewServeMux()

	v1.HandleFunc("GET /v1/me", s.me)
	v1.HandleFunc("POST /v1/sync/pull", s.pull)
	v1.HandleFunc("POST /v1/sync/push", s.push)

	v1.HandleFunc("GET /v1/workspaces", s.listWorkspaces)
	v1.HandleFunc("POST /v1/workspaces", auth.RequireAdmin(s.createWorkspace))
	v1.HandleFunc("GET /v1/folders", s.listFolders)

	v1.HandleFunc("GET /v1/memories", s.listMemories)
	v1.HandleFunc("POST /v1/memories", s.createMemory)
	v1.HandleFunc("PUT /v1/memories/{id}", s.updateMemory)
	v1.HandleFunc("DELETE /v1/memories/{id}", s.deleteMemory)
	v1.HandleFunc("POST /v1/memories/{id}/supersede", s.supersede)
	v1.HandleFunc("GET /v1/memories/{id}/history", s.history)
	v1.HandleFunc("GET /v1/memories/{id}/links", s.links)
	v1.HandleFunc("POST /v1/memories/review", s.markReview)
	v1.HandleFunc("POST /v1/progress/{workspace}/{taskKey}/claim", s.claim)

	v1.HandleFunc("GET /v1/users", auth.RequireAdmin(s.listUsers))
	v1.HandleFunc("POST /v1/users", auth.RequireAdmin(s.createUser))
	v1.HandleFunc("GET /v1/tokens", s.listTokens)
	v1.HandleFunc("POST /v1/tokens", s.createToken)
	v1.HandleFunc("DELETE /v1/tokens/{id}", s.revokeToken)

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, map[string]bool{"ok": true}) })
	root.Handle("/v1/", auth.Middleware(authStore, v1))
	root.Handle("/", web.Handler(webDir))
	return root
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, auth.UserFrom(r.Context()))
}
