package sso

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"bo-memory-server/internal/auth"
)

const stateTTL = "10 minutes"

// Flow: o provedor descoberto (/.well-known/openid-configuration), o cliente OAuth2 e o verificador
// de ID token (assinatura pelo JWKS, emissor, audiência = client id, validade).
type Flow struct {
	cfg      Config
	pool     *pgxpool.Pool
	users    *auth.Store
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// idpClient: toda chamada ao provedor (descoberta, chaves, troca do código) com tempo limite.
var idpClient = &http.Client{Timeout: 10 * time.Second}

func New(ctx context.Context, cfg Config, pool *pgxpool.Pool, users *auth.Store) (*Flow, error) {
	// o mesmo cliente segue para a busca das chaves (go-oidc guarda o do contexto do NewProvider)
	provider, err := oidc.NewProvider(oidc.ClientContext(context.WithoutCancel(ctx), idpClient), cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("SSO: não foi possível ler o provedor %s: %w", cfg.Issuer, err)
	}
	return &Flow{
		cfg:      cfg,
		pool:     pool,
		users:    users,
		oauth:    oauth2.Config{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, Endpoint: provider.Endpoint(), RedirectURL: cfg.RedirectURL(), Scopes: cfg.Scopes},
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
	}, nil
}

func random() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// returnPath: só caminhos simples deste servidor. Tab, quebra de linha, "//" ou "\" viram outro site
// no navegador (/\t/evil.com → //evil.com): o redirecionamento levaria a sessão junto (#sso=…).
var returnPath = regexp.MustCompile(`^/[A-Za-z0-9._~/-]*$`)

func safeReturn(p string) string {
	if !returnPath.MatchString(p) || strings.Contains(p, "//") {
		return "/"
	}
	return p
}

// Erros que podem aparecer para a pessoa (os demais viram uma mensagem genérica e vão só para o log).
var (
	ErrExpired    = errors.New("login expirado ou inválido: tente de novo")
	ErrUnverified = errors.New("o provedor não confirmou este e-mail (email_verified): fale com o administrador")
	ErrBrowser    = errors.New("o login foi iniciado em outro navegador: tente de novo")
)

// StateHash: o que vai no cookie do navegador que iniciou o login (amarra o state a ele).
func StateHash(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

// Start guarda o estado do login e devolve a URL do provedor (código + PKCE + nonce) e o state.
func (f *Flow) Start(ctx context.Context, returnTo string) (target, state string, err error) {
	state, nonce, verifier := random(), random(), oauth2.GenerateVerifier()
	if _, err := f.pool.Exec(ctx, `DELETE FROM sso_states WHERE created_at < now() - interval '`+stateTTL+`'`); err != nil {
		return "", "", err
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO sso_states (state, verifier, nonce, return_to) VALUES ($1, $2, $3, $4)`, state, verifier, nonce, safeReturn(returnTo)); err != nil {
		return "", "", err
	}
	return f.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), state, nil
}

// Finish troca o código, valida o ID token e devolve a sessão criada e o caminho de volta.
func (f *Flow) Finish(ctx context.Context, state, code string) (session, returnTo string, err error) {
	var verifier, nonce string
	err = f.pool.QueryRow(ctx, `DELETE FROM sso_states WHERE state = $1 AND created_at > now() - interval '`+stateTTL+`' RETURNING verifier, nonce, return_to`, state).
		Scan(&verifier, &nonce, &returnTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrExpired
	}
	if err != nil {
		return "", "", err
	}
	tok, err := f.oauth.Exchange(context.WithValue(ctx, oauth2.HTTPClient, idpClient), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", "", fmt.Errorf("o provedor recusou o código: %w", err)
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return "", "", errors.New("o provedor não devolveu um ID token (inclua o escopo openid)")
	}
	idt, err := f.verifier.Verify(ctx, raw)
	if err != nil {
		return "", "", fmt.Errorf("ID token inválido: %w", err)
	}
	if idt.Nonce != nonce {
		return "", "", errors.New("ID token com nonce diferente do login")
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		return "", "", err
	}
	// e-mail não verificado não identifica ninguém (nem dá acesso a "*@dominio" ou à conta com esse e-mail)
	if v, ok := claims["email_verified"]; ok && v != true && v != "true" {
		return "", "", ErrUnverified
	}
	user, err := f.users.UpsertSSOUser(ctx, f.identity(idt.Issuer, idt.Subject, claims))
	if err != nil {
		return "", "", err
	}
	session, err = f.users.CreateSession(ctx, user.ID, f.cfg.SessionTTL)
	return session, returnTo, err
}

// identity lê e-mail, nome e employeeid das claims configuradas (a primeira presente vale).
func (f *Flow) identity(issuer, subject string, claims map[string]any) auth.Identity {
	first := func(names []string) string {
		for _, n := range names {
			if v, ok := claims[n].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	return auth.Identity{Issuer: issuer, Subject: subject, Email: first(f.cfg.EmailClaims), Name: first([]string{"name", "given_name"}), EmployeeID: first(f.cfg.EmployeeID)}
}

func (f *Flow) ProviderName() string { return f.cfg.ProviderName }
