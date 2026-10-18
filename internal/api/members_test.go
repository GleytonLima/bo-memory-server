package api_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"bo-memory-server/internal/api"
	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/memory"
	"bo-memory-server/internal/testdb"
)

func TestWorkspaceMembersByEmail(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	users := auth.NewStore(pool)
	mk := func(name, email string, admin bool) client {
		u, _ := users.CreateUser(ctx, name, email, admin)
		tok, _, _ := users.CreateToken(ctx, u.ID, "cli")
		return client{t: t, token: tok}
	}
	srv := httptest.NewServer(api.Handler(users, memory.NewStore(pool), t.TempDir(), nil))
	defer srv.Close()
	adm, ana, bob := mk("Admin", "admin@empresa.com", true), mk("Ana", "ana@empresa.com", false), mk("Bob", "bob@outra.com", false)
	for _, c := range []*client{&adm, &ana, &bob} {
		c.base = srv.URL
	}

	// app: todo o domínio; secreto: só o chefe; aberto: sem lista
	if code := adm.do("PUT", "/v1/workspaces/app/members", map[string]any{"members": []string{"*@Empresa.com"}}, nil); code != 200 {
		t.Fatalf("membros de app: %d", code)
	}
	adm.do("PUT", "/v1/workspaces/secreto/members", map[string]any{"members": []string{"chefe@empresa.com"}}, nil)
	if code := adm.do("PUT", "/v1/workspaces/x/members", map[string]any{"members": []string{"não é e-mail"}}, nil); code != 400 {
		t.Fatalf("membro inválido deveria dar 400: %d", code)
	}
	note := func(ws, text string) map[string]any {
		return map[string]any{"kind": "note", "scope": "project", "workspace": ws, "text": text}
	}
	var inApp, inSecret struct{ ID string }
	adm.do("POST", "/v1/memories", note("app", "do app"), &inApp)
	adm.do("POST", "/v1/memories", map[string]any{"kind": "note", "scope": "project", "workspace": "secreto", "text": "segredo", "refs": []any{map[string]string{"id": inApp.ID, "type": "depends"}}}, &inSecret)
	adm.do("POST", "/v1/memories", note("aberto", "do aberto"), nil)

	// Ana (domínio): app sim, secreto não
	if code := ana.do("GET", "/v1/memories?workspace=app", nil, nil); code != 200 {
		t.Fatalf("Ana lê app: %d", code)
	}
	if code := ana.do("GET", "/v1/memories?workspace=secreto", nil, nil); code != 403 {
		t.Fatalf("Ana não lê secreto: %d", code)
	}
	var pulled memory.PullResponse
	ana.do("POST", "/v1/sync/pull", map[string]any{"since": 0, "workspaces": []string{"app", "secreto", "aberto"}}, &pulled)
	texts := map[string]bool{}
	for _, m := range pulled.Items {
		texts[m.Text] = true
	}
	if !texts["do app"] || !texts["do aberto"] || texts["segredo"] {
		t.Fatalf("pull deveria trazer só app e aberto: %v", texts)
	}
	var pushed struct{ Results []memory.Result }
	ana.do("POST", "/v1/sync/push", map[string]any{"changes": []any{
		map[string]any{"op": "upsert", "baseRev": 0, "memory": note("secreto", "invasão")},
		map[string]any{"op": "upsert", "baseRev": 0, "memory": note("app", "legítima")},
	}}, &pushed)
	if len(pushed.Results) != 2 || pushed.Results[0].Status != "error" || pushed.Results[1].Status != "ok" {
		t.Fatalf("push: secreto recusado e app aceito, na ordem: %+v", pushed.Results)
	}
	var links memory.Links
	ana.do("GET", "/v1/memories/"+inApp.ID+"/links", nil, &links)
	if len(links.Incoming) != 0 {
		t.Fatalf("vínculo vindo de projeto alheio não aparece (nem o id): %+v", links.Incoming)
	}

	// Bob (outro domínio): só o aberto
	if code := bob.do("GET", "/v1/memories?workspace=app", nil, nil); code != 403 {
		t.Fatalf("Bob não lê app: %d", code)
	}
	var ws struct{ Items []memory.WorkspaceInfo }
	bob.do("GET", "/v1/workspaces", nil, &ws)
	if len(ws.Items) != 1 || ws.Items[0].Key != "aberto" {
		t.Fatalf("Bob vê só o workspace aberto: %+v", ws.Items)
	}
	var all struct{ Items []memory.WorkspaceInfo }
	adm.do("GET", "/v1/workspaces", nil, &all)
	if len(all.Items) != 3 || !all.Items[0].Restricted && all.Items[0].Key == "app" {
		t.Fatalf("admin vê todos: %+v", all.Items)
	}

	// IDOR no push: delete do secreto "disfarçado" com um memory de workspace aberto junto
	var trick struct{ Results []memory.Result }
	ana.do("POST", "/v1/sync/push", map[string]any{"changes": []any{map[string]any{
		"op": "delete", "id": inSecret.ID, "baseRev": 0, "memory": note("aberto", "álibi"),
	}}}, &trick)
	if len(trick.Results) != 1 || trick.Results[0].Status != "error" || trick.Results[0].Server != nil {
		t.Fatalf("delete de memória do secreto deveria ser recusado (sem devolver o conteúdo): %+v", trick.Results)
	}
	var secret struct{ Items []memory.Memory }
	adm.do("GET", "/v1/memories?workspace=secreto", nil, &secret)
	if len(secret.Items) != 1 || secret.Items[0].Text != "segredo" {
		t.Fatalf("a memória do secreto continua lá: %+v", secret.Items)
	}

	// memória de pasta criada no secreto: quem conhece o remote não lê sem ser membro
	adm.do("POST", "/v1/memories", map[string]any{"kind": "note", "scope": "folder", "folder": "github.com/empresa/repo", "workspace": "secreto", "text": "pasta secreta"}, nil)
	var byFolder struct{ Items []memory.Memory }
	ana.do("GET", "/v1/memories?folder=github.com/empresa/repo", nil, &byFolder)
	var pulledFolder memory.PullResponse
	ana.do("POST", "/v1/sync/pull", map[string]any{"since": 0, "folders": []string{"github.com/empresa/repo"}}, &pulledFolder)
	for _, m := range append(byFolder.Items, pulledFolder.Items...) {
		if m.Text == "pasta secreta" {
			t.Fatalf("memória de pasta do secreto vazou para quem não é membro")
		}
	}
	var folders struct{ Items []memory.FolderInfo }
	ana.do("GET", "/v1/folders", nil, &folders)
	for _, f := range folders.Items {
		if f.Folder == "github.com/empresa/repo" {
			t.Fatalf("pasta só com memórias do secreto não aparece para quem não é membro: %+v", folders.Items)
		}
	}

	// memória que saiu do secreto para o aberto: o histórico não mostra a versão de quando era secreta
	var moved struct {
		ID  string
		Rev int64
	}
	adm.do("POST", "/v1/memories", note("secreto", "plano confidencial"), &moved)
	if code := adm.do("PUT", "/v1/memories/"+moved.ID, map[string]any{"kind": "note", "scope": "project", "workspace": "aberto", "text": "plano público", "baseRev": moved.Rev}, nil); code != 200 {
		t.Fatalf("mover a memória para o aberto: %d (rev %d)", code, moved.Rev)
	}
	var hist struct{ Items []map[string]any }
	ana.do("GET", "/v1/memories/"+moved.ID+"/history", nil, &hist)
	for _, h := range hist.Items {
		if data, _ := h["data"].(map[string]any); data["text"] == "plano confidencial" {
			t.Fatalf("histórico mostrou a versão de quando a memória era do secreto")
		}
	}
	var admHist struct{ Items []map[string]any }
	adm.do("GET", "/v1/memories/"+moved.ID+"/history", nil, &admHist)
	if len(admHist.Items) != 1 || len(hist.Items) != 0 {
		t.Fatalf("o admin vê a versão anterior (secreta); a Ana, nenhuma: admin %d, Ana %d", len(admHist.Items), len(hist.Items))
	}
}
