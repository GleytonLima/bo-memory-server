package api_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"bo-memory-server/internal/api"
	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/memory"
	"bo-memory-server/internal/project"
	"bo-memory-server/internal/testdb"
)

// Projeto (do workspace) e layout (de cada usuário) para trocar de máquina — docs/API.md.
func TestProjectAndLayout(t *testing.T) {
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

	if code := ana.do("GET", "/v1/workspaces/app/project", nil, nil); code != 404 {
		t.Fatalf("sem projeto ainda: %d", code)
	}
	doc := map[string]any{
		"name": "App", "baseVersion": 0,
		"folders": []map[string]string{{"name": "backend", "remote": "git@github.com:Time/Backend.git", "url": "https://github.com/Time/Backend.git"}},
		"columns": []map[string]string{{"id": "c1", "name": "A fazer"}},
	}
	var saved project.Project
	if code := ana.do("PUT", "/v1/workspaces/App/project", doc, &saved); code != 200 {
		t.Fatalf("criar projeto: %d", code)
	}
	if saved.Version != 1 || saved.Workspace != "app" || saved.Folders[0].Remote != "github.com/time/backend" || saved.UpdatedBy.Name != "Ana" {
		t.Fatalf("projeto gravado: %+v", saved)
	}

	// Bob edita sobre a versão 1; Ana, ainda na 1, entra em conflito — mas reenviar o mesmo conteúdo não é conflito.
	doc["baseVersion"] = 1
	doc["columns"] = []map[string]string{{"id": "c1", "name": "A fazer"}, {"id": "c2", "name": "Feito"}}
	if code := bob.do("PUT", "/v1/workspaces/app/project", doc, &saved); code != 200 || saved.Version != 2 {
		t.Fatalf("bob edita: %d %+v", code, saved)
	}
	var conflict struct{ Server project.Project }
	doc2 := map[string]any{"name": "App renomeado", "baseVersion": 1, "folders": doc["folders"], "columns": doc["columns"]}
	if code := ana.do("PUT", "/v1/workspaces/app/project", doc2, &conflict); code != 409 || conflict.Server.Version != 2 || len(conflict.Server.Columns) != 2 {
		t.Fatalf("conflito esperado com a versão do servidor: %d %+v", code, conflict)
	}
	if code := ana.do("PUT", "/v1/workspaces/app/project", doc, &saved); code != 200 || saved.Version != 2 {
		t.Fatalf("mesmo conteúdo com versão velha não é conflito: %d %+v", code, saved)
	}

	// Validação: url com credenciais, pasta sem remote, subdir que sobe de nível.
	bad := []map[string]any{
		{"name": "X", "folders": []map[string]string{{"remote": "github.com/a/b", "url": "https://user:senha@github.com/a/b.git"}}},
		{"name": "X", "folders": []map[string]string{{"name": "sem remote"}}},
		{"name": ""},
	}
	for i, b := range bad {
		if code := ana.do("PUT", "/v1/workspaces/outro/project", b, nil); code != 400 {
			t.Fatalf("documento inválido %d deveria dar 400: %d", i, code)
		}
	}

	// Layout: cada usuário tem o seu; startup do Bob nunca chega à Ana.
	layout := func(startup string) map[string]any {
		return map[string]any{"baseVersion": 0, "groups": []map[string]any{{"name": "Serviços", "terminals": []map[string]any{
			{"name": "api", "folder": "https://github.com/Time/Backend", "subdir": `src\api`, "startup": startup},
		}}}}
	}
	var mine project.Layout
	if code := ana.do("PUT", "/v1/workspaces/app/layout", layout("npm run dev"), &mine); code != 200 || mine.Version != 1 {
		t.Fatalf("layout da Ana: %d %+v", code, mine)
	}
	if term := mine.Groups[0].Terminals[0]; term.Folder != "github.com/time/backend" || term.Subdir != "src/api" {
		t.Fatalf("terminal normalizado: %+v", term)
	}
	bob.do("PUT", "/v1/workspaces/app/layout", layout("rm -rf ~"), nil)
	ana.do("GET", "/v1/workspaces/app/layout", nil, &mine)
	if mine.Groups[0].Terminals[0].Startup != "npm run dev" {
		t.Fatalf("Ana deve ver só o próprio layout: %+v", mine)
	}
	up := layout("x")
	up["groups"].([]map[string]any)[0]["terminals"].([]map[string]any)[0]["subdir"] = "../fora"
	if code := ana.do("PUT", "/v1/workspaces/app/layout", up, nil); code != 400 {
		t.Fatalf("subdir com .. deveria dar 400: %d", code)
	}

	// Lista para a máquina nova e acesso por membros.
	var list struct{ Items []memory.WorkspaceInfo }
	ana.do("GET", "/v1/workspaces", nil, &list)
	has := map[string]bool{}
	for _, w := range list.Items {
		has[w.Key] = w.HasProject
	}
	if !has["app"] {
		t.Fatalf("GET /v1/workspaces deveria marcar hasProject: %+v", list.Items)
	}
	adm.do("PUT", "/v1/workspaces/app/members", map[string]any{"members": []string{"*@x.com"}}, nil)
	if code := bob.do("GET", "/v1/workspaces/app/project", nil, nil); code != 403 {
		t.Fatalf("Bob fora da lista não lê o projeto: %d", code)
	}
	if code := bob.do("PUT", "/v1/workspaces/app/layout", layout(""), nil); code != 403 {
		t.Fatalf("Bob fora da lista não grava layout: %d", code)
	}
	if code := ana.do("GET", "/v1/workspaces/app/project", nil, nil); code != 200 {
		t.Fatalf("Ana (membro) lê o projeto: %d", code)
	}
}
