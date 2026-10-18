package memory_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/google/uuid"

	"bo-memory-server/internal/memory"
	"bo-memory-server/internal/testdb"
)

// Piso da busca por texto no conjunto de testdata/search-eval.json: só sobe (não deixe piorar).
var evalFloor = struct{ hit1, hit5, mrr float64 }{1, 1, 1} // medido em 2026-10-18: 0,64/0,92/0,753 → stopwords 0,88/1/0,94 → autoridade 1/1/1

type evalSet struct {
	Memories []struct {
		K, Kind, Text string
		Tags          []string
		SupersededBy  string `json:"supersededBy"`
		Review        bool
		Task, Status  string
	}
	Queries []struct {
		Q      string
		Expect []string
	}
}

// Avaliação da busca: grava o conjunto (o mesmo do app desktop) e mede hit@1, hit@5 e MRR.
// Mudou o ranking? Rode antes e depois: go test ./internal/memory -run TestSearchEval -v
func TestSearchEval(t *testing.T) {
	set := loadEvalSet(t)
	store := memory.NewStore(testdb.Open(t))
	keys := seedEval(t, store, set)
	var hit1, hit5, mrr float64
	for _, q := range set.Queries {
		pos, got := evalPosition(t, store, q.Q, q.Expect, keys)
		if pos == 1 {
			hit1++
		}
		if pos >= 1 && pos <= 5 {
			hit5++
		}
		if pos > 0 {
			mrr += 1 / float64(pos)
		}
		if pos != 1 {
			t.Logf("  · %s → %s (veio: %v)", q.Q, place(pos), head3(got))
		}
	}
	n := float64(len(set.Queries))
	hit1, hit5, mrr = hit1/n, hit5/n, mrr/n
	t.Logf("busca por texto: hit@1 %.3f · hit@5 %.3f · MRR %.3f (%d perguntas)", hit1, hit5, mrr, len(set.Queries))
	if hit1 < evalFloor.hit1 || hit5 < evalFloor.hit5 || mrr < evalFloor.mrr {
		t.Fatalf("a busca piorou: piso hit@1 %.3f, hit@5 %.3f, MRR %.3f", evalFloor.hit1, evalFloor.hit5, evalFloor.mrr)
	}
}

func loadEvalSet(t *testing.T) evalSet {
	t.Helper()
	raw, err := os.ReadFile("testdata/search-eval.json")
	if err != nil {
		t.Fatal(err)
	}
	var set evalSet
	if err := json.Unmarshal(raw, &set); err != nil {
		t.Fatal(err)
	}
	return set
}

// seedEval grava as memórias do conjunto (globais) e devolve id → chave.
func seedEval(t *testing.T, store *memory.Store, set evalSet) map[string]string {
	t.Helper()
	ids, keys := map[string]string{}, map[string]string{}
	for _, m := range set.Memories {
		ids[m.K] = uuid.NewString()
		keys[ids[m.K]] = m.K
	}
	for _, m := range set.Memories {
		mem := memory.Memory{ID: ids[m.K], Kind: m.Kind, Scope: "global", Text: m.Text, Tags: m.Tags}
		if m.Kind == "progress" { // progresso é do projeto (workspace "eval", o das perguntas)
			mem.Scope, mem.Workspace, mem.TaskKey = "project", ptr("eval"), ptr(m.Task)
		}
		if m.Status != "" {
			mem.Status = ptr(m.Status)
		}
		if m.SupersededBy != "" {
			s := ids[m.SupersededBy]
			mem.SupersededBy = &s
		}
		if m.Review {
			mem.Review = json.RawMessage(`{"reason":"avaliação"}`)
		}
		if err := mem.Normalize(); err != nil {
			t.Fatalf("%s: %v", m.K, err)
		}
		if r := store.Upsert(context.Background(), mem, 0, ""); r.Status != "ok" {
			t.Fatalf("%s: %+v", m.K, r)
		}
	}
	return keys
}

// evalPosition: posição (1…) da primeira memória esperada no resultado; 0 = não veio.
func evalPosition(t *testing.T, store *memory.Store, q string, expect []string, keys map[string]string) (int, []string) {
	t.Helper()
	found, err := store.Search(context.Background(), memory.Query{Workspace: "eval", Q: q, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(found.Items))
	for i, m := range found.Items {
		got[i] = keys[m.ID]
	}
	return slices.IndexFunc(got, func(k string) bool { return slices.Contains(expect, k) }) + 1, got
}

func place(pos int) string {
	if pos == 0 {
		return "fora"
	}
	return fmt.Sprintf("#%d", pos)
}

func head3(xs []string) []string {
	if len(xs) > 3 {
		return xs[:3]
	}
	return xs
}
