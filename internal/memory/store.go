package memory

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bo-memory-server/internal/db"
)

type Store struct {
	pool     *pgxpool.Pool
	Semantic Semantic // busca por significado (opcional)
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var ErrNotFound = errors.New("memória não encontrada")

// selectSQL: colunas de uma memória com o autor (alias m = memories, u = users).
const selectSQL = `SELECT m.id, m.rev, m.kind, m.scope, m.workspace, m.folder, m.text, m.tags, m.task_key,
	m.superseded_by, m.progress, m.refs, m.review, m.status, m.created_at, m.updated_at, m.deleted, u.id, u.name
	FROM memories m LEFT JOIN users u ON u.id = m.author_id`

func scan(row pgx.Row) (*Memory, error) {
	var m Memory
	var authorID, authorName *string
	var id, superseded *string
	err := row.Scan(&id, &m.Rev, &m.Kind, &m.Scope, &m.Workspace, &m.Folder, &m.Text, &m.Tags, &m.TaskKey,
		&superseded, &m.Progress, &m.Refs, &m.Review, &m.Status, &m.CreatedAt, &m.UpdatedAt, &m.Deleted, &authorID, &authorName)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.ID, m.SupersededBy = *id, superseded
	if authorID != nil {
		m.Author = &Author{ID: *authorID, Name: *authorName}
	}
	if m.Tags == nil {
		m.Tags = []string{}
	}
	if m.Refs == nil {
		m.Refs = []Ref{}
	}
	return &m, nil
}

func collect(rows pgx.Rows, err error) ([]Memory, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Memory{}
	for rows.Next() {
		m, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

func get(ctx context.Context, q db.Querier, id string, forUpdate bool) (*Memory, error) {
	sql := selectSQL + ` WHERE m.id = $1`
	if forUpdate {
		sql += ` FOR UPDATE OF m`
	}
	return scan(q.QueryRow(ctx, sql, id))
}

// Get devolve uma memória (inclusive apagada) pelo id.
func (s *Store) Get(ctx context.Context, id string) (*Memory, error) {
	return get(ctx, s.pool, id, false)
}

// History devolve as versões anteriores da memória, da mais recente para a mais antiga.
func (s *Store) History(ctx context.Context, id string) ([]map[string]any, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT h.rev, h.data, h.changed_at, u.name FROM memory_history h LEFT JOIN users u ON u.id = h.changed_by
		WHERE h.memory_id = $1 ORDER BY h.rev DESC`, id)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToMap)
}
