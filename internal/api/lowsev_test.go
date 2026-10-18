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

// Itens de severidade baixa da revisão OWASP: busca só de símbolos, curinga no *@dominio, regra de
// acesso dentro da transação e mensagens de erro sem texto cru do banco.
func TestLowSeverityHardening(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	users := auth.NewStore(pool)
	store := memory.NewStore(pool)
	mk := func(name, email string, admin bool) client {
		u, _ := users.CreateUser(ctx, name, email, admin)
		tok, _, _ := users.CreateToken(ctx, u.ID, "cli")
		return client{t: t, token: tok}
	}
	srv := httptest.NewServer(api.Handler(users, store, t.TempDir(), nil))
	defer srv.Close()
	adm, ana := mk("Admin", "admin@empresa.com", true), mk("Ana", "ana@empresa.com", false)
	adm.base, ana.base = srv.URL, srv.URL

	// busca com termo só de símbolos não vira 500 (to_tsquery("-:*") é erro de sintaxe)
	for _, q := range []string{"-", "_", "--", "a-"} {
		if code := ana.do("GET", "/v1/memories?q="+q, nil, nil); code != 200 {
			t.Fatalf("busca %q deveria dar 200: %d", q, code)
		}
	}

	// "*@%.com" não é curinga: o % é literal (antes virava LIKE e casava qualquer .com)
	adm.do("PUT", "/v1/workspaces/cofre/members", map[string]any{"members": []string{"*@%.com"}}, nil)
	adm.do("POST", "/v1/memories", map[string]any{"kind": "note", "scope": "project", "workspace": "cofre", "text": "do cofre"}, nil)
	if code := ana.do("GET", "/v1/memories?workspace=cofre", nil, nil); code != 403 {
		t.Fatalf("*@%%.com não deveria casar ana@empresa.com: %d", code)
	}

	// regra de acesso conferida dentro da transação (a memória pode mudar de workspace no meio)
	created := store.Upsert(ctx, memory.Memory{Kind: "note", Scope: "project", Workspace: ptr("cofre"), Text: "x"}, 0, "")
	deny := memory.WithGuard(ctx, func(ws *string) bool { return ws == nil || *ws != "cofre" })
	if r := store.Upsert(deny, memory.Memory{ID: created.ID, Kind: "note", Scope: "project", Workspace: ptr("aberto"), Text: "y"}, created.Rev, ""); r.Status != "error" || r.Server != nil {
		t.Fatalf("escrita sem acesso dentro da transação deveria falhar sem devolver o conteúdo: %+v", r)
	}
	if r := store.Delete(deny, created.ID, created.Rev, ""); r.Status != "error" {
		t.Fatalf("delete sem acesso dentro da transação deveria falhar: %+v", r)
	}

	// no máximo 500 mudanças por envio
	big := make([]any, 501)
	for i := range big {
		big[i] = map[string]any{"op": "delete", "id": "x"}
	}
	if code := ana.do("POST", "/v1/sync/push", map[string]any{"changes": big}, nil); code != 400 {
		t.Fatalf("push com 501 mudanças deveria dar 400: %d", code)
	}
	// erros sem texto cru do banco
	var e struct{ Error string }
	if code := adm.do("DELETE", "/v1/tokens/nao-e-uuid", nil, &e); code != 404 || e.Error != "token não encontrado" {
		t.Fatalf("revogar id malformado: %d %q", code, e.Error)
	}
	if code := adm.do("POST", "/v1/users", map[string]any{"name": "Ana 2", "email": "ana@empresa.com"}, &e); code != 400 || e.Error != "Já existe um usuário com este e-mail." {
		t.Fatalf("e-mail repetido: %d %q", code, e.Error)
	}
}

func ptr(s string) *string { return &s }
