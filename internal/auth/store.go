package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidToken = errors.New("token inválido ou revogado")

type User struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Email   string `json:"email"`
	IsAdmin bool   `json:"isAdmin"`
}

type Token struct {
	ID         string     `json:"id"`
	UserID     string     `json:"userId"`
	UserName   string     `json:"userName"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) CreateUser(ctx context.Context, name, email string, admin bool) (User, error) {
	name, email = strings.TrimSpace(name), strings.ToLower(strings.TrimSpace(email))
	if name == "" || email == "" {
		return User{}, errors.New("nome e e-mail são obrigatórios")
	}
	u := User{Name: name, Email: email, IsAdmin: admin}
	err := s.pool.QueryRow(ctx, `INSERT INTO users (name, email, is_admin) VALUES ($1, $2, $3) RETURNING id`, name, email, admin).Scan(&u.ID)
	return u, err
}

func (s *Store) UserByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `SELECT id, name, email, is_admin FROM users WHERE email = $1`, strings.ToLower(email)).
		Scan(&u.ID, &u.Name, &u.Email, &u.IsAdmin)
	return u, err
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, email, is_admin FROM users WHERE NOT disabled ORDER BY name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (User, error) {
		var u User
		return u, r.Scan(&u.ID, &u.Name, &u.Email, &u.IsAdmin)
	})
}

// CreateToken emite um token para o usuário; o valor só existe neste retorno.
func (s *Store) CreateToken(ctx context.Context, userID, name string) (token string, id string, err error) {
	token, hash := NewToken()
	if strings.TrimSpace(name) == "" {
		name = "token"
	}
	err = s.pool.QueryRow(ctx, `INSERT INTO tokens (user_id, name, hash) VALUES ($1, $2, $3) RETURNING id`, userID, name, hash).Scan(&id)
	return token, id, err
}

// UserByToken valida o token (não revogado, usuário ativo) e registra o uso.
func (s *Store) UserByToken(ctx context.Context, token string) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		UPDATE tokens t SET last_used_at = now()
		FROM users u
		WHERE t.hash = $1 AND t.revoked_at IS NULL AND u.id = t.user_id AND NOT u.disabled
		RETURNING u.id, u.name, u.email, u.is_admin`, Hash(token)).Scan(&u.ID, &u.Name, &u.Email, &u.IsAdmin)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrInvalidToken
	}
	return u, err
}

// ListTokens: de um usuário, ou de todos quando userID == "".
func (s *Store) ListTokens(ctx context.Context, userID string) ([]Token, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.user_id, u.name, t.name, t.created_at, t.last_used_at, t.revoked_at
		FROM tokens t JOIN users u ON u.id = t.user_id
		WHERE $1 = '' OR t.user_id::text = $1
		ORDER BY t.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Token, error) {
		var t Token
		return t, r.Scan(&t.ID, &t.UserID, &t.UserName, &t.Name, &t.CreatedAt, &t.LastUsedAt, &t.RevokedAt)
	})
}

// RevokeToken revoga; um usuário comum só revoga os próprios tokens.
func (s *Store) RevokeToken(ctx context.Context, id string, by User) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE tokens SET revoked_at = now()
		WHERE id = $1 AND revoked_at IS NULL AND ($2 OR user_id = $3)`, id, by.IsAdmin, by.ID)
	if err == nil && tag.RowsAffected() == 0 {
		return errors.New("token não encontrado")
	}
	return err
}
