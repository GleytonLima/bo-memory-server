package memory_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/memory"
	"bo-memory-server/internal/testdb"
)

var ctx = context.Background()

func ptr(s string) *string { return &s }

func setup(t *testing.T) (*memory.Store, string) {
	pool := testdb.Open(t)
	u, err := auth.NewStore(pool).CreateUser(ctx, "Dev", "dev@x.com", false)
	if err != nil {
		t.Fatal(err)
	}
	return memory.NewStore(pool), u.ID
}

func note(ws, text string) memory.Memory {
	return memory.Memory{Kind: "note", Scope: "project", Workspace: ptr(ws), Text: text}
}

func TestCreateEditAndConflict(t *testing.T) {
	s, user := setup(t)
	created := s.Upsert(ctx, note("app", "primeira"), 0, user)
	if created.Status != "ok" || created.Rev == 0 {
		t.Fatalf("criar: %+v", created)
	}
	m, _ := s.Get(ctx, created.ID)
	m.Text = "editada"
	edited := s.Upsert(ctx, *m, created.Rev, user)
	if edited.Status != "ok" || edited.Rev <= created.Rev {
		t.Fatalf("editar: %+v", edited)
	}
	// outra máquina ainda com o rev antigo, conteúdo diferente → conflito com a versão do servidor
	m.Text = "edição concorrente"
	conflict := s.Upsert(ctx, *m, created.Rev, user)
	if conflict.Status != "conflict" || conflict.Server.Text != "editada" || conflict.Server.Rev != edited.Rev {
		t.Fatalf("esperava conflito: %+v", conflict)
	}
	// mesmo conteúdo que o servidor, rev antigo → não é conflito
	m.Text = "editada"
	if same := s.Upsert(ctx, *m, created.Rev, user); same.Status != "ok" {
		t.Fatalf("conteúdo idêntico não deveria conflitar: %+v", same)
	}
	if h, _ := s.History(ctx, created.ID); len(h) != 1 {
		t.Fatalf("histórico: quero 1 versão anterior, tenho %d", len(h))
	}
}

func TestDecisionValidation(t *testing.T) {
	s, user := setup(t)
	long := memory.Memory{Kind: "decision", Scope: "global", Text: strings.Repeat("x", 281)}
	if r := s.Upsert(ctx, long, 0, user); r.Status != "error" || !strings.Contains(r.Error, "280") {
		t.Fatalf("decisão longa: %+v", r)
	}
	multi := memory.Memory{Kind: "decision", Scope: "global", Text: "linha 1\nlinha 2"}
	if r := s.Upsert(ctx, multi, 0, user); r.Status != "error" {
		t.Fatalf("decisão com quebra de linha: %+v", r)
	}
	noWs := memory.Memory{Kind: "note", Scope: "project", Text: "sem workspace"}
	if r := s.Upsert(ctx, noWs, 0, user); r.Status != "error" {
		t.Fatalf("projeto sem workspace: %+v", r)
	}
}

func TestPullScopesAndTombstones(t *testing.T) {
	s, user := setup(t)
	global := s.Upsert(ctx, memory.Memory{Kind: "decision", Scope: "global", Text: "Vale para todos porque sim."}, 0, user)
	mine := s.Upsert(ctx, note("app", "do app"), 0, user)
	s.Upsert(ctx, note("outro", "de outro workspace"), 0, user)
	folder := s.Upsert(ctx, memory.Memory{Kind: "note", Scope: "folder", Folder: ptr("https://github.com/Gleyton/Backend.git"), Text: "convenção do backend"}, 0, user)
	s.Upsert(ctx, memory.Memory{Kind: "note", Scope: "folder", Folder: ptr("github.com/x/outro-repo"), Text: "outro repo"}, 0, user)

	got, err := s.Pull(ctx, memory.PullRequest{Since: 0, Workspaces: []string{"app"}, Folders: []string{"git@github.com:gleyton/backend.git"}})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, m := range got.Items {
		ids[m.ID] = true
	}
	if len(got.Items) != 3 || !ids[global.ID] || !ids[mine.ID] || !ids[folder.ID] {
		t.Fatalf("pull deveria trazer global + app + pasta backend; veio %d itens", len(got.Items))
	}
	// apagar gera tombstone que o pull seguinte entrega
	if r := s.Delete(ctx, mine.ID, mine.Rev, user); r.Status != "ok" {
		t.Fatalf("apagar: %+v", r)
	}
	next, _ := s.Pull(ctx, memory.PullRequest{Since: got.Rev, Workspaces: []string{"app"}})
	if len(next.Items) != 1 || next.Items[0].ID != mine.ID || !next.Items[0].Deleted {
		t.Fatalf("pull deveria trazer só o tombstone: %+v", next.Items)
	}
}

func TestProgressOnePerTaskAndClaim(t *testing.T) {
	s, user := setup(t)
	prog := func(text string) memory.Memory {
		return memory.Memory{Kind: "progress", Workspace: ptr("app"), TaskKey: ptr("BO-22"), Text: text,
			Progress: json.RawMessage(`{"status":"login feito","nextSteps":["refresh token"]}`)}
	}
	first := s.Upsert(ctx, prog("progresso da máquina A"), 0, user)
	second := s.Upsert(ctx, prog("progresso da máquina B"), 0, user)
	if first.Status != "ok" || second.Status != "conflict" || second.Server.ID != first.ID {
		t.Fatalf("segundo progresso da mesma tarefa deveria conflitar: %+v / %+v", first, second)
	}
	claimed, err := s.Claim(ctx, "app", "BO-22", memory.Author{ID: user, Name: "Dev"})
	if err != nil || !strings.Contains(string(claimed.Progress), `"owner"`) || claimed.Rev <= first.Rev {
		t.Fatalf("retomar: %v %+v", err, claimed)
	}
}

func TestSupersede(t *testing.T) {
	s, user := setup(t)
	old := s.Upsert(ctx, memory.Memory{Kind: "decision", Workspace: ptr("app"), Text: "Usamos MySQL porque sim."}, 0, user)
	res, err := s.Supersede(ctx, old.ID, memory.Memory{Text: "Usamos Postgres porque o time domina."}, user)
	if err != nil || res.Status != "ok" {
		t.Fatalf("substituir: %v %+v", err, res)
	}
	list, _ := s.List(ctx, memory.Query{Workspace: "app", Kind: "decision"})
	if len(list) != 1 || list[0].ID != res.ID {
		t.Fatalf("só a decisão nova deveria estar vigente: %+v", list)
	}
	if again, _ := s.List(ctx, memory.Query{Workspace: "app", Q: "postgres TIME"}); len(again) != 1 {
		t.Fatalf("busca por prefixo/sem caixa deveria achar a nova: %d", len(again))
	}
}

// Progresso de tarefa concluída ("done") sai da busca padrão e fica no filtro de progresso; estado
// inválido é recusado; reabrir ("open") traz de volta.
func TestProgressDoneLeavesDefaultSearch(t *testing.T) {
	s, user := setup(t)
	p := memory.Memory{Kind: "progress", Workspace: ptr("app"), TaskKey: ptr("BO-30"), Text: "BO-30: deploy pela metade",
		Progress: json.RawMessage(`{"status":"falta aprovar"}`)}
	r := s.Upsert(ctx, p, 0, user)
	texts := func(q memory.Query) int {
		items, err := s.List(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		return len(items)
	}
	if texts(memory.Query{Workspace: "app", Q: "deploy"}) != 1 {
		t.Fatal("progresso aberto aparece na busca")
	}
	cur, _ := s.Get(ctx, r.ID)
	cur.Status = ptr("done")
	if u := s.Upsert(ctx, *cur, cur.Rev, user); u.Status != "ok" {
		t.Fatalf("arquivar: %+v", u)
	}
	if texts(memory.Query{Workspace: "app", Q: "deploy"}) != 0 || texts(memory.Query{Workspace: "app", Kind: "progress"}) != 1 {
		t.Fatal("concluída: fora da busca padrão, dentro do filtro de progresso")
	}
	cur, _ = s.Get(ctx, r.ID)
	cur.Status = ptr("perdido")
	if u := s.Upsert(ctx, *cur, cur.Rev, user); u.Status != "error" {
		t.Fatalf("estado inválido de progresso deveria ser recusado: %+v", u)
	}
	cur.Status = ptr("open")
	if u := s.Upsert(ctx, *cur, cur.Rev, user); u.Status != "ok" || texts(memory.Query{Workspace: "app", Q: "deploy"}) != 1 {
		t.Fatalf("reaberta volta à busca: %+v", u)
	}
	n := note("app", "nota")
	n.Status = ptr("done")
	if u := s.Upsert(ctx, n, 0, user); u.Status != "ok" {
		t.Fatalf("nota com estado: o estado é ignorado: %+v", u)
	}
}
