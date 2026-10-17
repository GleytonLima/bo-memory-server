package memory

import (
	"context"
	"encoding/json"
	"errors"
)

// Supersede cria uma decisão nova e marca a antiga como substituída por ela.
func (s *Store) Supersede(ctx context.Context, oldID string, next Memory, userID string) (Result, error) {
	old, err := s.Get(ctx, oldID)
	if err != nil {
		return Result{}, err
	}
	if old.Kind != "decision" || old.Deleted {
		return Result{}, invalid("Só decisões vigentes podem ser substituídas.")
	}
	next.ID, next.Kind = "", "decision"
	if next.Scope == "" {
		next.Scope, next.Workspace, next.Folder = old.Scope, old.Workspace, old.Folder
	}
	created := s.Upsert(ctx, next, 0, userID)
	if created.Status != "ok" {
		return created, nil
	}
	old.SupersededBy = &created.ID
	if r := s.Upsert(ctx, *old, old.Rev, userID); r.Status != "ok" {
		return r, nil
	}
	return created, nil
}

// Claim faz o usuário assumir a tarefa: o progresso dela passa a ter este dono (fica no histórico).
func (s *Store) Claim(ctx context.Context, workspace, taskKey string, user Author) (*Memory, error) {
	cur, err := scan(s.pool.QueryRow(ctx, selectSQL+` WHERE m.workspace = $1 AND m.task_key = $2 AND m.kind = 'progress' AND NOT m.deleted`, workspace, taskKey))
	if err != nil {
		return nil, err
	}
	progress := map[string]any{}
	if len(cur.Progress) > 0 {
		if err := json.Unmarshal(cur.Progress, &progress); err != nil {
			return nil, err
		}
	}
	progress["owner"] = user
	cur.Progress, _ = json.Marshal(progress)
	r := s.Upsert(ctx, *cur, cur.Rev, user.ID)
	if r.Status != "ok" {
		return nil, errors.New("a tarefa mudou enquanto era retomada; tente de novo")
	}
	return s.Get(ctx, cur.ID)
}
