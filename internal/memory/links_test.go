package memory_test

import (
	"encoding/json"
	"strings"
	"testing"

	"bo-memory-server/internal/memory"
)

func TestLinksAndImpact(t *testing.T) {
	s, user := setup(t)
	add := func(text string, refs ...memory.Ref) string {
		r := s.Upsert(ctx, memory.Memory{Kind: "note", Scope: "project", Workspace: ptr("app"), Text: text, Refs: refs}, 0, user)
		if r.Status != "ok" {
			t.Fatalf("%s: %+v", text, r)
		}
		return r.ID
	}
	a := add("A: processamento assíncrono")
	b := add("B: usamos filas", memory.Ref{ID: a, Type: "depends"})
	c := add("C: workers escalam pela fila", memory.Ref{ID: b, Type: "depends"})
	d := add("D: eventos de domínio", memory.Ref{ID: a, Type: "related"})
	e := add("E: tudo síncrono", memory.Ref{ID: a, Type: "contradicts"})

	links, err := s.Links(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	types := map[string]string{}
	for _, l := range links.Incoming {
		types[l.ID] = l.Type
	}
	if len(links.Incoming) != 3 || types[b] != "depends" || types[d] != "related" || types[e] != "contradicts" {
		t.Fatalf("entrada: %+v", links.Incoming)
	}
	if len(links.Impact) != 2 || links.Impact[0].ID != b || links.Impact[0].Depth != 1 || links.Impact[1].ID != c || links.Impact[1].Depth != 2 || links.Impact[1].Via != b {
		t.Fatalf("impacto deveria ser B (1) e C (2, via B): %+v", links.Impact)
	}
	if out, _ := s.Links(ctx, c); len(out.Outgoing) != 1 || out.Outgoing[0].Memory == nil || out.Outgoing[0].Memory.ID != b {
		t.Fatalf("saída de C: %+v", out.Outgoing)
	}

	// ciclo: A passa a depender de C — o impacto termina e não inclui a própria A
	m, _ := s.Get(ctx, a)
	m.Refs = []memory.Ref{{ID: c, Type: "depends"}, {ID: a, Type: "depends"}}
	if r := s.Upsert(ctx, *m, m.Rev, user); r.Status != "ok" {
		t.Fatalf("ciclo: %+v", r)
	}
	if got, _ := s.Get(ctx, a); len(got.Refs) != 1 {
		t.Fatalf("auto-referência deveria sumir: %+v", got.Refs)
	}
	if again, _ := s.Links(ctx, a); len(again.Impact) != 2 {
		t.Fatalf("impacto com ciclo: %+v", again.Impact)
	}

	// validação
	bad := memory.Memory{Kind: "note", Scope: "global", Text: "x", Refs: []memory.Ref{{ID: a, Type: "inspira"}}}
	if r := s.Upsert(ctx, bad, 0, user); r.Status != "error" || !strings.Contains(r.Error, "Tipo de vínculo") {
		t.Fatalf("tipo inválido: %+v", r)
	}

	// marcar a revisar
	res := s.MarkReview(ctx, []string{b, c}, "Mudou: A", a, memory.Author{ID: user, Name: "Dev"})
	if len(res) != 2 || res[0].Status != "ok" || res[1].Status != "ok" {
		t.Fatalf("marcar: %+v", res)
	}
	got, _ := s.Get(ctx, c)
	var mark memory.ReviewMark
	if json.Unmarshal(got.Review, &mark) != nil || mark.Source != a || mark.By == nil || mark.By.Name != "Dev" {
		t.Fatalf("marca: %s", got.Review)
	}
	// revisar = gravar sem a marca
	got.Review = nil
	if r := s.Upsert(ctx, *got, got.Rev, user); r.Status != "ok" {
		t.Fatalf("revisar: %+v", r)
	}
	if again, _ := s.Get(ctx, c); again.Review != nil {
		t.Fatalf("marca deveria sumir: %s", again.Review)
	}
}

func TestRefsTakePartInConflicts(t *testing.T) {
	s, user := setup(t)
	a := s.Upsert(ctx, note("app", "A"), 0, user)
	b := s.Upsert(ctx, note("app", "B"), 0, user)
	m, _ := s.Get(ctx, b.ID)
	m.Refs = []memory.Ref{{ID: a.ID, Type: "related"}}
	first := s.Upsert(ctx, *m, b.Rev, user)
	m.Refs = []memory.Ref{{ID: a.ID, Type: "depends"}}
	if r := s.Upsert(ctx, *m, b.Rev, user); r.Status != "conflict" || first.Status != "ok" {
		t.Fatalf("mudar o vínculo com rev velho deveria conflitar: %+v", r)
	}
}

func TestIdeaAndHowto(t *testing.T) {
	s, user := setup(t)
	idea := s.Upsert(ctx, memory.Memory{Kind: "idea", Workspace: ptr("app"), Scope: "project", Text: "Exportar memórias em markdown"}, 0, user)
	how := s.Upsert(ctx, memory.Memory{Kind: "howto", Scope: "global", Text: "Deploy travado: limpar o cache do runner.", Status: ptr("open")}, 0, user)
	if idea.Status != "ok" || how.Status != "ok" {
		t.Fatalf("criar: %+v %+v", idea, how)
	}
	if m, _ := s.Get(ctx, idea.ID); m.Status == nil || *m.Status != "open" {
		t.Fatalf("ideia nasce aberta: %+v", m.Status)
	}
	if m, _ := s.Get(ctx, how.ID); m.Status != nil {
		t.Fatalf("como fazer não tem estado: %v", *m.Status)
	}
	m, _ := s.Get(ctx, idea.ID)
	m.Status = ptr("arquivada")
	if r := s.Upsert(ctx, *m, m.Rev, user); r.Status != "error" {
		t.Fatalf("estado inválido: %+v", r)
	}
	m.Status, m.TaskKey = ptr("converted"), ptr("APP-9")
	if r := s.Upsert(ctx, *m, m.Rev, user); r.Status != "ok" {
		t.Fatalf("virar tarefa: %+v", r)
	}
	all, _ := s.List(ctx, memory.Query{Workspace: "app"})
	ideas, _ := s.List(ctx, memory.Query{Workspace: "app", Kind: "idea"})
	if len(all) != 1 || all[0].Kind != "howto" || len(ideas) != 1 {
		t.Fatalf("ideia fechada só no filtro de ideias: tudo=%d ideias=%d", len(all), len(ideas))
	}
}
