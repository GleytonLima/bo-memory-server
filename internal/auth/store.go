package auth

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrInvalidToken = errors.New("token inválido ou revogado")

type User struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Email      string `json:"email"`
	IsAdmin    bool   `json:"isAdmin"`
	EmployeeID string `json:"employeeId,omitempty"`
	SSO        bool   `json:"sso"` // vinculado ao provedor de identidade
}

type Token struct {
	ID         string     `json:"id"`
	UserID     string     `json:"userId"`
	UserName   string     `json:"userName"`
	Name       string     `json:"name"`
	Kind       string     `json:"kind"` // personal | session
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
}

type Store struct {
	pool *pgxpool.Pool
	// PersonalTokens: tokens pessoais valem (padrão). Com SSO adotado, pode-se exigir só sessões SSO.
	PersonalTokens bool
	// PersonalTTL: validade padrão dos tokens pessoais emitidos pela API (BO_TOKEN_TTL_DAYS; 90 dias).
	PersonalTTL time.Duration
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, PersonalTokens: true, PersonalTTL: 90 * 24 * time.Hour}
}

const userCols = `u.id, u.name, u.email, u.is_admin, coalesce(u.employee_id, ''), u.sso_subject IS NOT NULL`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Name, &u.Email, &u.IsAdmin, &u.EmployeeID, &u.SSO)
	return u, err
}

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
	return scanUser(s.pool.QueryRow(ctx, `SELECT `+userCols+` FROM users u WHERE u.email = $1`, strings.ToLower(email)))
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+userCols+` FROM users u WHERE NOT u.disabled ORDER BY u.name`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (User, error) { return scanUser(r) })
}

// CreateToken emite um token sem validade (CLI do operador: o 1º admin). Pela API, use CreateTokenFor.
func (s *Store) CreateToken(ctx context.Context, userID, name string) (token string, id string, err error) {
	token, id, _, err = s.CreateTokenFor(ctx, userID, name, 0)
	return token, id, err
}

// CreateTokenFor emite um token que vence em `ttl` (0 = não vence); o valor só existe neste retorno.
func (s *Store) CreateTokenFor(ctx context.Context, userID, name string, ttl time.Duration) (token, id string, expiresAt *time.Time, err error) {
	token, hash := NewToken()
	if strings.TrimSpace(name) == "" {
		name = "token"
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO tokens (user_id, name, hash, expires_at)
		VALUES ($1, $2, $3, CASE WHEN $4::bigint > 0 THEN now() + make_interval(secs => $4::bigint) END)
		RETURNING id, expires_at`, userID, name, hash, int64(ttl.Seconds())).Scan(&id, &expiresAt)
	return token, id, expiresAt, err
}

// UserByToken valida o token (não revogado, não expirado, do tipo aceito, usuário ativo) e registra o uso.
func (s *Store) UserByToken(ctx context.Context, token string) (User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, `
		UPDATE tokens t SET last_used_at = now()
		FROM users u
		WHERE t.hash = $1 AND t.revoked_at IS NULL AND (t.expires_at IS NULL OR t.expires_at > now())
			AND ($2 OR t.kind = 'session') AND u.id = t.user_id AND NOT u.disabled
		RETURNING `+userCols, Hash(token), s.PersonalTokens))
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrInvalidToken
	}
	return u, err
}

// ListTokens: de um usuário, ou de todos quando userID == "".
func (s *Store) ListTokens(ctx context.Context, userID string) ([]Token, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.user_id, u.name, t.name, t.kind, t.created_at, t.expires_at, t.last_used_at, t.revoked_at
		FROM tokens t JOIN users u ON u.id = t.user_id
		WHERE $1 = '' OR t.user_id::text = $1
		ORDER BY t.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Token, error) {
		var t Token
		return t, r.Scan(&t.ID, &t.UserID, &t.UserName, &t.Name, &t.Kind, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt, &t.RevokedAt)
	})
}

// RevokeToken revoga; um usuário comum só revoga os próprios tokens.
func (s *Store) RevokeToken(ctx context.Context, id string, by User) error {
	if _, err := uuid.Parse(id); err != nil {
		return errors.New("token não encontrado") // id malformado: não chega ao banco (sem erro cru do pg)
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE tokens SET revoked_at = now()
		WHERE id = $1 AND revoked_at IS NULL AND ($2 OR user_id = $3)`, id, by.IsAdmin, by.ID)
	if err == nil && tag.RowsAffected() == 0 {
		return errors.New("token não encontrado")
	}
	return err
}
