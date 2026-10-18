package sso

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Login do app desktop (RFC 8252, "OAuth para apps nativos"): o app abre /auth/desktop no navegador
// do sistema com a porta de um endereço local seu (127.0.0.1) e o desafio PKCE (S256 de um verificador
// que só ele conhece). O login segue igual ao da web; no fim, em vez da sessão, o navegador leva ao
// app um código de uso único (2 min), que só vale junto com o verificador.

const (
	desktopPrefix = "desktop:"
	codeTTL       = "2 minutes"
)

var (
	challengeRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
	ErrDesktopArg = errors.New("pedido de login do app inválido: atualize o Brain Organizer")
	ErrCode       = errors.New("código de login expirado ou inválido: entre de novo pelo app")
)

// desktopReturn vai no return_to do estado: "desktop:<porta>:<desafio>".
func desktopReturn(port int, challenge string) (string, error) {
	if port < 1024 || port > 65535 || !challengeRe.MatchString(challenge) {
		return "", ErrDesktopArg
	}
	return fmt.Sprintf("%s%d:%s", desktopPrefix, port, challenge), nil
}

func parseDesktop(returnTo string) (port int, challenge string, ok bool) {
	rest, found := strings.CutPrefix(returnTo, desktopPrefix)
	if !found {
		return 0, "", false
	}
	p, c, _ := strings.Cut(rest, ":")
	port, err := strconv.Atoi(p)
	return port, c, err == nil
}

// loopback: o endereço local do app (só 127.0.0.1: nada de nomes que um DNS possa redirecionar).
func loopback(port int) string { return fmt.Sprintf("http://127.0.0.1:%d/callback", port) }

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// StartDesktop: como Start, mas o retorno é o app (porta local + desafio PKCE).
func (f *Flow) StartDesktop(ctx context.Context, port int, challenge string) (target, state string, err error) {
	ret, err := desktopReturn(port, challenge)
	if err != nil {
		return "", "", err
	}
	return f.start(ctx, ret)
}

// desktopCode guarda (pelo hash) um código de uso único para o usuário que acabou de entrar.
func (f *Flow) desktopCode(ctx context.Context, userID, challenge string) (string, error) {
	code := random()
	if _, err := f.pool.Exec(ctx, `DELETE FROM sso_desktop_codes WHERE created_at < now() - interval '`+codeTTL+`'`); err != nil {
		return "", err
	}
	_, err := f.pool.Exec(ctx, `INSERT INTO sso_desktop_codes (code_hash, user_id, challenge) VALUES ($1, $2, $3)`, hashCode(code), userID, challenge)
	return code, err
}

// ExchangeDesktop troca o código pela sessão, se o verificador bater com o desafio do início.
func (f *Flow) ExchangeDesktop(ctx context.Context, code, verifier string) (session string, err error) {
	var userID, challenge string
	err = f.pool.QueryRow(ctx, `DELETE FROM sso_desktop_codes WHERE code_hash = $1 AND created_at > now() - interval '`+codeTTL+`'
		RETURNING user_id, challenge`, hashCode(code)).Scan(&userID, &challenge)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrCode
	}
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	if subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) != 1 {
		return "", ErrCode // o código foi interceptado por quem não tem o verificador
	}
	return f.users.CreateSession(ctx, userID, f.cfg.SessionTTL)
}
