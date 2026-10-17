package memory

import (
	"context"
	"encoding/json"
	"time"
)

// Linked é uma memória vista a partir de outra: o tipo do vínculo e, na cadeia de impacto, a
// distância (1 = depende diretamente) e por qual memória o impacto chegou.
type Linked struct {
	Type   string  `json:"type,omitempty"`
	Depth  int     `json:"depth,omitempty"`
	Via    string  `json:"via,omitempty"`
	ID     string  `json:"id"`
	Memory *Memory `json:"memory"` // null se a memória apontada foi apagada
}

// Links = de quem ela depende/com quem se relaciona (saída), quem aponta para ela (entrada) e a
// cadeia de impacto: todas que dependem dela, direta ou indiretamente (só vínculos "depends").
type Links struct {
	Outgoing []Linked `json:"outgoing"`
	Incoming []Linked `json:"incoming"`
	Impact   []Linked `json:"impact"`
}

const maxDepth = 10

func refsJSON(refs []Ref) []byte {
	if refs == nil {
		refs = []Ref{}
	}
	b, _ := json.Marshal(refs)
	return b
}

func (s *Store) byIDs(ctx context.Context, ids []string) (map[string]*Memory, error) {
	items, err := collect(s.pool.Query(ctx, selectSQL+` WHERE m.id = ANY($1::uuid[]) AND NOT m.deleted`, ids))
	if err != nil {
		return nil, err
	}
	out := map[string]*Memory{}
	for i := range items {
		out[items[i].ID] = &items[i]
	}
	return out, nil
}

func (s *Store) Links(ctx context.Context, id string) (*Links, error) {
	m, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &Links{Outgoing: []Linked{}, Incoming: []Linked{}, Impact: []Linked{}}
	ids := make([]string, len(m.Refs))
	for i, r := range m.Refs {
		ids[i] = r.ID
	}
	targets, err := s.byIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, r := range m.Refs {
		out.Outgoing = append(out.Outgoing, Linked{Type: r.Type, ID: r.ID, Memory: targets[r.ID]})
	}

	incoming, err := collect(s.pool.Query(ctx, selectSQL+` WHERE NOT m.deleted AND m.refs @> jsonb_build_array(jsonb_build_object('id', $1::text))
		ORDER BY m.updated_at DESC`, id))
	if err != nil {
		return nil, err
	}
	for i := range incoming {
		for _, r := range incoming[i].Refs {
			if r.ID == id {
				out.Incoming = append(out.Incoming, Linked{Type: r.Type, ID: incoming[i].ID, Memory: &incoming[i]})
			}
		}
	}

	out.Impact, err = s.impact(ctx, id)
	return out, err
}

// impact percorre "quem depende de quem" a partir de `id` (com limite de profundidade contra ciclos).
func (s *Store) impact(ctx context.Context, id string) ([]Linked, error) {
	rows, err := s.pool.Query(ctx, `
		WITH RECURSIVE impact(id, depth, via) AS (
			SELECT m.id, 1, $1::uuid FROM memories m
			WHERE NOT m.deleted AND m.refs @> jsonb_build_array(jsonb_build_object('id', $1::text, 'type', 'depends'))
			UNION
			SELECT m.id, i.depth + 1, i.id FROM impact i
			JOIN memories m ON NOT m.deleted AND m.refs @> jsonb_build_array(jsonb_build_object('id', i.id::text, 'type', 'depends'))
			WHERE i.depth < $2
		)
		SELECT id::text, min(depth), (array_agg(via::text ORDER BY depth))[1] FROM impact
		WHERE id <> $1::uuid GROUP BY id ORDER BY min(depth)`, id, maxDepth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Linked{}
	var ids []string
	for rows.Next() {
		var l Linked
		if err := rows.Scan(&l.ID, &l.Depth, &l.Via); err != nil {
			return nil, err
		}
		out = append(out, l)
		ids = append(ids, l.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	found, err := s.byIDs(ctx, ids)
	for i := range out {
		out[i].Memory = found[out[i].ID]
	}
	return out, err
}

// ReviewMark é a marca "a revisar" que vai em cada memória impactada.
type ReviewMark struct {
	Reason string    `json:"reason"`           // ex.: "Mudou: <texto novo da memória de origem>"
	Source string    `json:"source,omitempty"` // id da memória que mudou
	By     *Author   `json:"by,omitempty"`
	At     time.Time `json:"at"`
}

// MarkReview marca as memórias como "a revisar" (cada uma vira uma escrita normal, com novo rev e
// histórico). Não exige baseRev: só a marca muda.
func (s *Store) MarkReview(ctx context.Context, ids []string, reason, source string, by Author) []Result {
	mark, _ := json.Marshal(ReviewMark{Reason: reason, Source: source, By: &by, At: time.Now().UTC()})
	results := make([]Result, 0, len(ids))
	for _, id := range ids {
		cur, err := s.Get(ctx, id)
		if err != nil {
			results = append(results, fail(id, err))
			continue
		}
		cur.Review = mark
		results = append(results, s.Upsert(ctx, *cur, cur.Rev, by.ID))
	}
	return results
}
