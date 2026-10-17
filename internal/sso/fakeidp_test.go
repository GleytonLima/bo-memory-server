package sso_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// fakeIdP é um provedor OpenID Connect mínimo: discovery, JWKS, /authorize (devolve o código na
// hora, como se o usuário tivesse entrado) e /token (confere o PKCE e assina o ID token).
type fakeIdP struct {
	*httptest.Server
	key      *rsa.PrivateKey
	clientID string
	mu       sync.Mutex
	codes    map[string]grant
	// próximo usuário a "entrar"
	Claims map[string]any
	// para testar falhas
	WrongNonce bool
}

type grant struct{ challenge, nonce string }

func newFakeIdP(t *testing.T, clientID string) *fakeIdP {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{key: key, clientID: clientID, codes: map[string]grant{}}
	mux := http.NewServeMux()
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": f.URL, "authorization_endpoint": f.URL + "/authorize", "token_endpoint": f.URL + "/token",
			"jwks_uri": f.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != clientID {
			http.Error(w, "pedido sem PKCE ou de outro cliente", http.StatusBadRequest)
			return
		}
		code := base64.RawURLEncoding.EncodeToString([]byte(time.Now().String()))
		f.mu.Lock()
		f.codes[code] = grant{challenge: q.Get("code_challenge"), nonce: q.Get("nonce")}
		f.mu.Unlock()
		back, _ := url.Parse(q.Get("redirect_uri"))
		back.RawQuery = url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
		http.Redirect(w, r, back.String(), http.StatusFound)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		g, ok := f.codes[r.Form.Get("code")]
		delete(f.codes, r.Form.Get("code"))
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		nonce := g.nonce
		if f.WrongNonce {
			nonce = "outro"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "x", "token_type": "Bearer", "expires_in": 3600, "id_token": f.sign(t, nonce)})
	})
	return f
}

func (f *fakeIdP) sign(t *testing.T, nonce string) string {
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key}, (&jose.SignerOptions{}).WithHeader("kid", "k1").WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	claims := map[string]any{"iss": f.URL, "aud": f.clientID, "iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": nonce}
	for k, v := range f.Claims {
		claims[k] = v
	}
	raw, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
