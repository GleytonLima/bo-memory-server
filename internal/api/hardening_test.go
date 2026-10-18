package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"bo-memory-server/internal/api"
	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/memory"
	"bo-memory-server/internal/testdb"
)

// Cabeçalhos de segurança em toda resposta e a web servindo só os arquivos da interface
// (nada de .git/, test/ ou CLAUDE.md da pasta do bo-memory-web).
func TestSecurityHeadersAndPrivateFiles(t *testing.T) {
	pool := testdb.Open(t)
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.html", "<!doctype html>INDEX")
	write("js/main.js", "console.log('app')")
	write(".git/config", "[remote] url=segredo")
	write("CLAUDE.md", "notas internas")
	write("test/run.js", "teste")
	srv := httptest.NewServer(api.Handler(auth.NewStore(pool), memory.NewStore(pool), dir, nil))
	defer srv.Close()

	get := func(path string) (*http.Response, string) {
		res, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res, string(b)
	}
	res, body := get("/js/main.js")
	if !strings.Contains(body, "app") {
		t.Fatalf("arquivo da interface deveria ser servido: %q", body)
	}
	for _, h := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Content-Security-Policy"} {
		if res.Header.Get(h) == "" {
			t.Fatalf("falta o cabeçalho %s", h)
		}
	}
	if !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Fatalf("CSP sem frame-ancestors: %q", res.Header.Get("Content-Security-Policy"))
	}
	for _, private := range []string{"/.git/config", "/CLAUDE.md", "/test/run.js"} {
		if _, body := get(private); strings.Contains(body, "segredo") || strings.Contains(body, "notas internas") || body == "teste" {
			t.Fatalf("%s não deveria ser servido: %q", private, body)
		}
	}
}
