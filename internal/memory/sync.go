package memory

import (
	"context"
	"errors"
)

const pullPage = 500

type PullRequest struct {
	Since      int64    `json:"since"`
	Workspaces []string `json:"workspaces"`
	Folders    []string `json:"folders"` // remotes git (normalizados aqui)
}

type PullResponse struct {
	Items []Memory `json:"items"`
	Rev   int64    `json:"rev"`
	More  bool     `json:"more"`
}

// Pull devolve o que mudou depois de `since` e vale para o cliente: globais, project/progress dos
// workspaces e folder das pastas — incluindo tombstones. Paginado por rev.
func (s *Store) Pull(ctx context.Context, req PullRequest) (PullResponse, error) {
	folders := make([]string, 0, len(req.Folders))
	for _, f := range req.Folders {
		if n := NormalizeRemote(f); n != "" {
			folders = append(folders, n)
		}
	}
	workspaces := req.Workspaces
	if workspaces == nil {
		workspaces = []string{}
	}
	items, err := collect(s.pool.Query(ctx, selectSQL+`
		WHERE m.rev > $1 AND (
			m.scope = 'global'
			OR (m.workspace = ANY($2) AND m.scope = 'project')
			OR (m.scope = 'folder' AND m.folder = ANY($3)))
		ORDER BY m.rev LIMIT $4`, req.Since, workspaces, folders, pullPage+1))
	if err != nil {
		return PullResponse{}, err
	}
	res := PullResponse{Items: items, Rev: req.Since}
	if len(items) > pullPage {
		res.Items, res.More = items[:pullPage], true
	}
	if n := len(res.Items); n > 0 {
		res.Rev = res.Items[n-1].Rev
	}
	return res, nil
}

type Change struct {
	Op      string  `json:"op"` // upsert | delete
	BaseRev int64   `json:"baseRev"`
	Memory  *Memory `json:"memory,omitempty"`
	ID      string  `json:"id,omitempty"` // em delete
}

// Push aplica as mudanças do cliente, uma a uma, devolvendo um resultado por mudança (mesma ordem).
func (s *Store) Push(ctx context.Context, changes []Change, userID string) []Result {
	results := make([]Result, 0, len(changes))
	for _, c := range changes {
		switch {
		case c.Op == "delete" && c.ID != "":
			r := s.Delete(ctx, c.ID, c.BaseRev, userID)
			if r.Status == "error" && r.Error == ErrNotFound.Error() {
				r = Result{ID: c.ID, Status: "ok"} // apagar o que o servidor nunca viu: nada a fazer
			}
			results = append(results, r)
		case c.Op == "upsert" && c.Memory != nil:
			results = append(results, s.Upsert(ctx, *c.Memory, c.BaseRev, userID))
		default:
			results = append(results, fail(c.ID, errors.New(`mudança inválida: use op "upsert" (com memory) ou "delete" (com id)`)))
		}
	}
	return results
}
