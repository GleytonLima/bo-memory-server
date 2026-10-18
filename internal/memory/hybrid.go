package memory

import (
	"context"
	"log"
)

// Semantic ranqueia por significado (pacote semantic); nil no Store = busca só por texto.
type Semantic interface {
	// Rank: ids das memórias parecidas com a consulta entre as que passam em `where` (alias m).
	Rank(ctx context.Context, query, where string, args []any, limit int) ([]string, error)
	// Changed: memórias mudaram (reindexar logo).
	Changed()
}

const (
	candidates = 50 // de cada ranking, antes da fusão
	rrfK       = 60 // reciprocal rank fusion, como no app
	Text       = "text"
	Hybrid     = "hybrid"
)

// Found: resultado da busca. Mode "hybrid" = texto + significado; Warning quando o provedor falhou.
type Found struct {
	Items   []Memory `json:"items"`
	Mode    string   `json:"mode,omitempty"`
	Warning string   `json:"warning,omitempty"`
}

// Search: com texto e busca semântica ligada, funde o ranking por texto com o por similaridade
// (RRF) e aplica a autoridade (authority.go); o que só o significado trouxe vem com Match =
// "semantic". Sem provedor (ou se ele falhar), só texto.
func (s *Store) Search(ctx context.Context, q Query) (Found, error) {
	if q.Q == "" || s.Semantic == nil {
		items, err := s.List(ctx, q)
		return Found{Items: items, Mode: modeOf(q, Text)}, err
	}
	limit := limitOf(q)
	byText, _, err := s.byText(ctx, q, max(candidates, authorityWindow(limit)))
	if err != nil {
		return Found{}, err
	}
	w := filters(q)
	similar, err := s.Semantic.Rank(ctx, q.Q, w.sql(), w.args, candidates)
	if err != nil {
		log.Printf("busca semântica: %v", err)
		items, err := s.List(ctx, q)
		return Found{Items: items, Mode: Text, Warning: "Busca por significado indisponível agora; usando só o texto."}, err
	}
	textIDs := make([]string, len(byText))
	found := map[string]bool{}
	for i, m := range byText {
		textIDs[i], found[m.ID] = m.ID, true
	}
	score := fuse(textIDs, similar)
	ids := make([]string, 0, len(score))
	for id := range score {
		ids = append(ids, id)
	}
	items, err := s.inOrder(ctx, ids)
	if err != nil {
		return Found{}, err
	}
	items = rerank(items, func(m *Memory) float64 { return score[m.ID] }, limit)
	for i := range items {
		if !found[items[i].ID] {
			items[i].Match = "semantic"
		}
	}
	return Found{Items: items, Mode: Hybrid}, nil
}

func modeOf(q Query, mode string) string {
	if q.Q == "" {
		return ""
	}
	return mode
}

func head[T any](xs []T, n int) []T {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

// fuse: reciprocal rank fusion — cada ranking soma 1/(k + posição). → id → escore.
func fuse(rankings ...[]string) map[string]float64 {
	score := map[string]float64{}
	for _, ids := range rankings {
		for i, id := range ids {
			score[id] += 1 / float64(rrfK+i+1)
		}
	}
	return score
}

// inOrder: as memórias (vivas) na ordem dos ids.
func (s *Store) inOrder(ctx context.Context, ids []string) ([]Memory, error) {
	at, err := s.byIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Memory, 0, len(ids))
	for _, id := range ids {
		if m, ok := at[id]; ok {
			out = append(out, *m)
		}
	}
	return out, nil
}
