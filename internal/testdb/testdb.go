// Package testdb prepara o banco de testes: recria o esquema pelas migrações a cada teste.
// Requer o PostgreSQL do docker compose (ou TEST_DATABASE_URL); sem ele, os testes são pulados.
package testdb

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"bo-memory-server/internal/db"
)

const defaultURL = "postgres://bo:bo@localhost:55432/bo_memory_test?sslmode=disable"

func Open(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = defaultURL
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, url)
	if err != nil {
		t.Skipf("PostgreSQL de testes indisponível (%v) — rode: docker compose up -d", err)
	}
	t.Cleanup(pool.Close)
	// Esquema limpo a cada teste (o reset do goose exige a tabela de versões já existir).
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatalf("limpar esquema: %v", err)
	}
	if err := db.Migrate(ctx, pool, "up"); err != nil {
		t.Fatalf("up: %v", err)
	}
	return pool
}
