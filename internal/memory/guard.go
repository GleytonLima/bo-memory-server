package memory

import (
	"context"
	"errors"
)

// ErrForbidden: a memória está (agora) num workspace que quem pede não pode alterar.
var ErrForbidden = errors.New("sem acesso ao projeto desta memória")

type guardKey struct{}

// WithGuard leva a regra de acesso (ex.: Access.Can) para dentro das escritas: Upsert e Delete a
// conferem de novo depois de travar a linha (FOR UPDATE), fechando a janela entre a checagem do
// handler e a transação (a memória pode ter mudado de workspace no meio).
func WithGuard(ctx context.Context, can func(*string) bool) context.Context {
	return context.WithValue(ctx, guardKey{}, can)
}

func allowed(ctx context.Context, cur *Memory) bool {
	can, _ := ctx.Value(guardKey{}).(func(*string) bool)
	return can == nil || cur == nil || can(cur.Workspace)
}
