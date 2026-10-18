package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// O balde barra a rajada além do limite (429 com Retry-After) e separa as chaves.
func TestLimiter(t *testing.T) {
	l := newLimiter(60, 3) // 1 por segundo, rajada de 3
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	h := l.wrap(byToken, ok)
	call := func(token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/v1/me", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for i := 0; i < 3; i++ {
		if c := call("a").Code; c != 204 {
			t.Fatalf("pedido %d dentro da rajada: %d", i+1, c)
		}
	}
	if w := call("a"); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatalf("além da rajada deveria dar 429 com Retry-After: %d %q", w.Code, w.Header().Get("Retry-After"))
	}
	if c := call("b").Code; c != 204 {
		t.Fatalf("outro token tem o próprio balde: %d", c)
	}
	if byIP(httptest.NewRequest("GET", "/", nil)) == "" {
		t.Fatal("byIP sem endereço")
	}
}
