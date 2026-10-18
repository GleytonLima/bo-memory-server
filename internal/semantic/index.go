package semantic

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	batch    = 32
	interval = 30 * time.Second
)

// Index mantém memory_embeddings em dia (em segundo plano) e ranqueia por similaridade.
type Index struct {
	pool   *pgxpool.Pool
	emb    Embedder
	model  string // Config.VectorKey: modelo + prefixo dos documentos
	minSim float64
	qPre   string // prefixo da consulta
	dPre   string // prefixo dos documentos
	nudge  chan struct{}

	mu      sync.Mutex
	queries map[string][]float32 // cache das consultas recentes
	lastErr string
}

func New(pool *pgxpool.Pool, emb Embedder, cfg Config) *Index {
	return &Index{pool: pool, emb: emb, model: cfg.VectorKey(), minSim: cfg.MinSimilarity, qPre: cfg.QueryPrefix, dPre: cfg.DocPrefix, nudge: make(chan struct{}, 1), queries: map[string][]float32{}}
}

// Start liga a busca semântica se configurada: cria a extensão e a tabela e indexa em segundo plano.
// Sem configuração devolve nil; sem pgvector no banco, erro (o servidor segue só com texto).
func Start(ctx context.Context, pool *pgxpool.Pool, cfg Config) (*Index, error) {
	if !cfg.Enabled() {
		return nil, nil
	}
	emb, err := NewEmbedder(cfg)
	if err != nil {
		return nil, err
	}
	ix := New(pool, emb, cfg)
	if err := ix.Ensure(ctx); err != nil {
		return nil, err
	}
	go ix.Run(ctx)
	return ix, nil
}

// Ensure cria a extensão vector e a tabela dos vetores (fora das migrações: pgvector é opcional).
// Vetor sem dimensão fixa: trocar de modelo reindexa sem mudar o esquema; a busca é exata. Sem FK
// para memories (dado derivado: não trava o `migrate reset`); órfãos saem em cada ciclo.
func (ix *Index) Ensure(ctx context.Context) error {
	_, err := ix.pool.Exec(ctx, `
		CREATE EXTENSION IF NOT EXISTS vector;
		CREATE TABLE IF NOT EXISTS memory_embeddings (
			memory_id  uuid PRIMARY KEY,
			model      text NOT NULL,
			hash       text NOT NULL,
			embedding  vector NOT NULL,
			updated_at timestamptz NOT NULL DEFAULT now()
		)`)
	if err != nil {
		return fmt.Errorf("pgvector indisponível no banco: %w", err)
	}
	return nil
}

// Changed avisa que memórias mudaram: a indexação roda logo, sem esperar o próximo ciclo.
func (ix *Index) Changed() {
	select {
	case ix.nudge <- struct{}{}:
	default:
	}
}

func (ix *Index) Run(ctx context.Context) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		ix.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-ix.nudge:
		}
	}
}

func (ix *Index) drain(ctx context.Context) {
	for {
		n, err := ix.IndexPending(ctx)
		ix.report(err)
		if err != nil || n < batch {
			return
		}
	}
}

// report loga o erro só quando ele muda (provedor fora do ar não enche o log a cada ciclo).
func (ix *Index) report(err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if msg != ix.lastErr {
		if msg != "" {
			log.Printf("indexação semântica: %s", msg)
		} else if ix.lastErr != "" {
			log.Printf("indexação semântica: voltou a funcionar")
		}
		ix.lastErr = msg
	}
}

// docSQL: o texto que vira vetor (texto + tags + repositório), igual ao do app; no máximo 8000 caracteres.
const docSQL = `left(concat_ws(E'\n', m.text, 'tags: ' || nullif(array_to_string(m.tags, ', '), ''), 'repo: ' || m.folder), 8000)`

// IndexPending gera os vetores de até um lote de memórias vivas sem vetor (ou com texto ou
// modelo diferente do atual), depois de tirar os órfãos. Devolve quantas indexou.
func (ix *Index) IndexPending(ctx context.Context) (int, error) {
	if _, err := ix.pool.Exec(ctx, `DELETE FROM memory_embeddings e WHERE NOT EXISTS (SELECT 1 FROM memories m WHERE m.id = e.memory_id)`); err != nil {
		return 0, err
	}
	rows, err := ix.pool.Query(ctx, `
		SELECT m.id, d.doc, md5(d.doc) FROM memories m
		CROSS JOIN LATERAL (SELECT `+docSQL+` AS doc) d
		LEFT JOIN memory_embeddings e ON e.memory_id = m.id
		WHERE NOT m.deleted AND (e.memory_id IS NULL OR e.model <> $1 OR e.hash <> md5(d.doc))
		ORDER BY m.updated_at DESC LIMIT $2`, ix.model, batch)
	if err != nil {
		return 0, err
	}
	var ids, docs, hashes []string
	for rows.Next() {
		var id, doc, hash string
		if err := rows.Scan(&id, &doc, &hash); err != nil {
			rows.Close()
			return 0, err
		}
		ids, docs, hashes = append(ids, id), append(docs, ix.dPre+doc), append(hashes, hash)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return 0, err
	}
	vectors, err := ix.emb.Embed(ctx, docs)
	if err != nil {
		return 0, err
	}
	for i, id := range ids {
		if _, err := ix.pool.Exec(ctx, `
			INSERT INTO memory_embeddings (memory_id, model, hash, embedding) VALUES ($1, $2, $3, $4::vector)
			ON CONFLICT (memory_id) DO UPDATE SET model = EXCLUDED.model, hash = EXCLUDED.hash,
				embedding = EXCLUDED.embedding, updated_at = now()`, id, ix.model, hashes[i], literal(vectors[i])); err != nil {
			return i, err
		}
	}
	return len(ids), nil
}
