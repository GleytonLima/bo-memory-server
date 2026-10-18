package api_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"bo-memory-server/internal/api"
	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/memory"
	"bo-memory-server/internal/semantic"
	"bo-memory-server/internal/semantic/fakeembed"
	"bo-memory-server/internal/testdb"
)

type searchResponse struct {
	Items   []memory.Memory
	Mode    string
	Warning string
}

// GET /v1/memories?q= com a busca semântica ligada: modo híbrido, marca do que veio pelo
// significado, sem vazar memória de projeto restrito; provedor fora do ar → só texto, com aviso.
func TestSemanticSearchAPI(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	fake := fakeembed.Start(t)
	cfg := semantic.Config{Provider: "openai", URL: fake.URL, Model: "falso", MinSimilarity: 0.3}
	emb, _ := semantic.NewEmbedder(cfg)
	ix := semantic.New(pool, emb, cfg)
	if err := ix.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	users := auth.NewStore(pool)
	store := memory.NewStore(pool)
	store.Semantic = ix
	srv := httptest.NewServer(api.Handler(users, store, t.TempDir(), nil))
	defer srv.Close()
	mk := func(name, email string, admin bool) client {
		u, _ := users.CreateUser(ctx, name, email, admin)
		tok, _, _ := users.CreateToken(ctx, u.ID, "cli")
		return client{t, srv.URL, tok}
	}
	adm, dev := mk("Admin", "admin@x.com", true), mk("Dev", "dev@x.com", false)

	adm.do("PUT", "/v1/workspaces/secreto/members", map[string]any{"members": []string{"admin@x.com"}}, nil)
	adm.do("POST", "/v1/memories", map[string]any{"kind": "decision", "scope": "global", "text": "Usamos PostgreSQL no servidor de memória"}, nil)
	adm.do("POST", "/v1/memories", map[string]any{"kind": "note", "scope": "global", "text": "Login pelo SSO"}, nil)
	adm.do("POST", "/v1/memories", map[string]any{"kind": "note", "scope": "folder", "workspace": "secreto", "folder": "github.com/x/api", "text": "Senha do banco no cofre"}, nil)
	for {
		if n, err := ix.IndexPending(ctx); err != nil || n == 0 {
			break
		}
	}

	var got searchResponse
	dev.do("GET", "/v1/memories?q=dados&folder=github.com/x/api", nil, &got)
	if got.Mode != "hybrid" || len(got.Items) != 1 || got.Items[0].Match != "semantic" || got.Items[0].Kind != "decision" {
		t.Fatalf("\"dados\": a decisão do PostgreSQL, pelo significado; a da pasta restrita não: %+v", got)
	}
	var recent searchResponse
	if dev.do("GET", "/v1/memories", nil, &recent); recent.Mode != "" || len(recent.Items) != 2 {
		t.Fatalf("sem texto: as recentes, sem modo: %+v", recent)
	}

	fake.Fail.Store(true)
	var down searchResponse
	dev.do("GET", "/v1/memories?q=banco", nil, &down) // consulta nova (a anterior está no cache)
	if down.Mode != "text" || down.Warning == "" || len(down.Items) != 0 {
		t.Fatalf("provedor fora do ar: só texto, com aviso: %+v", down)
	}
}
