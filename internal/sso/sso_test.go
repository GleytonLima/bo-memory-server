package sso_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"bo-memory-server/internal/api"
	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/memory"
	"bo-memory-server/internal/sso"
	"bo-memory-server/internal/testdb"
)

var ctx = context.Background()

type env struct {
	t     *testing.T
	idp   *fakeIdP
	users *auth.Store
	srv   *httptest.Server

	dropCookies bool // simula o callback chegando por outro navegador
}

// setup: banco limpo, provedor falso e o servidor de memória com SSO apontando para ele.
func setup(t *testing.T) *env {
	pool := testdb.Open(t)
	idp := newFakeIdP(t, "bo-client")
	users := auth.NewStore(pool)
	e := &env{t: t, idp: idp, users: users}
	var handler http.Handler
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(e.srv.Close)
	cfg := sso.Config{
		Issuer: idp.URL, ClientID: "bo-client", ClientSecret: "segredo", PublicURL: e.srv.URL, Scopes: []string{"openid", "profile", "email"},
		EmailClaims: []string{"email", "preferred_username"}, EmployeeID: []string{"employeeid"}, ProviderName: "Microsoft", SessionTTL: time.Hour,
	}
	flow, err := sso.New(ctx, cfg, pool, users)
	if err != nil {
		t.Fatal(err)
	}
	handler = api.Handler(users, memory.NewStore(pool), t.TempDir(), flow)
	return e
}

// login faz o caminho do navegador: /auth/login → provedor → /auth/callback; devolve o fragmento final.
func (e *env) login(returnTo string) url.Values {
	e.t.Helper()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if !e.dropCookies {
		client.Jar, _ = cookiejar.New(nil) // o callback exige o cookie posto no /auth/login (mesmo navegador)
	}
	next := e.srv.URL + "/auth/login?return=" + url.QueryEscape(returnTo)
	for i := 0; i < 5; i++ {
		res, err := client.Get(next)
		if err != nil {
			e.t.Fatal(err)
		}
		res.Body.Close()
		loc := res.Header.Get("Location")
		if loc == "" {
			e.t.Fatalf("sem redirecionamento em %s (%d)", next, res.StatusCode)
		}
		u, _ := url.Parse(loc)
		if u.Fragment != "" {
			frag, _ := url.ParseQuery(u.EscapedFragment())
			frag.Set("_path", u.Path)
			return frag
		}
		if !u.IsAbs() {
			loc = e.srv.URL + loc
		}
		next = loc
	}
	e.t.Fatal("login não terminou")
	return nil
}

func (e *env) me(token string) (int, auth.User) {
	req, _ := http.NewRequest("GET", e.srv.URL+"/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var u auth.User
	_ = json.NewDecoder(res.Body).Decode(&u)
	return res.StatusCode, u
}

func TestLoginCreatesUserAndSession(t *testing.T) {
	e := setup(t)
	e.idp.Claims = map[string]any{"sub": "abc-123", "preferred_username": "Ana@Empresa.com", "name": "Ana Lima", "employeeid": "E-77"}
	frag := e.login("/")
	session := frag.Get("sso")
	if !strings.HasPrefix(session, "bos_") {
		t.Fatalf("esperava sessão no fragmento: %v", frag)
	}
	code, u := e.me(session)
	if code != 200 || u.Email != "ana@empresa.com" || u.EmployeeID != "E-77" || !u.SSO || u.IsAdmin {
		t.Fatalf("usuário do SSO: %d %+v", code, u)
	}
	// novo login do mesmo sub, com outro e-mail: mesmo usuário, e-mail atualizado
	e.idp.Claims["preferred_username"] = "ana.lima@empresa.com"
	_, again := e.me(e.login("/").Get("sso"))
	if again.ID != u.ID || again.Email != "ana.lima@empresa.com" {
		t.Fatalf("relogin deveria manter o usuário e atualizar o e-mail: %+v", again)
	}
}

func TestLoginLinksExistingAdminByEmail(t *testing.T) {
	e := setup(t)
	admin, _ := e.users.CreateUser(ctx, "Chefe", "chefe@empresa.com", true)
	e.idp.Claims = map[string]any{"sub": "xyz", "email": "chefe@empresa.com", "name": "Chefe"}
	_, u := e.me(e.login("/").Get("sso"))
	if u.ID != admin.ID || !u.IsAdmin || !u.SSO {
		t.Fatalf("1º login deveria vincular o admin criado pelo CLI: %+v", u)
	}
}

func TestLoginRejectsBadNonceAndUnsafeReturn(t *testing.T) {
	e := setup(t)
	e.idp.Claims = map[string]any{"sub": "s1", "email": "x@empresa.com"}
	e.idp.WrongNonce = true
	if frag := e.login("/"); frag.Get("sso") != "" || frag.Get("sso-error") == "" {
		t.Fatalf("nonce diferente deveria falhar: %v", frag)
	}
	e.idp.WrongNonce = false
	// "/\t/evil" vira "//evil" no navegador: qualquer caractere fora do simples volta para "/"
	for _, bad := range []string{"//evil.example/roubo", "/\t/evil.example", "/\\evil.example", "/a//evil", "https://evil.example"} {
		if frag := e.login(bad); frag.Get("_path") != "/" || frag.Get("sso") == "" {
			t.Fatalf("retorno %q deveria virar '/': %v", bad, frag)
		}
	}
}

func TestLoginRejectsUnverifiedEmailAndOtherBrowser(t *testing.T) {
	e := setup(t)
	e.idp.Claims = map[string]any{"sub": "s9", "email": "z@empresa.com", "email_verified": false}
	if frag := e.login("/"); frag.Get("sso") != "" || !strings.Contains(frag.Get("sso-error"), "email_verified") {
		t.Fatalf("e-mail não verificado deveria ser recusado: %v", frag)
	}
	// callback sem o cookie do /auth/login (outro navegador: login CSRF / fixação)
	e.idp.Claims = map[string]any{"sub": "s9", "email": "z@empresa.com", "email_verified": true}
	e.dropCookies = true
	if frag := e.login("/"); frag.Get("sso") != "" || !strings.Contains(frag.Get("sso-error"), "outro navegador") {
		t.Fatalf("callback sem o cookie do login deveria ser recusado: %v", frag)
	}
}

func TestSessionExpiryLogoutAndPersonalTokens(t *testing.T) {
	e := setup(t)
	e.idp.Claims = map[string]any{"sub": "s2", "email": "bia@empresa.com", "name": "Bia"}
	session := e.login("/").Get("sso")
	_, u := e.me(session)
	personal, _, _ := e.users.CreateToken(ctx, u.ID, "notebook")

	e.users.PersonalTokens = false
	if code, _ := e.me(personal); code != 401 {
		t.Fatalf("token pessoal deveria ser recusado com BO_PERSONAL_TOKENS=false: %d", code)
	}
	if code, _ := e.me(session); code != 200 {
		t.Fatalf("sessão SSO continua valendo: %d", code)
	}
	e.users.PersonalTokens = true

	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/logout", nil)
	req.Header.Set("Authorization", "Bearer "+session)
	if res, _ := http.DefaultClient.Do(req); res.StatusCode != 204 {
		t.Fatalf("logout: %d", res.StatusCode)
	}
	if code, _ := e.me(session); code != 401 {
		t.Fatalf("sessão revogada no logout: %d", code)
	}
	expired, _ := e.users.CreateSession(ctx, u.ID, -time.Minute)
	if code, _ := e.me(expired); code != 401 {
		t.Fatalf("sessão expirada deveria ser recusada: %d", code)
	}
}

func TestAuthConfig(t *testing.T) {
	e := setup(t)
	res, _ := http.Get(e.srv.URL + "/auth/config")
	var cfg map[string]any
	_ = json.NewDecoder(res.Body).Decode(&cfg)
	if cfg["sso"] != true || cfg["provider"] != "Microsoft" || cfg["personalTokens"] != true {
		t.Fatalf("config: %v", cfg)
	}
}
