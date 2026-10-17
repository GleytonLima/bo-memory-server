package api

import (
	"net/http"
	"strconv"

	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/memory"
)

func (s *Server) pull(w http.ResponseWriter, r *http.Request) {
	var req memory.PullRequest
	if !decode(w, r, &req) {
		return
	}
	res, err := s.memory.Pull(r.Context(), req)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) push(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Changes []memory.Change `json:"changes"`
	}
	if !decode(w, r, &req) {
		return
	}
	user := auth.UserFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"results": s.memory.Push(r.Context(), req.Changes, user.ID)})
}

func (s *Server) listMemories(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, err := s.memory.List(r.Context(), memory.Query{
		Workspace: q.Get("workspace"), Scope: q.Get("scope"), Kind: q.Get("kind"), Folder: q.Get("folder"),
		Q: q.Get("q"), IncludeSuperseded: q.Get("superseded") == "true", NeedsReview: q.Get("review") == "true", Limit: limit,
	})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createMemory(w http.ResponseWriter, r *http.Request) {
	var m memory.Memory
	if !decode(w, r, &m) {
		return
	}
	res := s.memory.Upsert(r.Context(), m, 0, auth.UserFrom(r.Context()).ID)
	if res.Status == "ok" {
		created, err := s.memory.Get(r.Context(), res.ID)
		if err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, created)
		return
	}
	writeResult(w, res)
}

func (s *Server) updateMemory(w http.ResponseWriter, r *http.Request) {
	var body struct {
		memory.Memory
		BaseRev int64 `json:"baseRev"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Memory.ID = r.PathValue("id")
	writeResult(w, s.memory.Upsert(r.Context(), body.Memory, body.BaseRev, auth.UserFrom(r.Context()).ID))
}

func (s *Server) deleteMemory(w http.ResponseWriter, r *http.Request) {
	baseRev, _ := strconv.ParseInt(r.URL.Query().Get("baseRev"), 10, 64)
	res := s.memory.Delete(r.Context(), r.PathValue("id"), baseRev, auth.UserFrom(r.Context()).ID)
	if res.Status == "error" && res.Error == memory.ErrNotFound.Error() {
		fail(w, memory.ErrNotFound)
		return
	}
	writeResult(w, res)
}

func (s *Server) supersede(w http.ResponseWriter, r *http.Request) {
	var next memory.Memory
	if !decode(w, r, &next) {
		return
	}
	res, err := s.memory.Supersede(r.Context(), r.PathValue("id"), next, auth.UserFrom(r.Context()).ID)
	if err != nil {
		fail(w, err)
		return
	}
	writeResult(w, res)
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	items, err := s.memory.History(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	m, err := s.memory.Claim(r.Context(), r.PathValue("workspace"), r.PathValue("taskKey"), memory.Author{ID: user.ID, Name: user.Name})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) links(w http.ResponseWriter, r *http.Request) {
	links, err := s.memory.Links(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, links)
}

// markReview marca memórias impactadas como "a revisar": { ids, reason, source }.
func (s *Server) markReview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs    []string `json:"ids"`
		Reason string   `json:"reason"`
		Source string   `json:"source"`
	}
	if !decode(w, r, &body) {
		return
	}
	user := auth.UserFrom(r.Context())
	results := s.memory.MarkReview(r.Context(), body.IDs, body.Reason, body.Source, memory.Author{ID: user.ID, Name: user.Name})
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
