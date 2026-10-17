package memory

import (
	"context"
	"regexp"
	"strings"
)

// Membros de um workspace: e-mails exatos ou domínios inteiros (*@empresa.com). Workspace sem
// membros é aberto a todos os usuários autenticados (como antes do SSO).
var memberRe = regexp.MustCompile(`^(\*|[^@\s*]+)@[^@\s*]+\.[^@\s*]+$`)

// Access diz quais workspaces um usuário pode ver e alterar.
type Access struct {
	admin   bool
	allowed map[string]bool // só workspaces restritos: true = é membro
}

// Can: o workspace não é restrito, ou o usuário é membro (ou admin). Vazio (global/pasta) = sim.
func (a Access) Can(ws *string) bool {
	if ws == nil || *ws == "" || a.admin {
		return true
	}
	member, restricted := a.allowed[*ws]
	return !restricted || member
}

// AccessFor calcula o acesso do e-mail a todos os workspaces restritos de uma vez.
func (s *Store) AccessFor(ctx context.Context, email string, admin bool) (Access, error) {
	a := Access{admin: admin, allowed: map[string]bool{}}
	rows, err := s.pool.Query(ctx, `
		SELECT workspace, bool_or(email_pattern = $1 OR (left(email_pattern, 2) = '*@' AND $1 LIKE '%' || substr(email_pattern, 2)))
		FROM workspace_members GROUP BY workspace`, strings.ToLower(email))
	if err != nil {
		return a, err
	}
	defer rows.Close()
	for rows.Next() {
		var ws string
		var member bool
		if err := rows.Scan(&ws, &member); err != nil {
			return a, err
		}
		a.allowed[ws] = member
	}
	return a, rows.Err()
}

func (s *Store) Members(ctx context.Context, workspace string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT email_pattern FROM workspace_members WHERE workspace = $1 ORDER BY email_pattern`, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetMembers substitui a lista de membros (vazia = workspace aberto).
func (s *Store) SetMembers(ctx context.Context, workspace string, patterns []string, by string) ([]string, error) {
	clean := []string{}
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p == "" {
			continue
		}
		if !memberRe.MatchString(p) {
			return nil, invalid("Membro inválido: %q (use um e-mail ou *@dominio.com).", p)
		}
		clean = append(clean, p)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // sem efeito após Commit
	if _, err := tx.Exec(ctx, `INSERT INTO workspaces (key, name) VALUES ($1, $1) ON CONFLICT DO NOTHING`, workspace); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM workspace_members WHERE workspace = $1`, workspace); err != nil {
		return nil, err
	}
	for _, p := range clean {
		if _, err := tx.Exec(ctx, `INSERT INTO workspace_members (workspace, email_pattern, added_by) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, workspace, p, nullID(by)); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Members(ctx, workspace)
}
