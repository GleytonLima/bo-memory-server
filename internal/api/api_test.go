package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"bo-memory-server/internal/api"
	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/memory"
	"bo-memory-server/internal/testdb"
)

type client struct {
	t     *testing.T
	base  string
	token string
}

func (c client) do(method, path string, body any, out any) int {
	c.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, c.base+path, &buf)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		_ = json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func TestAPIFlow(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	authStore := auth.NewStore(pool)
	admin, _ := authStore.CreateUser(ctx, "Admin", "admin@x.com", true)
	adminToken, _, _ := authStore.CreateToken(ctx, admin.ID, "cli")
	srv := httptest.NewServer(api.Handler(authStore, memory.NewStore(pool), t.TempDir()))
	defer srv.Close()

	anon := client{t, srv.URL, ""}
	if code := anon.do("GET", "/healthz", nil, nil); code != 200 {
		t.Fatalf("healthz: %d", code)
	}
	if code := anon.do("GET", "/v1/me", nil, nil); code != 401 {
		t.Fatalf("sem token deveria dar 401: %d", code)
	}

	a := client{t, srv.URL, adminToken}
	var dev auth.User
	if code := a.do("POST", "/v1/users", map[string]any{"name": "Dev", "email": "dev@x.com"}, &dev); code != 201 {
		t.Fatalf("criar usuário: %d", code)
	}
	var issued struct{ ID, Token string }
	if code := a.do("POST", "/v1/tokens", map[string]any{"name": "notebook", "userId": dev.ID}, &issued); code != 201 || issued.Token == "" {
		t.Fatalf("emitir token: %d", code)
	}
	d := client{t, srv.URL, issued.Token}
	if code := d.do("GET", "/v1/users", nil, nil); code != 403 {
		t.Fatalf("dev não é admin: %d", code)
	}

	// push → pull → push com rev velho dá conflito
	mem := map[string]any{"id": "7d7cfb1e-8d4f-4a39-9b2e-1a1e0c1c0001", "kind": "note", "scope": "project", "workspace": "app", "text": "v1"}
	var pushed struct{ Results []memory.Result }
	d.do("POST", "/v1/sync/push", map[string]any{"changes": []any{map[string]any{"op": "upsert", "baseRev": 0, "memory": mem}}}, &pushed)
	if len(pushed.Results) != 1 || pushed.Results[0].Status != "ok" {
		t.Fatalf("push: %+v", pushed)
	}
	var pulled memory.PullResponse
	a.do("POST", "/v1/sync/pull", map[string]any{"since": 0, "workspaces": []string{"app"}}, &pulled)
	if len(pulled.Items) != 1 || pulled.Items[0].Author == nil || pulled.Items[0].Author.Name != "Dev" {
		t.Fatalf("pull: %+v", pulled)
	}
	mem["text"] = "v2"
	if code := a.do("PUT", "/v1/memories/"+mem["id"].(string), withRev(mem, pulled.Rev), nil); code != 200 {
		t.Fatalf("editar pela web: %d", code)
	}
	mem["text"] = "v2 concorrente"
	if code := d.do("PUT", "/v1/memories/"+mem["id"].(string), withRev(mem, pulled.Rev), nil); code != 409 {
		t.Fatalf("edição com rev velho deveria dar 409: %d", code)
	}

	var folders struct{ Items []memory.FolderInfo }
	d.do("POST", "/v1/memories", map[string]any{"kind": "note", "scope": "folder", "folder": "git@github.com:Time/Api.git", "text": "porta 8080"}, nil)
	if a.do("GET", "/v1/folders", nil, &folders); len(folders.Items) != 1 || folders.Items[0].Folder != "github.com/time/api" {
		t.Fatalf("folders: %+v", folders)
	}

	// revogar o token do dev corta o acesso
	if code := a.do("DELETE", "/v1/tokens/"+issued.ID, nil, nil); code != 204 {
		t.Fatalf("revogar: %d", code)
	}
	if code := d.do("GET", "/v1/me", nil, nil); code != 401 {
		t.Fatalf("token revogado deveria dar 401: %d", code)
	}
}

func withRev(m map[string]any, rev int64) map[string]any {
	out := map[string]any{"baseRev": rev}
	for k, v := range m {
		out[k] = v
	}
	return out
}
