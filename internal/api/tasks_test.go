package api_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"bo-memory-server/internal/api"
	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/memory"
	"bo-memory-server/internal/task"
	"bo-memory-server/internal/testdb"
)

// Quadro de tarefas do workspace — docs/API.md → "Quadro de tarefas".
func TestTaskBoardSync(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	users := auth.NewStore(pool)
	srv := httptest.NewServer(api.Handler(users, memory.NewStore(pool), t.TempDir(), nil))
	defer srv.Close()
	mk := func(name, email string, admin bool) client {
		u, _ := users.CreateUser(ctx, name, email, admin)
		tok, _, _ := users.CreateToken(ctx, u.ID, "cli")
		return client{t: t, base: srv.URL, token: tok}
	}
	adm, ana, bob := mk("Admin", "admin@x.com", true), mk("Ana", "ana@x.com", false), mk("Bob", "bob@y.com", false)
	type results struct{ Results []task.Result }
	push := func(c client, changes ...map[string]any) []task.Result {
		var out results
		if code := c.do("POST", "/v1/tasks/push", map[string]any{"changes": changes}, &out); code != 200 {
			t.Fatalf("push: %d", code)
		}
		return out.Results
	}
	upsert := func(id, key string, base int64, title string) map[string]any {
		return map[string]any{"op": "upsert", "workspace": "App", "baseRev": base, "id": id, "key": key, "task": map[string]any{"title": title}}
	}

	// Ana e Bob criam offline a mesma chave: a segunda é renumerada.
	r := push(ana, upsert("t1", "AP-1", 0, "Login"), upsert("t2", "AP-2", 0, "Logout"))
	if r[0].Status != "ok" || r[0].Key != "AP-1" || r[1].Key != "AP-2" {
		t.Fatalf("criar: %+v", r)
	}
	rb := push(bob, upsert("t9", "AP-1", 0, "Cadastro"))
	if rb[0].Status != "ok" || rb[0].Key != "AP-3" {
		t.Fatalf("chave repetida deveria virar AP-3: %+v", rb)
	}

	// Conflito: Bob edita sobre o rev atual; Ana, com o rev velho, recebe a versão do servidor.
	r1 := push(bob, upsert("t1", "AP-1", r[0].Rev, "Login com SSO"))
	if r1[0].Status != "ok" {
		t.Fatalf("Bob edita: %+v", r1)
	}
	rc := push(ana, upsert("t1", "AP-1", r[0].Rev, "Login com senha"))
	if rc[0].Status != "conflict" || rc[0].Server == nil || rc[0].Server.Rev != r1[0].Rev || rc[0].Server.UpdatedBy.Name != "Bob" {
		t.Fatalf("conflito esperado: %+v", rc)
	}
	if same := push(ana, upsert("t1", "AP-1", r[0].Rev, "Login com SSO")); same[0].Status != "ok" {
		t.Fatalf("mesmo conteúdo com rev velho não é conflito: %+v", same)
	}

	// Exclusão vira tombstone e libera a chave.
	del := push(ana, map[string]any{"op": "delete", "workspace": "app", "baseRev": r[1].Rev, "id": "t2"})
	if del[0].Status != "ok" {
		t.Fatalf("apagar: %+v", del)
	}
	if again := push(bob, upsert("t5", "AP-2", 0, "Reusa a chave")); again[0].Key != "AP-2" {
		t.Fatalf("chave de tarefa apagada pode ser reusada: %+v", again)
	}

	// Pull traz tudo desde 0, com a exclusão, em ordem de rev.
	var pulled task.PullResponse
	ana.do("POST", "/v1/tasks/pull", map[string]any{"since": 0, "workspaces": []string{"app"}}, &pulled)
	byID := map[string]task.Task{}
	for _, it := range pulled.Items {
		byID[it.ID] = it
	}
	if len(byID) != 4 || !byID["t2"].Deleted || string(byID["t1"].Doc) != `{"title":"Login com SSO"}` || pulled.Rev == 0 {
		t.Fatalf("pull: %+v", pulled)
	}
	var since task.PullResponse
	ana.do("POST", "/v1/tasks/pull", map[string]any{"since": pulled.Rev, "workspaces": []string{"app"}}, &since)
	if len(since.Items) != 0 {
		t.Fatalf("nada novo desde o último rev: %+v", since.Items)
	}

	// Validação e membros.
	bad := push(ana, map[string]any{"op": "upsert", "workspace": "app", "id": "t7", "key": "sem número", "task": map[string]any{}},
		map[string]any{"op": "upsert", "workspace": "app", "id": "t8", "key": "AP-9", "task": "texto"})
	if bad[0].Status != "error" || bad[1].Status != "error" {
		t.Fatalf("chave e conteúdo inválidos: %+v", bad)
	}
	adm.do("PUT", "/v1/workspaces/app/members", map[string]any{"members": []string{"*@x.com"}}, nil)
	if denied := push(bob, upsert("t6", "AP-6", 0, "Bob fora")); denied[0].Status != "error" {
		t.Fatalf("Bob fora da lista não grava: %+v", denied)
	}
	var none task.PullResponse
	bob.do("POST", "/v1/tasks/pull", map[string]any{"since": 0, "workspaces": []string{"app"}}, &none)
	if len(none.Items) != 0 {
		t.Fatalf("Bob fora da lista não lê: %+v", none.Items)
	}
}
