package memory

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

type Query struct {
	Workspace         string // vazio = só globais
	Scope, Kind       string
	Folder            string // remote git
	Q                 string // texto livre (sem acentos, por prefixo)
	IncludeSuperseded bool
	NeedsReview       bool // só as marcadas "a revisar"
	Limit             int
}

var termRe = regexp.MustCompile(`[\p{L}\p{N}_-]+`)

// tsQuery converte texto livre em consulta de prefixo: "postgres trans" → "postgres:* | trans:*".
func tsQuery(q string) string {
	terms := termRe.FindAllString(q, 20)
	for i, t := range terms {
		terms[i] = t + ":*"
	}
	return strings.Join(terms, " | ")
}

// List lista/busca memórias vivas para a web: globais + do workspace (e de pastas, se pedidas).
func (s *Store) List(ctx context.Context, q Query) ([]Memory, error) {
	where := []string{"NOT m.deleted"}
	args := []any{}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	visible := "m.scope = 'global'"
	if q.Workspace != "" {
		ws := arg(q.Workspace)
		visible += " OR m.workspace = " + ws // projeto, progresso e pastas registradas a partir dele
	}
	if q.Folder != "" {
		visible += " OR m.folder = " + arg(NormalizeRemote(q.Folder))
	}
	where = append(where, "("+visible+")")
	if q.Scope != "" {
		where = append(where, "m.scope = "+arg(q.Scope))
	}
	if q.Kind != "" {
		where = append(where, "m.kind = "+arg(q.Kind))
	}
	if !q.IncludeSuperseded {
		where = append(where, "m.superseded_by IS NULL")
	}
	if q.NeedsReview {
		where = append(where, "m.review IS NOT NULL")
	}
	if q.Kind != "idea" { // ideias fechadas (viraram tarefa / descartadas) só no filtro de ideias
		where = append(where, "coalesce(m.status, 'open') = 'open'")
	}
	order := "m.updated_at DESC"
	if ts := tsQuery(q.Q); ts != "" {
		p := arg(ts)
		where = append(where, fmt.Sprintf("m.search @@ to_tsquery('simple', f_unaccent(%s))", p))
		order = fmt.Sprintf("ts_rank(m.search, to_tsquery('simple', f_unaccent(%s))) DESC, m.updated_at DESC", p)
	}
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	sql := fmt.Sprintf("%s WHERE %s ORDER BY %s LIMIT %d", selectSQL, strings.Join(where, " AND "), order, limit)
	return collect(s.pool.Query(ctx, sql, args...))
}

type WorkspaceInfo struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
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
func (s *Store) Folders(ctx context.Context) ([]FolderInfo, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT folder, count(*) FROM memories
		WHERE scope = 'folder' AND NOT deleted AND superseded_by IS NULL
		GROUP BY folder ORDER BY folder`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FolderInfo{}
	for rows.Next() {
		var f FolderInfo
		if err := rows.Scan(&f.Folder, &f.Memories); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
