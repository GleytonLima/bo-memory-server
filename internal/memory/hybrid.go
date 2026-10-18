package memory

import (
	"context"
	"log"
	"sort"
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
// (RRF); o que só o significado trouxe vem com Match = "semantic". Sem provedor (ou se ele falhar),
// só texto.
func (s *Store) Search(ctx context.Context, q Query) (Found, error) {
	if q.Q == "" || s.Semantic == nil {
		items, err := s.List(ctx, q)
		return Found{Items: items, Mode: modeOf(q, Text)}, err
	}
	limit := limitOf(q)
	byText, err := s.List(ctx, Query{Workspace: q.Workspace, Scope: q.Scope, Kind: q.Kind, Folder: q.Folder, Q: q.Q,
		IncludeSuperseded: q.IncludeSuperseded, NeedsReview: q.NeedsReview, Limit: candidates})
	if err != nil {
		return Found{}, err
	}
	w := filters(q)
	similar, err := s.Semantic.Rank(ctx, q.Q, w.sql(), w.args, candidates)
	if err != nil {
		log.Printf("busca semântica: %v", err)
		return Found{Items: head(byText, limit), Mode: Text, Warning: "Busca por significado indisponível agora; usando só o texto."}, nil
	}
	textIDs := make([]string, len(byText))
	found := map[string]bool{}
	for i, m := range byText {
		textIDs[i], found[m.ID] = m.ID, true
	}
	ids := head(fuse(textIDs, similar), limit)
	items, err := s.inOrder(ctx, ids)
	if err != nil {
		return Found{}, err
	}
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

// fuse: reciprocal rank fusion — cada ranking soma 1/(k + posição); empate fica com a ordem do texto.
func fuse(rankings ...[]string) []string {
	score := map[string]float64{}
	order := []string{}
	for _, ids := range rankings {
		for i, id := range ids {
			if _, seen := score[id]; !seen {
				order = append(order, id)
			}
			score[id] += 1 / float64(rrfK+i+1)
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return score[order[a]] > score[order[b]] })
	return order
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
