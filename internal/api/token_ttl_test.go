package api_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"bo-memory-server/internal/api"
	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/memory"
	"bo-memory-server/internal/testdb"
)

// Qualquer token emitido pela API vence (padrão 90 dias; 1 a 365 pedido): uma sessão SSO não vira acesso eterno.
func TestPersonalTokensFromAPIExpire(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	users := auth.NewStore(pool)
	u, _ := users.CreateUser(ctx, "Ana", "ana@empresa.com", false)
	tok, _, _ := users.CreateToken(ctx, u.ID, "cli")
	srv := httptest.NewServer(api.Handler(users, memory.NewStore(pool), t.TempDir(), nil))
	defer srv.Close()
	ana := client{t: t, token: tok, base: srv.URL}

	var def struct {
		Token     string
		ExpiresAt *time.Time
	}
	if code := ana.do("POST", "/v1/tokens", map[string]any{"name": "notebook"}, &def); code != 201 || def.ExpiresAt == nil {
		t.Fatalf("token sem validade pedida deveria vencer no padrão: %d %+v", code, def)
	}
	if d := time.Until(*def.ExpiresAt); d < 89*24*time.Hour || d > 91*24*time.Hour {
		t.Fatalf("padrão deveria ser ~90 dias: %v", d)
	}
	var week struct {
		Token     string
		ExpiresAt *time.Time
	}
	ana.do("POST", "/v1/tokens", map[string]any{"name": "curto", "expiresInDays": 7}, &week)
	if week.ExpiresAt == nil || time.Until(*week.ExpiresAt) > 8*24*time.Hour {
		t.Fatalf("validade pedida (7 dias) não foi respeitada: %+v", week)
	}
	for _, bad := range []int{-1, 366} {
		if code := ana.do("POST", "/v1/tokens", map[string]any{"name": "x", "expiresInDays": bad}, nil); code != 400 {
			t.Fatalf("validade %d deveria dar 400: %d", bad, code)
		}
	}
	// vencido não autentica
	if _, err := pool.Exec(ctx, `UPDATE tokens SET expires_at = now() - interval '1 minute' WHERE name = 'curto'`); err != nil {
		t.Fatal(err)
	}
	if code := (client{t: t, token: week.Token, base: srv.URL}).do("GET", "/v1/me", nil, nil); code != 401 {
		t.Fatalf("token vencido deveria dar 401: %d", code)
	}
	if code := (client{t: t, token: def.Token, base: srv.URL}).do("GET", "/v1/me", nil, nil); code != 200 {
		t.Fatalf("token dentro da validade continua valendo: %d", code)
	}
}
