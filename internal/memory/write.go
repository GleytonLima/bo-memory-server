package memory

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Result de uma escrita (o mesmo formato vai no push do sync — docs/API.md).
type Result struct {
	ID     string  `json:"id"`
	Status string  `json:"status"` // ok | conflict | error
	Rev    int64   `json:"rev,omitempty"`
	Server *Memory `json:"server,omitempty"`
	Error  string  `json:"error,omitempty"`
}

func fail(id string, err error) Result { return Result{ID: id, Status: "error", Error: err.Error()} }

// Upsert grava a memória se baseRev for o rev atual (ou se ela for nova); caso contrário, conflito.
// Conteúdo idêntico ao do servidor nunca é conflito.
func (s *Store) Upsert(ctx context.Context, m Memory, baseRev int64, userID string) Result {
	if err := m.Normalize(); err != nil {
		return fail(m.ID, err)
	}
	return s.inTx(ctx, m.ID, func(tx pgx.Tx) Result {
		cur, err := get(ctx, tx, m.ID, true)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return fail(m.ID, err)
		}
		if !allowed(ctx, cur) {
			return fail(m.ID, ErrForbidden)
		}
		if cur != nil && cur.Rev != baseRev {
			if SameContent(cur, &m) {
				return Result{ID: m.ID, Status: "ok", Rev: cur.Rev}
			}
			return Result{ID: m.ID, Status: "conflict", Server: cur}
		}
		if m.Workspace != nil {
			if _, err := tx.Exec(ctx, `INSERT INTO workspaces (key, name) VALUES ($1, $1) ON CONFLICT DO NOTHING`, *m.Workspace); err != nil {
				return fail(m.ID, err)
			}
		}
		if cur != nil {
			if err := saveHistory(ctx, tx, cur, userID); err != nil {
				return fail(m.ID, err)
			}
		}
		rev, err := write(ctx, tx, &m, userID)
		if err != nil {
			return s.writeError(ctx, &m, err)
		}
		return Result{ID: m.ID, Status: "ok", Rev: rev}
	})
}

// Delete marca a memória como apagada (tombstone com novo rev), se baseRev for o atual.
func (s *Store) Delete(ctx context.Context, id string, baseRev int64, userID string) Result {
	return s.inTx(ctx, id, func(tx pgx.Tx) Result {
		cur, err := get(ctx, tx, id, true)
		if err != nil {
			return fail(id, err)
		}
		if !allowed(ctx, cur) {
			return fail(id, ErrForbidden)
		}
		if cur.Deleted {
			return Result{ID: id, Status: "ok", Rev: cur.Rev}
		}
		if cur.Rev != baseRev {
			return Result{ID: id, Status: "conflict", Server: cur}
		}
		if err := saveHistory(ctx, tx, cur, userID); err != nil {
			return fail(id, err)
		}
		var rev int64
		err = tx.QueryRow(ctx, `UPDATE memories SET deleted = true, rev = nextval('memory_rev_seq'), updated_at = now()
			WHERE id = $1 RETURNING rev`, id).Scan(&rev)
		if err != nil {
			return fail(id, err)
		}
		return Result{ID: id, Status: "ok", Rev: rev}
	})
}

func (s *Store) inTx(ctx context.Context, id string, fn func(pgx.Tx) Result) Result {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fail(id, err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // sem efeito após Commit
	res := fn(tx)
	if res.Status == "ok" {
		if err := tx.Commit(ctx); err != nil {
			return fail(id, err)
		}
		if s.Semantic != nil {
			s.Semantic.Changed()
		}
	}
	return res
}

func write(ctx context.Context, tx pgx.Tx, m *Memory, userID string) (int64, error) {
	var rev int64
	err := tx.QueryRow(ctx, `
		INSERT INTO memories (id, rev, kind, scope, workspace, folder, text, tags, task_key, superseded_by, progress, refs, review, status, author_id)
		VALUES ($1, nextval('memory_rev_seq'), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (id) DO UPDATE SET rev = EXCLUDED.rev, kind = EXCLUDED.kind, scope = EXCLUDED.scope,
			workspace = EXCLUDED.workspace, folder = EXCLUDED.folder, text = EXCLUDED.text, tags = EXCLUDED.tags,
			task_key = EXCLUDED.task_key, superseded_by = EXCLUDED.superseded_by, progress = EXCLUDED.progress,
			refs = EXCLUDED.refs, review = EXCLUDED.review, status = EXCLUDED.status, updated_at = now(), deleted = false
		RETURNING rev`,
		m.ID, m.Kind, m.Scope, m.Workspace, m.Folder, m.Text, m.Tags, m.TaskKey, m.SupersededBy, nullJSON(m.Progress), refsJSON(m.Refs), nullJSON(m.Review), m.Status, nullID(userID),
	).Scan(&rev)
	return rev, err
}

// writeError traduz violações conhecidas: progresso duplicado para a mesma tarefa vira conflito.
func (s *Store) writeError(ctx context.Context, m *Memory, err error) Result {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.ConstraintName == "memories_progress_task" {
		existing, gerr := scan(s.pool.QueryRow(ctx, selectSQL+` WHERE m.workspace = $1 AND m.task_key = $2 AND m.kind = 'progress' AND NOT m.deleted`, m.Workspace, m.TaskKey))
		if gerr == nil {
			return Result{ID: m.ID, Status: "conflict", Server: existing}
		}
	}
	if errors.As(err, &pg) && pg.Code == "23514" { // CHECK violado
		return fail(m.ID, fmt.Errorf("dados inválidos (%s)", pg.ConstraintName))
	}
	log.Printf("gravar memória %s: %v", m.ID, err)
	return fail(m.ID, errors.New("não foi possível gravar a memória"))
}

func saveHistory(ctx context.Context, tx pgx.Tx, cur *Memory, userID string) error {
	_, err := tx.Exec(ctx, `INSERT INTO memory_history (memory_id, rev, data, changed_by) VALUES ($1, $2, $3, $4)`,
		cur.ID, cur.Rev, cur, nullID(userID))
	return err
}

func nullID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func nullJSON(raw []byte) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}
