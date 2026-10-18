package semantic

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const cacheSize = 200

// Rank devolve os ids das memórias mais parecidas com a consulta, da mais para a menos parecida,
// entre as que passam no filtro `where` (SQL sobre o alias m = memories, com os args $1..$n) e têm
// similaridade (cosseno) de pelo menos MinSimilarity.
func (ix *Index) Rank(ctx context.Context, query, where string, args []any, limit int) ([]string, error) {
	q, err := ix.queryVector(ctx, query)
	if err != nil {
		return nil, err
	}
	args = append(args, ix.model, literal(q), ix.minSim, limit)
	n := len(args)
	sql := fmt.Sprintf(`SELECT m.id FROM memory_embeddings e JOIN memories m ON m.id = e.memory_id
		WHERE e.model = $%d AND (%s) AND 1 - (e.embedding <=> $%d::vector) >= $%d
		ORDER BY e.embedding <=> $%d::vector LIMIT $%d`, n-3, where, n-2, n-1, n-2, n)
	rows, err := ix.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (ix *Index) queryVector(ctx context.Context, query string) ([]float32, error) {
	key := strings.ToLower(strings.TrimSpace(query))
	ix.mu.Lock()
	v, ok := ix.queries[key]
	ix.mu.Unlock()
	if ok {
		return v, nil
	}
	vs, err := ix.emb.Embed(ctx, []string{key})
	if err != nil {
		return nil, err
	}
	ix.mu.Lock()
	if len(ix.queries) >= cacheSize {
		clear(ix.queries)
	}
	ix.queries[key] = vs[0]
	ix.mu.Unlock()
	return vs[0], nil
}

// literal: vetor no formato de texto do pgvector ("[0.1,0.2]"), convertido com ::vector.
func literal(v []float32) string {
	parts := make([]string, len(v))
	for i, f := range v {
		parts[i] = strconv.FormatFloat(float64(f), 'g', -1, 32)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
