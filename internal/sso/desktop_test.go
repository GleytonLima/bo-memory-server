package sso_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

const desktopPort = "50123"

func challengeOf(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// desktopLogin faz o caminho do navegador a partir de /auth/desktop e devolve a URL final.
func (e *env) desktopLogin(query string) *url.URL {
	e.t.Helper()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	next := e.srv.URL + "/auth/desktop?" + query
	for i := 0; i < 5; i++ {
		res, err := client.Get(next)
		if err != nil {
			e.t.Fatal(err)
		}
		res.Body.Close()
		u, _ := url.Parse(res.Header.Get("Location"))
		if u.Host == "127.0.0.1:"+desktopPort || u.Fragment != "" {
			return u
		}
		if !u.IsAbs() {
			u, _ = url.Parse(e.srv.URL + u.String())
		}
		next = u.String()
	}
	e.t.Fatal("login do desktop não terminou")
	return nil
}

func (e *env) exchange(code, verifier string) (int, string) {
	body, _ := json.Marshal(map[string]string{"code": code, "verifier": verifier})
	res, err := http.Post(e.srv.URL+"/auth/desktop/token", "application/json", bytes.NewReader(body))
	if err != nil {
		e.t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct{ Session, Error string }
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out.Session
}

func TestDesktopLoginWithLoopbackAndPKCE(t *testing.T) {
	e := setup(t)
	e.idp.Claims = map[string]any{"sub": "dev-1", "email": "dev@empresa.com", "name": "Dev"}
	verifier := strings.Repeat("v", 43)
	q := "port=" + desktopPort + "&challenge=" + challengeOf(verifier)

	// o navegador volta ao app (127.0.0.1) com um código — nunca com a sessão
	back := e.desktopLogin(q)
	if back.Host != "127.0.0.1:"+desktopPort || back.Path != "/callback" || back.Query().Get("code") == "" || strings.Contains(back.String(), "bos_") {
		t.Fatalf("retorno ao app: %s", back)
	}
	// código sem o verificador certo não vale (e é consumido)
	if code, _ := e.exchange(back.Query().Get("code"), strings.Repeat("x", 43)); code != 400 {
		t.Fatalf("verificador errado deveria dar 400: %d", code)
	}
	back = e.desktopLogin(q)
	status, session := e.exchange(back.Query().Get("code"), verifier)
	if status != 200 || !strings.HasPrefix(session, "bos_") {
		t.Fatalf("troca do código: %d %q", status, session)
	}
	if code, u := e.me(session); code != 200 || u.Email != "dev@empresa.com" {
		t.Fatalf("sessão do desktop: %d %+v", code, u)
	}
	if again, _ := e.exchange(back.Query().Get("code"), verifier); again != 400 {
		t.Fatalf("código é de uso único: %d", again)
	}

	// porta fora da faixa ou desafio inválido: erro na web, nada vai para endereço local
	for _, bad := range []string{"port=80&challenge=" + challengeOf(verifier), "port=" + desktopPort + "&challenge=curto"} {
		if u := e.desktopLogin(bad); u.Host == "127.0.0.1:"+desktopPort || !strings.Contains(u.Fragment, "sso-error") {
			t.Fatalf("pedido inválido %q: %s", bad, u)
		}
	}
}
