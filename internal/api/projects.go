package api

import (
	"errors"
	"net/http"

	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/project"
)

// Projeto (do workspace) e layout (seu) para abrir o projeto em outra máquina — docs/API.md →
// "Projeto e layout". Workspace restrito: só membros, como nas memórias.

func failProject(w http.ResponseWriter, err error) {
	switch {
	case project.IsValidation(err):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, project.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		fail(w, err)
	}
}

// writeDoc: ok 200 com o documento gravado; conflito 409 com o do servidor.
func writeDoc[T any](w http.ResponseWriter, res project.Result[T], err error) {
	switch {
	case err != nil:
		failProject(w, err)
	case res.Status == "conflict":
		writeJSON(w, http.StatusConflict, map[string]any{"server": res.Server})
	default:
		writeJSON(w, http.StatusOK, res.Doc)
	}
}

func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !s.allowAll(w, r, &key) {
		return
	}
	p, err := s.projects.Project(r.Context(), key)
	if err != nil {
		failProject(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) putProject(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !s.allowAll(w, r, &key) {
		return
	}
	var body struct {
		project.Project
		BaseVersion int64 `json:"baseVersion"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Workspace = key
	res, err := s.projects.PutProject(r.Context(), body.Project, body.BaseVersion, auth.UserFrom(r.Context()).ID)
	writeDoc(w, res, err)
}

func (s *Server) getLayout(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !s.allowAll(w, r, &key) {
		return
	}
	l, err := s.projects.Layout(r.Context(), auth.UserFrom(r.Context()).ID, key)
	if err != nil {
		failProject(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) putLayout(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if !s.allowAll(w, r, &key) {
		return
	}
	var body struct {
		project.Layout
		BaseVersion int64 `json:"baseVersion"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Workspace = key
	res, err := s.projects.PutLayout(r.Context(), body.Layout, body.BaseVersion, auth.UserFrom(r.Context()).ID)
	writeDoc(w, res, err)
}
