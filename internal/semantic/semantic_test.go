package semantic_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bo-memory-server/internal/db"
	"bo-memory-server/internal/memory"
	"bo-memory-server/internal/semantic"
	"bo-memory-server/internal/semantic/fakeembed"
	"bo-memory-server/internal/testdb"
)

func setup(t *testing.T, provider string) (*memory.Store, *semantic.Index, *fakeembed.Server, *pgxpool.Pool) {
	t.Helper()
	pool := testdb.Open(t)
	fake := fakeembed.Start(t)
	t.Setenv("BO_EMBED_PROVIDER", provider)
	t.Setenv("BO_EMBED_URL", fake.URL)
	t.Setenv("BO_EMBED_MODEL", "falso")
	t.Setenv("BO_EMBED_KEY", "chave-teste")
	ix := index(t, pool) // sem o laço de fundo: o teste indexa na hora certa
	store := memory.NewStore(pool)
	store.Semantic = ix
	return store, ix, fake, pool
}

func index(t *testing.T, pool *pgxpool.Pool) *semantic.Index {
	t.Helper()
	cfg := semantic.LoadConfig()
	emb, err := semantic.NewEmbedder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ix := semantic.New(pool, emb, cfg)
	if err := ix.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	return ix
}

func put(t *testing.T, s *memory.Store, text string, tags ...string) string {
	t.Helper()
	m := memory.Memory{Kind: "note", Scope: "global", Text: text, Tags: tags}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	if r := s.Upsert(context.Background(), m, 0, ""); r.Status != "ok" {
		t.Fatalf("upsert: %+v", r)
	}
	return m.ID
}

func indexAll(t *testing.T, ix *semantic.Index) int {
	t.Helper()
	total := 0
	for {
		n, err := ix.IndexPending(context.Background())
		if err != nil {
			t.Fatalf("indexar: %v", err)
		}
		if total += n; n == 0 {
			return total
		}
	}
}

// Indexa só o que falta ou mudou; a busca acha pelo significado o que o texto não acha.
func TestIndexAndHybridSearch(t *testing.T) {
	ctx := context.Background()
	store, ix, fake, _ := setup(t, "openai")
	pg := put(t, store, "Usamos PostgreSQL para guardar a memória do time")
	put(t, store, "Login pelo SSO da empresa")
	put(t, store, "Tema escuro como padrão", "visual")
	if n := indexAll(t, ix); n != 3 {
		t.Fatalf("primeira indexação: %d", n)
	}
	if n := indexAll(t, ix); n != 0 {
		t.Fatalf("nada mudou, nada a indexar: %d", n)
	}
	if h, _ := fake.Header.Load().(string); h != "Bearer chave-teste" {
		t.Fatalf("openai manda a chave como Bearer: %q", h)
	}

	text, _ := store.Search(ctx, memory.Query{Q: "banco de dados"})
	if text.Mode != memory.Hybrid || len(text.Items) != 1 || text.Items[0].ID != pg || text.Items[0].Match != "semantic" {
		t.Fatalf("\"banco de dados\" acha a do PostgreSQL só pelo significado: %+v", text)
	}
	both, _ := store.Search(ctx, memory.Query{Q: "postgresql"})
	if len(both.Items) != 1 || both.Items[0].Match != "" {
		t.Fatalf("achada pelo texto (e pelo significado) não leva a marca: %+v", both)
	}
	if none, _ := store.Search(ctx, memory.Query{Q: "cozinha"}); len(none.Items) != 0 {
		t.Fatalf("nada parecido: nada volta (similaridade mínima): %+v", none.Items)
	}

	// editar o texto reindexa só aquela; consulta repetida vem do cache
	m, _ := store.Get(ctx, pg)
	m.Text = "Usamos PostgreSQL com pgvector"
	store.Upsert(ctx, *m, m.Rev, "")
	if n := indexAll(t, ix); n != 1 {
		t.Fatalf("editada: reindexa 1, foram %d", n)
	}
	before := fake.Calls.Load()
	store.Search(ctx, memory.Query{Q: "banco de dados"})
	if fake.Calls.Load() != before {
		t.Fatal("consulta repetida deveria vir do cache")
	}

	// provedor fora do ar: a busca segue só por texto, com aviso
	fake.Fail.Store(true)
	down, err := store.Search(ctx, memory.Query{Q: "login autenticação"})
	if err != nil || down.Mode != memory.Text || down.Warning == "" || len(down.Items) != 1 {
		t.Fatalf("sem provedor: texto + aviso: %+v %v", down, err)
	}
	put(t, store, "nova")
	if _, err := ix.IndexPending(ctx); err == nil || !strings.Contains(err.Error(), "500") {
		t.Fatalf("indexar com o provedor fora do ar deveria falhar: %v", err)
	}
}

// Start liga o laço de fundo: gravou, indexou (sem esperar o ciclo de 30 s).
func TestStartIndexesInBackground(t *testing.T) {
	pool := testdb.Open(t)
	fake := fakeembed.Start(t)
	t.Setenv("BO_EMBED_PROVIDER", "openai")
	t.Setenv("BO_EMBED_URL", fake.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ix, err := semantic.Start(ctx, pool, semantic.LoadConfig())
	if err != nil {
		t.Fatal(err)
	}
	store := memory.NewStore(pool)
	store.Semantic = ix
	put(t, store, "Banco de dados")
	for range 100 {
		var n int
		_ = pool.QueryRow(ctx, "SELECT count(*) FROM memory_embeddings").Scan(&n)
		if n == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("a memória nova deveria ser indexada em segundo plano")
}

// Os vetores são dado derivado: `migrate reset` funciona com a tabela presente, e os órfãos saem.
func TestResetKeepsWorking(t *testing.T) {
	ctx := context.Background()
	store, ix, _, pool := setup(t, "openai")
	put(t, store, "Banco de dados")
	indexAll(t, ix)
	if err := db.Migrate(ctx, pool, "reset"); err != nil {
		t.Fatalf("reset com memory_embeddings: %v", err)
	}
	if err := db.Migrate(ctx, pool, "up"); err != nil {
		t.Fatal(err)
	}
	indexAll(t, ix)
	var n int
	if _ = pool.QueryRow(ctx, "SELECT count(*) FROM memory_embeddings").Scan(&n); n != 0 {
		t.Fatalf("órfãos deveriam sair: %d", n)
	}
}

// Prefixos: a família nomic recebe search_document/search_query; mudar o prefixo reindexa.
func TestPrefixes(t *testing.T) {
	ctx := context.Background()
	store, _, fake, pool := setup(t, "openai")
	t.Setenv("BO_EMBED_MODEL", "text-embedding-nomic-embed-text-v1.5")
	ix := index(t, pool)
	store.Semantic = ix
	put(t, store, "Banco de dados")
	indexAll(t, ix)
	if in, _ := fake.Inputs.Load().([]string); len(in) != 1 || !strings.HasPrefix(in[0], "search_document: Banco") {
		t.Fatalf("documento com o prefixo do nomic: %q", in)
	}
	store.Search(ctx, memory.Query{Q: "postgres"})
	if in, _ := fake.Inputs.Load().([]string); len(in) != 1 || in[0] != "search_query: postgres" {
		t.Fatalf("consulta com o prefixo do nomic: %q", in)
	}
	t.Setenv("BO_EMBED_DOC_PREFIX", "none")
	if n := indexAll(t, index(t, pool)); n != 1 {
		t.Fatalf("sem o prefixo, os vetores são outros: reindexa (%d)", n)
	}
}

func TestConfig(t *testing.T) {
	for _, k := range []string{"BO_EMBED_PROVIDER", "BO_EMBED_URL", "BO_EMBED_MODEL", "BO_EMBED_KEY", "BO_EMBED_API_VERSION", "BO_EMBED_MIN_SIMILARITY", "BO_EMBED_QUERY_PREFIX", "BO_EMBED_DOC_PREFIX"} {
		t.Setenv(k, "")
	}
	if c := semantic.LoadConfig(); c.Enabled() {
		t.Fatal("desligada por padrão")
	}
	if ix, err := semantic.Start(context.Background(), nil, semantic.LoadConfig()); ix != nil || err != nil {
		t.Fatal("desligada: Start não faz nada")
	}
	t.Setenv("BO_EMBED_PROVIDER", "Ollama")
	t.Setenv("BO_EMBED_MIN_SIMILARITY", "0.5")
	if c := semantic.LoadConfig(); c.URL != "http://127.0.0.1:11434" || c.Model != "nomic-embed-text" || c.MinSimilarity != 0.5 || c.Check() != nil {
		t.Fatalf("padrões do ollama: %+v", c)
	}
	if c := semantic.LoadConfig(); c.QueryPrefix != "search_query: " || c.DocPrefix != "search_document: " || c.VectorKey() != "nomic-embed-text | search_document: " {
		t.Fatalf("nomic: prefixos padrão: %+v", c)
	}
	t.Setenv("BO_EMBED_MODEL", "intfloat/multilingual-e5-small")
	if c := semantic.LoadConfig(); c.QueryPrefix != "query: " || c.DocPrefix != "passage: " {
		t.Fatalf("e5: prefixos padrão: %+v", c)
	}
	t.Setenv("BO_EMBED_MODEL", "bge-m3")
	t.Setenv("BO_EMBED_QUERY_PREFIX", "Q: ")
	if c := semantic.LoadConfig(); c.QueryPrefix != "Q: " || c.DocPrefix != "" || c.VectorKey() != "bge-m3" {
		t.Fatalf("sem padrão; variável manda: %+v", c)
	}
	t.Setenv("BO_EMBED_PROVIDER", "azure")
	if err := semantic.LoadConfig().Check(); err == nil {
		t.Fatal("azure sem URL, implantação e chave deveria ser recusado")
	}
	t.Setenv("BO_EMBED_PROVIDER", "outro")
	if _, err := semantic.NewEmbedder(semantic.LoadConfig()); err == nil {
		t.Fatal("provedor desconhecido deveria ser recusado")
	}
}

// Trocar de modelo reindexa tudo; Ollama e Azure falam os formatos deles.
func TestProvidersAndModelChange(t *testing.T) {
	store, ix, fake, pool := setup(t, "ollama")
	put(t, store, "Testes com Playwright")
	if n := indexAll(t, ix); n != 1 {
		t.Fatalf("ollama: %d", n)
	}
	t.Setenv("BO_EMBED_PROVIDER", "azure")
	t.Setenv("BO_EMBED_MODEL", "implantacao")
	if n := indexAll(t, index(t, pool)); n != 1 {
		t.Fatalf("outro modelo reindexa: %d", n)
	}
	if h, _ := fake.Header.Load().(string); h != "chave-teste" {
		t.Fatalf("azure manda a chave em api-key: %q", h)
	}
}
