package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Identity é quem o provedor SSO autenticou (claims do ID token).
type Identity struct {
	Issuer     string
	Subject    string // "sub": estável no provedor
	Email      string
	Name       string
	EmployeeID string
}

var (
	ErrDisabled = errors.New("usuário desativado")
	ErrNoEmail  = errors.New("o provedor não informou o usuário (sub) ou o e-mail")
)

// UpsertSSOUser encontra o usuário pelo vínculo (emissor + sub) ou, no 1º login, pelo e-mail (um
// usuário criado antes pelo admin) — ou cria um novo. Atualiza nome, e-mail e employeeid a cada login.
func (s *Store) UpsertSSOUser(ctx context.Context, id Identity) (User, error) {
	email := strings.ToLower(strings.TrimSpace(id.Email))
	if id.Subject == "" || email == "" {
		return User{}, ErrNoEmail
	}
	name := strings.TrimSpace(id.Name)
	if name == "" {
		name = strings.Split(email, "@")[0]
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // sem efeito após Commit

	var userID string
	var disabled bool
	err = tx.QueryRow(ctx, `SELECT id, disabled FROM users WHERE sso_issuer = $1 AND sso_subject = $2`, id.Issuer, id.Subject).Scan(&userID, &disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		// conta criada pelo CLI com o mesmo e-mail vira a do SSO (é assim que o 1º admin entra). Seguro só com
		// e-mail confiável: o fluxo recusa email_verified=false e o emissor deve ser o do tenant (docs/SSO.md)
		err = tx.QueryRow(ctx, `SELECT id, disabled FROM users WHERE email = $1 AND sso_subject IS NULL`, email).Scan(&userID, &disabled)
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		err = tx.QueryRow(ctx, `INSERT INTO users (name, email) VALUES ($1, $2) RETURNING id`, name, email).Scan(&userID)
	case err == nil && disabled:
		return User{}, ErrDisabled
	}
	if err != nil {
		return User{}, err
	}
	var u User
	err = tx.QueryRow(ctx, `
		UPDATE users SET name = $2, email = $3, employee_id = NULLIF($4, ''), sso_issuer = $5, sso_subject = $6, last_login_at = now()
		WHERE id = $1 RETURNING id, name, email, is_admin, coalesce(employee_id, '')`,
		userID, name, email, id.EmployeeID, id.Issuer, id.Subject).Scan(&u.ID, &u.Name, &u.Email, &u.IsAdmin, &u.EmployeeID)
	if err != nil {
		return User{}, err
	}
	u.SSO = true
	return u, tx.Commit(ctx)
}

// CreateSession emite a sessão de um login SSO (token com validade); o valor só existe aqui.
func (s *Store) CreateSession(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	token, hash := NewSessionToken()
	_, err := s.pool.Exec(ctx, `INSERT INTO tokens (user_id, name, hash, kind, expires_at) VALUES ($1, 'sessão SSO', $2, 'session', now() + make_interval(secs => $3))`,
		userID, hash, ttl.Seconds())
	return token, err
}

// RevokeByValue revoga o token apresentado (sair).
func (s *Store) RevokeByValue(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `UPDATE tokens SET revoked_at = now() WHERE hash = $1 AND revoked_at IS NULL`, Hash(token))
	return err
}
