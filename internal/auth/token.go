// Package auth cuida de usuários, tokens por dev (guardados só como hash) e do middleware HTTP.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

const tokenPrefix = "bom_" // "bo memory": facilita reconhecer o token num arquivo ou log

// NewToken gera um token aleatório e o hash que vai para o banco.
func NewToken() (token string, hash []byte) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // sem fonte de aleatoriedade não há como emitir tokens com segurança
	}
	token = tokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	return token, Hash(token)
}

func Hash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}
