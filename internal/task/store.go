package task

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const taskSQL = `SELECT t.workspace, t.id, t.key, t.rev, t.deleted, t.doc, t.updated_at, u.id, u.name, o.id, o.name
	FROM tasks t LEFT JOIN users u ON u.id = t.updated_by LEFT JOIN users o ON o.id = t.owner_id`

func scan(row pgx.Row) (*Task, error) {
	var t Task
	var uid, uname, oid, oname *string
	if err := row.Scan(&t.Workspace, &t.ID, &t.Key, &t.Rev, &t.Deleted, &t.Doc, &t.UpdatedAt, &uid, &uname, &oid, &oname); err != nil {
		return nil, err
	}
	if uid != nil {
		t.UpdatedBy = &Author{ID: *uid, Name: *uname}
	}
	if oid != nil {
		t.Owner = &Author{ID: *oid, Name: *oname}
	}
	return &t, nil
}

// Pull: tarefas com rev > since dos workspaces pedidos (só os que `can` libera), por rev.
func (s *Store) Pull(ctx context.Context, since int64, workspaces []string, can func(string) bool) (PullResponse, error) {
	allowed := []string{}
	for _, ws := range workspaces {
		if can(ws) {
			allowed = append(allowed, ws)
		}
	}
	out := PullResponse{Items: []Task{}, Rev: since}
	rows, err := s.pool.Query(ctx, taskSQL+` WHERE t.workspace = ANY($1) AND t.rev > $2 ORDER BY t.rev LIMIT $3`, allowed, since, maxPage+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return out, err
		}
		if len(out.Items) == maxPage {
			out.More = true
			break
		}
		out.Items = append(out.Items, *t)
		out.Rev = t.Rev
	}
	return out, rows.Err()
}

// Push aplica as mudanças na ordem; cada uma é independente (uma recusada não desfaz as outras).
func (s *Store) Push(ctx context.Context, changes []Change, userID string, can func(string) bool) ([]Result, error) {
	out := make([]Result, 0, len(changes))
	for _, c := range changes {
		if msg := c.validate(); msg != "" {
			out = append(out, errorResult(c.ID, msg))
			continue
		}
		if !can(c.Workspace) {
			out = append(out, errorResult(c.ID, "Você não é membro do projeto \""+c.Workspace+"\"."))
			continue
		}
		r, err := s.apply(ctx, c, userID)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *Store) apply(ctx context.Context, c Change, userID string) (Result, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// uma escrita por workspace de cada vez: a chave livre é escolhida sem corrida
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('tasks:' || $1))`, c.Workspace); err != nil {
		return Result{}, err
	}
	cur, err := scan(tx.QueryRow(ctx, taskSQL+` WHERE t.workspace = $1 AND t.id = $2`, c.Workspace, c.ID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Result{}, err
	}
	if cur != nil && cur.Rev != c.BaseRev {
		same := false
		if c.Op == "upsert" && !cur.Deleted && cur.Key == c.Key {
			_ = tx.QueryRow(ctx, `SELECT doc = $1::jsonb FROM tasks WHERE workspace = $2 AND id = $3`, []byte(c.Doc), c.Workspace, c.ID).Scan(&same)
		}
		if same || (c.Op == "delete" && cur.Deleted) {
			return Result{ID: c.ID, Status: "ok", Rev: cur.Rev, Key: cur.Key}, nil
		}
		return Result{ID: c.ID, Status: "conflict", Server: cur}, nil
	}
	if c.Op == "delete" {
		if cur == nil {
			return Result{ID: c.ID, Status: "ok"}, nil // nunca chegou ao servidor: nada a apagar
		}
		var rev int64
		err := tx.QueryRow(ctx, `UPDATE tasks SET deleted = true, rev = nextval('task_rev_seq'), updated_by = $3, updated_at = now()
			WHERE workspace = $1 AND id = $2 RETURNING rev`, c.Workspace, c.ID, userID).Scan(&rev)
		if err != nil {
			return Result{}, err
		}
		return Result{ID: c.ID, Status: "ok", Rev: rev, Key: cur.Key}, tx.Commit(ctx)
	}
	key, err := freeKey(ctx, tx, c)
	if err != nil {
		return Result{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO workspaces (key, name) VALUES ($1, $1) ON CONFLICT DO NOTHING`, c.Workspace); err != nil {
		return Result{}, err
	}
	var rev int64
	// quem cria é o dono; o push nunca muda o dono (só o claim)
	err = tx.QueryRow(ctx, `INSERT INTO tasks (workspace, id, key, rev, doc, updated_by, owner_id) VALUES ($1, $2, $3, nextval('task_rev_seq'), $4, $5, $5)
		ON CONFLICT (workspace, id) DO UPDATE SET key = $3, rev = EXCLUDED.rev, doc = $4, deleted = false, updated_by = $5, updated_at = now()
		RETURNING rev`, c.Workspace, c.ID, key, []byte(c.Doc), userID).Scan(&rev)
	if err != nil {
		return Result{}, err
	}
	return Result{ID: c.ID, Status: "ok", Rev: rev, Key: key}, tx.Commit(ctx)
}

// freeKey: a chave pedida, ou a próxima livre do prefixo se outra tarefa viva já a usa.
func freeKey(ctx context.Context, tx pgx.Tx, c Change) (string, error) {
	var taken bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM tasks WHERE workspace = $1 AND key = $2 AND id <> $3 AND NOT deleted)`,
		c.Workspace, c.Key, c.ID).Scan(&taken)
	if err != nil || !taken {
		return c.Key, err
	}
	rows, err := tx.Query(ctx, `SELECT key FROM tasks WHERE workspace = $1 AND NOT deleted`, c.Workspace)
	if err != nil {
		return "", err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", err
	}
	prefix, max := prefixOf(c.Key), 0
	for _, k := range keys {
		if n := seqOf(k, prefix); n > max {
			max = n
		}
	}
	return nextKey(c.Key, max), nil
}

// Claim ("retomar"): você passa a ser o dono; o histórico da tarefa registra a troca.
func (s *Store) Claim(ctx context.Context, workspace, id string, user Author) (*Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	entry, _ := json.Marshal(map[string]any{"at": time.Now().UnixMilli(), "by": user.Name, "type": "claim", "text": "Retomada por " + user.Name})
	tag, err := tx.Exec(ctx, `UPDATE tasks SET owner_id = $3, updated_by = $3, updated_at = now(), rev = nextval('task_rev_seq'),
		doc = jsonb_set(doc, '{history}', coalesce(doc->'history', '[]'::jsonb) || jsonb_build_array($4::jsonb))
		WHERE workspace = $1 AND id = $2 AND NOT deleted`, strings.ToLower(workspace), id, user.ID, entry)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	t, err := scan(tx.QueryRow(ctx, taskSQL+` WHERE t.workspace = $1 AND t.id = $2`, strings.ToLower(workspace), id))
	if err != nil {
		return nil, err
	}
	return t, tx.Commit(ctx)
}
