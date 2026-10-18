package memory

import (
	"context"
	"strings"
)

type WorkspaceInfo struct {
	Key        string `json:"key"`
	Name       string `json:"name"`
	Memories   int    `json:"memories"`
	Restricted bool   `json:"restricted"` // tem lista de membros
}

func (s *Store) Workspaces(ctx context.Context) ([]WorkspaceInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT w.key, w.name, count(m.id) FILTER (WHERE NOT m.deleted AND m.superseded_by IS NULL),
			EXISTS (SELECT 1 FROM workspace_members wm WHERE wm.workspace = w.key)
		FROM workspaces w LEFT JOIN memories m ON m.workspace = w.key
		GROUP BY w.key, w.name ORDER BY w.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkspaceInfo{}
	for rows.Next() {
		var w WorkspaceInfo
		if err := rows.Scan(&w.Key, &w.Name, &w.Memories, &w.Restricted); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func (s *Store) CreateWorkspace(ctx context.Context, key, name string) error {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return invalid("Informe a chave do workspace.")
	}
	if strings.TrimSpace(name) == "" {
		name = key
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO workspaces (key, name) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET name = EXCLUDED.name`, key, name)
	return err
}

type FolderInfo struct {
	Folder   string `json:"folder"` // remote normalizado
	Memories int    `json:"memories"`
}

// Folders lista as pastas (remotes) que têm memórias vivas — para a web navegar por repositório.
// `can`: só conta memórias de workspaces visíveis (pasta só de projeto restrito nem aparece).
func (s *Store) Folders(ctx context.Context, can func(*string) bool) ([]FolderInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT folder, workspace, count(*) FROM memories
		WHERE scope = 'folder' AND NOT deleted AND superseded_by IS NULL
		GROUP BY folder, workspace ORDER BY folder`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FolderInfo{}
	for rows.Next() {
		var folder string
		var ws *string
		var n int
		if err := rows.Scan(&folder, &ws, &n); err != nil {
			return nil, err
		}
		if !can(ws) {
			continue
		}
		if last := len(out) - 1; last >= 0 && out[last].Folder == folder {
			out[last].Memories += n
		} else {
			out = append(out, FolderInfo{Folder: folder, Memories: n})
		}
	}
	return out, rows.Err()
}
