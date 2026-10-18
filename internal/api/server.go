package api

import (
	"net/http"
	"strings"

	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/memory"
	"bo-memory-server/internal/project"
	"bo-memory-server/internal/sso"
	"bo-memory-server/internal/web"
)

type Server struct {
	auth     *auth.Store
	memory   *memory.Store
	projects *project.Store
}

// Handler monta as rotas: /healthz, /auth/* (SSO) e a web sem autenticação; /v1/* exige token.
// `flow` é nil quando não há SSO configurado.
func Handler(authStore *auth.Store, memStore *memory.Store, webDir string, flow *sso.Flow) http.Handler {
	s := &Server{auth: authStore, memory: memStore, projects: project.NewStore(memStore.Pool())}
	v1 := http.NewServeMux()

	v1.HandleFunc("GET /v1/me", s.me)
	v1.HandleFunc("POST /v1/logout", s.logout)
	v1.HandleFunc("POST /v1/sync/pull", s.pull)
	v1.HandleFunc("POST /v1/sync/push", s.push)

	v1.HandleFunc("GET /v1/workspaces", s.listWorkspaces)
	v1.HandleFunc("POST /v1/workspaces", auth.RequireAdmin(s.createWorkspace))
	v1.HandleFunc("GET /v1/workspaces/{key}/members", auth.RequireAdmin(s.listMembers))
	v1.HandleFunc("PUT /v1/workspaces/{key}/members", auth.RequireAdmin(s.setMembers))
	v1.HandleFunc("GET /v1/workspaces/{key}/project", s.getProject)
	v1.HandleFunc("PUT /v1/workspaces/{key}/project", s.putProject)
	v1.HandleFunc("GET /v1/workspaces/{key}/layout", s.getLayout)
	v1.HandleFunc("PUT /v1/workspaces/{key}/layout", s.putLayout)
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
	root.HandleFunc("GET /auth/config", func(w http.ResponseWriter, _ *http.Request) {
		cfg := map[string]any{"sso": flow != nil, "personalTokens": authStore.PersonalTokens}
		if flow != nil {
			cfg["provider"] = flow.ProviderName()
		}
		writeJSON(w, http.StatusOK, cfg)
	})
	if flow != nil {
		// login: cada GET grava um estado no banco — 30 por minuto por IP bastam para gente de verdade
		perIP := newLimiter(30, 30)
		root.Handle("GET /auth/login", perIP.wrap(byIP, http.HandlerFunc(flow.Login)))
		root.Handle("GET /auth/callback", perIP.wrap(byIP, http.HandlerFunc(flow.Callback)))
	}
	// API: 1200 por minuto por token (rajada de 300) — o sync do desktop faz poucas por minuto
	root.Handle("/v1/", newLimiter(1200, 300).wrap(byToken, auth.Middleware(authStore, v1)))
	root.Handle("/", web.Handler(webDir))
	return secureHeaders(root)
}

// secureHeaders: nada de embutir em iframe (clickjacking), MIME sniffing ou Referer para fora; CSP
// também no cabeçalho (frame-ancestors só vale nele) e HSTS quando chega por HTTPS.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, auth.UserFrom(r.Context()))
}

// logout revoga o token usado nesta chamada (a sessão SSO, tipicamente).
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if err := s.auth.RevokeByValue(r.Context(), strings.TrimSpace(token)); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
