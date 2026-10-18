package memory

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

type Query struct {
	Workspace         string // vazio = só globais
	Scope, Kind       string
	Folder            string // remote git
	Q                 string // texto livre (sem acentos, por prefixo)
	IncludeSuperseded bool
	NeedsReview       bool // só as marcadas "a revisar"
	Limit             int
}

var termRe = regexp.MustCompile(`[\p{L}\p{N}_-]+`)

// tsQuery converte texto livre em consulta de prefixo: "postgres trans" → "postgres:* | trans:*".
var hasWord = regexp.MustCompile(`[\p{L}\p{N}]`)

// tsQuery: termos com ao menos uma letra ou número (só "-" ou "_" quebraria o to_tsquery), sem
// stopwords (stopwords.go), como prefixo.
func tsQuery(q string) string {
	words := []string{}
	for _, t := range termRe.FindAllString(q, 20) {
		if t = strings.Trim(t, "-_"); hasWord.MatchString(t) {
			words = append(words, t)
		}
	}
	terms := contentTerms(words)
	for i, t := range terms {
		terms[i] = t + ":*"
	}
	return strings.Join(terms, " | ")
}

// where: condições SQL (alias m) com os argumentos posicionais ($1..$n).
type where struct {
	conds []string
	args  []any
}

func (w *where) arg(v any) string {
	w.args = append(w.args, v)
	return fmt.Sprintf("$%d", len(w.args))
}

func (w *where) add(cond string) { w.conds = append(w.conds, cond) }

func (w *where) sql() string { return strings.Join(w.conds, " AND ") }

// filters: o que a consulta pede, sem o texto livre — globais + do workspace (e de pastas, se
// pedidas), escopo, tipo, substituídas, "a revisar" e ideias fechadas.
func filters(q Query) *where {
	w := &where{}
	w.add("NOT m.deleted")
	visible := "m.scope = 'global'"
	if q.Workspace != "" {
		visible += " OR m.workspace = " + w.arg(q.Workspace) // projeto, progresso e pastas registradas a partir dele
	}
	if q.Folder != "" {
		visible += " OR m.folder = " + w.arg(NormalizeRemote(q.Folder))
	}
	w.add("(" + visible + ")")
	if q.Scope != "" {
		w.add("m.scope = " + w.arg(q.Scope))
	}
	if q.Kind != "" {
		w.add("m.kind = " + w.arg(q.Kind))
	}
	if !q.IncludeSuperseded {
		w.add("m.superseded_by IS NULL")
	}
	if q.NeedsReview {
		w.add("m.review IS NOT NULL")
	}
	if q.Kind != "idea" && q.Kind != "progress" { // ideias fechadas e progresso de tarefa concluída: só no filtro do próprio tipo
		w.add("coalesce(m.status, 'open') = 'open'")
	}
	return w
}

func limitOf(q Query) int {
	if q.Limit <= 0 || q.Limit > 500 {
		return 100
	}
	return q.Limit
}

// List lista/busca memórias vivas por texto (full-text, sem acentos, por prefixo), com a autoridade
// (authority.go) aplicada sobre a relevância; sem texto, as mais recentes.
func (s *Store) List(ctx context.Context, q Query) ([]Memory, error) {
	limit := limitOf(q)
	if tsQuery(q.Q) == "" {
		w := filters(q)
		sql := fmt.Sprintf("%s WHERE %s ORDER BY m.updated_at DESC LIMIT %d", selectSQL, w.sql(), limit)
		return collect(s.pool.Query(ctx, sql, w.args...))
	}
	items, rank, err := s.byText(ctx, q, authorityWindow(limit))
	if err != nil {
		return nil, err
	}
	return rerank(items, func(m *Memory) float64 { return rank[m.ID] }, limit), nil
}

// byText: as memórias que casam com o texto, da mais para a menos relevante (ts_rank), e a relevância.
func (s *Store) byText(ctx context.Context, q Query, limit int) ([]Memory, map[string]float64, error) {
	w := filters(q)
	p := w.arg(tsQuery(q.Q))
	tsq := fmt.Sprintf("to_tsquery('simple', f_unaccent(%s))", p)
	w.add("m.search @@ " + tsq)
	sql := fmt.Sprintf("SELECT %s, ts_rank(m.search, %s) AS rank %s WHERE %s ORDER BY rank DESC, m.updated_at DESC LIMIT %d",
		memoryCols, tsq, memoryFrom, w.sql(), limit)
	rows, err := s.pool.Query(ctx, sql, w.args...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	items, rank := []Memory{}, map[string]float64{}
	for rows.Next() {
		var r float64
		m, err := scan(rows, &r)
		if err != nil {
			return nil, nil, err
		}
		items, rank[m.ID] = append(items, *m), r
	}
	return items, rank, rows.Err()
}
