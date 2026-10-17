// Package auth cuida de usuários, tokens por dev (guardados só como hash) e do middleware HTTP.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// Prefixos facilitam reconhecer um token num arquivo ou log: "bom_" pessoal, "bos_" sessão SSO.
const (
	tokenPrefix   = "bom_"
	sessionPrefix = "bos_"
)

// NewToken gera um token pessoal aleatório e o hash que vai para o banco.
func NewToken() (token string, hash []byte) { return newToken(tokenPrefix) }

// NewSessionToken gera o token de uma sessão SSO.
func NewSessionToken() (token string, hash []byte) { return newToken(sessionPrefix) }

func newToken(prefix string) (token string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // sem fonte de aleatoriedade não há como emitir tokens com segurança
	}
	token = prefix + base64.RawURLEncoding.EncodeToString(b)
	return token, Hash(token)
}

func Hash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
