package project

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Result de uma gravação: "ok" (com o documento gravado) ou "conflict" (com o do servidor).
type Result[T any] struct {
	Status string `json:"status"`
	Doc    *T     `json:"doc,omitempty"`
	Server *T     `json:"server,omitempty"`
}

const projectSQL = `SELECT p.workspace, p.name, p.folders, p.columns, p.version, p.updated_at, u.id, u.name
	FROM workspace_projects p LEFT JOIN users u ON u.id = p.updated_by WHERE p.workspace = $1`

func scanProject(row pgx.Row) (*Project, error) {
	var p Project
	var folders, columns []byte
	var uid, uname *string
	if err := row.Scan(&p.Workspace, &p.Name, &folders, &columns, &p.Version, &p.UpdatedAt, &uid, &uname); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if uid != nil {
		p.UpdatedBy = &Author{ID: *uid, Name: *uname}
	}
	if err := json.Unmarshal(folders, &p.Folders); err != nil {
		return nil, err
	}
	err := json.Unmarshal(columns, &p.Columns)
	p.Folders, p.Columns = nonNil(p.Folders), nonNil(p.Columns)
	return &p, err
}

func (s *Store) Project(ctx context.Context, workspace string) (*Project, error) {
	return scanProject(s.pool.QueryRow(ctx, projectSQL, key(workspace)))
}

// HasProject: quais workspaces já têm definição (para a lista da máquina nova).
func (s *Store) HasProject(ctx context.Context) (map[string]bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT workspace FROM workspace_projects`)
	if err != nil {
		return nil, err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	out := map[string]bool{}
	for _, k := range keys {
		out[k] = true
	}
	return out, err
}

// PutProject grava se baseVersion for a versão atual (0 = novo); conteúdo igual nunca é conflito.
func (s *Store) PutProject(ctx context.Context, p Project, baseVersion int64, userID string) (Result[Project], error) {
	p.Workspace = key(p.Workspace)
	if err := p.validate(); err != nil {
		return Result[Project]{}, err
	}
	return inTx(ctx, s.pool, func(tx pgx.Tx) (Result[Project], error) {
		cur, err := scanProject(tx.QueryRow(ctx, projectSQL+" FOR UPDATE OF p", p.Workspace))
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Result[Project]{}, err
		}
		if cur != nil && cur.Version != baseVersion {
			if cur.Name == p.Name && reflect.DeepEqual(cur.Folders, p.Folders) && reflect.DeepEqual(cur.Columns, p.Columns) {
				return Result[Project]{Status: "ok", Doc: cur}, nil
			}
			return Result[Project]{Status: "conflict", Server: cur}, nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO workspaces (key, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`, p.Workspace, p.Name); err != nil {
			return Result[Project]{}, err
		}
		folders, _ := json.Marshal(nonNil(p.Folders))
		columns, _ := json.Marshal(nonNil(p.Columns))
		_, err = tx.Exec(ctx, `INSERT INTO workspace_projects (workspace, name, folders, columns, version, updated_by)
			VALUES ($1, $2, $3, $4, 1, $5)
			ON CONFLICT (workspace) DO UPDATE SET name = $2, folders = $3, columns = $4,
				version = workspace_projects.version + 1, updated_by = $5, updated_at = now()`,
			p.Workspace, p.Name, folders, columns, userID)
		if err != nil {
			return Result[Project]{}, err
		}
		saved, err := scanProject(tx.QueryRow(ctx, projectSQL, p.Workspace))
		return Result[Project]{Status: "ok", Doc: saved}, err
	})
}

const layoutSQL = `SELECT workspace, groups, version, updated_at FROM user_layouts WHERE user_id = $1 AND workspace = $2`

func scanLayout(row pgx.Row) (*Layout, error) {
	var l Layout
	var groups []byte
	if err := row.Scan(&l.Workspace, &groups, &l.Version, &l.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	err := json.Unmarshal(groups, &l.Groups)
	l.Groups = nonNil(l.Groups)
	for i := range l.Groups {
		l.Groups[i].Terminals = nonNil(l.Groups[i].Terminals)
	}
	return &l, err
}

func (s *Store) Layout(ctx context.Context, userID, workspace string) (*Layout, error) {
	return scanLayout(s.pool.QueryRow(ctx, layoutSQL, userID, key(workspace)))
}

// PutLayout grava o layout do usuário no workspace (mesma regra de versão do projeto).
func (s *Store) PutLayout(ctx context.Context, l Layout, baseVersion int64, userID string) (Result[Layout], error) {
	l.Workspace = key(l.Workspace)
	if err := l.validate(); err != nil {
		return Result[Layout]{}, err
	}
	return inTx(ctx, s.pool, func(tx pgx.Tx) (Result[Layout], error) {
		cur, err := scanLayout(tx.QueryRow(ctx, layoutSQL+" FOR UPDATE", userID, l.Workspace))
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Result[Layout]{}, err
		}
		if cur != nil && cur.Version != baseVersion {
			if reflect.DeepEqual(cur.Groups, l.Groups) {
				return Result[Layout]{Status: "ok", Doc: cur}, nil
			}
			return Result[Layout]{Status: "conflict", Server: cur}, nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO workspaces (key, name) VALUES ($1, $1) ON CONFLICT DO NOTHING`, l.Workspace); err != nil {
			return Result[Layout]{}, err
		}
		groups, _ := json.Marshal(nonNil(l.Groups))
		_, err = tx.Exec(ctx, `INSERT INTO user_layouts (user_id, workspace, groups, version) VALUES ($1, $2, $3, 1)
			ON CONFLICT (user_id, workspace) DO UPDATE SET groups = $3, version = user_layouts.version + 1, updated_at = now()`,
			userID, l.Workspace, groups)
		if err != nil {
			return Result[Layout]{}, err
		}
		saved, err := scanLayout(tx.QueryRow(ctx, layoutSQL, userID, l.Workspace))
		return Result[Layout]{Status: "ok", Doc: saved}, err
	})
}

func key(ws string) string { return strings.ToLower(strings.TrimSpace(ws)) }

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func inTx[T any](ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) (T, error)) (T, error) {
	var zero T
	tx, err := pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err := fn(tx)
	if err != nil {
		return zero, err
	}
	return out, tx.Commit(ctx)
}
