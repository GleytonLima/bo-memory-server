package api

import (
	"fmt"
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
	a, err := s.access(r)
	if err != nil {
		fail(w, err)
		return
	}
	req.Workspaces = visible(a, req.Workspaces) // projetos de que não é membro ficam de fora
	res, err := s.memory.Pull(r.Context(), req)
	res.Items = allowed(a, res.Items) // memórias de pasta carregam o workspace de origem: só os permitidos
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// maxBatch: itens por pedido (push, marcar a revisar); o desktop envia em lotes de 200.
const maxBatch = 500

func (s *Server) push(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Changes []memory.Change `json:"changes"`
	}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Changes) > maxBatch {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("No máximo %d mudanças por envio.", maxBatch))
		return
	}
	user := auth.UserFrom(r.Context())
	a, err := s.access(r)
	if err != nil {
		fail(w, err)
		return
	}
	// uma a uma, na ordem: mudança em projeto de que não é membro vira "error"
	results := make([]memory.Result, 0, len(req.Changes))
	for _, c := range req.Changes {
		id, target := c.ID, (*string)(nil)
		if c.Op == "delete" {
			c.Memory = nil // o delete age sobre c.ID: um "memory" junto não pode servir de álibi no acesso (IDOR)
		} else if c.Memory != nil {
			id, target = c.Memory.ID, c.Memory.Workspace
		}
		existing, err := s.workspaceOf(r, id)
		if err != nil {
			fail(w, err)
			return
		}
		if msg := denied(a, existing, target); msg != "" {
			results = append(results, memory.Result{ID: id, Status: "error", Error: msg})
			continue
		}
		results = append(results, s.memory.Push(s.guarded(r), []memory.Change{c}, user.ID)...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) listMemories(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if ws := q.Get("workspace"); ws != "" && !s.allowAll(w, r, &ws) {
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, err := s.memory.List(r.Context(), memory.Query{
		Workspace: q.Get("workspace"), Scope: q.Get("scope"), Kind: q.Get("kind"), Folder: q.Get("folder"),
		Q: q.Get("q"), IncludeSuperseded: q.Get("superseded") == "true", NeedsReview: q.Get("review") == "true", Limit: limit,
	})
	if err != nil {
		fail(w, err)
		return
	}
	a, err := s.access(r)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": allowed(a, items)})
}

func (s *Server) createMemory(w http.ResponseWriter, r *http.Request) {
	var m memory.Memory
	if !decode(w, r, &m) || !s.allowMemory(w, r, m.ID, m.Workspace) {
		return
	}
	res := s.memory.Upsert(s.guarded(r), m, 0, auth.UserFrom(r.Context()).ID)
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
	if !s.allowMemory(w, r, body.Memory.ID, body.Memory.Workspace) {
		return
	}
	writeResult(w, s.memory.Upsert(s.guarded(r), body.Memory, body.BaseRev, auth.UserFrom(r.Context()).ID))
}

func (s *Server) deleteMemory(w http.ResponseWriter, r *http.Request) {
	baseRev, _ := strconv.ParseInt(r.URL.Query().Get("baseRev"), 10, 64)
	if !s.allowMemory(w, r, r.PathValue("id"), nil) {
		return
	}
	res := s.memory.Delete(s.guarded(r), r.PathValue("id"), baseRev, auth.UserFrom(r.Context()).ID)
	if res.Status == "error" && res.Error == memory.ErrNotFound.Error() {
		fail(w, memory.ErrNotFound)
		return
	}
	writeResult(w, res)
}

func (s *Server) supersede(w http.ResponseWriter, r *http.Request) {
	var next memory.Memory
	if !decode(w, r, &next) || !s.allowMemory(w, r, r.PathValue("id"), next.Workspace) {
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
	if !s.allowMemory(w, r, r.PathValue("id"), nil) {
		return
	}
	items, err := s.memory.History(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	a, err := s.access(r)
	if err != nil {
		fail(w, err)
		return
	}
	// versões de quando a memória estava num projeto de que você não é membro ficam de fora
	visibleItems := make([]map[string]any, 0, len(items))
	for _, it := range items {
		data, _ := it["data"].(map[string]any)
		ws, _ := data["workspace"].(string)
		if ws == "" || a.Can(&ws) {
			visibleItems = append(visibleItems, it)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": visibleItems})
}

func (s *Server) claim(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFrom(r.Context())
	if ws := r.PathValue("workspace"); !s.allowAll(w, r, &ws) {
		return
	}
	m, err := s.memory.Claim(r.Context(), r.PathValue("workspace"), r.PathValue("taskKey"), memory.Author{ID: user.ID, Name: user.Name})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) links(w http.ResponseWriter, r *http.Request) {
	if !s.allowMemory(w, r, r.PathValue("id"), nil) {
		return
	}
	links, err := s.memory.Links(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	a, err := s.access(r)
	if err != nil {
		fail(w, err)
		return
	}
	// vínculos com memórias de projetos de que não é membro somem (nem o id: ele já revela que existe)
	hide := func(list []memory.Linked) []memory.Linked {
		out := make([]memory.Linked, 0, len(list))
		for _, l := range list {
			if l.Memory == nil || a.Can(l.Memory.Workspace) {
				out = append(out, l)
			}
		}
		return out
	}
	links.Outgoing, links.Incoming, links.Impact = hide(links.Outgoing), hide(links.Incoming), hide(links.Impact)
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
	if len(body.IDs) > maxBatch {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("No máximo %d memórias por pedido.", maxBatch))
		return
	}
	user := auth.UserFrom(r.Context())
	a, err := s.access(r)
	if err != nil {
		fail(w, err)
		return
	}
	results := make([]memory.Result, 0, len(body.IDs))
	for _, id := range body.IDs {
		ws, err := s.workspaceOf(r, id)
		if err != nil {
			fail(w, err)
			return
		}
		if msg := denied(a, ws); msg != "" {
			results = append(results, memory.Result{ID: id, Status: "error", Error: msg})
			continue
		}
		results = append(results, s.memory.MarkReview(r.Context(), []string{id}, body.Reason, body.Source, memory.Author{ID: user.ID, Name: user.Name})...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
