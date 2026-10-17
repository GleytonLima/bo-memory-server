package sso

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

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

func New(ctx context.Context, cfg Config, pool *pgxpool.Pool, users *auth.Store) (*Flow, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
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

// safeReturn só aceita caminhos deste servidor (evita redirecionar para outro site).
func safeReturn(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.Contains(p, "\\") {
		return "/"
	}
	return p
}

// Start guarda o estado do login e devolve a URL do provedor (código + PKCE + nonce).
func (f *Flow) Start(ctx context.Context, returnTo string) (string, error) {
	state, nonce, verifier := random(), random(), oauth2.GenerateVerifier()
	if _, err := f.pool.Exec(ctx, `DELETE FROM sso_states WHERE created_at < now() - interval '`+stateTTL+`'`); err != nil {
		return "", err
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO sso_states (state, verifier, nonce, return_to) VALUES ($1, $2, $3, $4)`, state, verifier, nonce, safeReturn(returnTo)); err != nil {
		return "", err
	}
	return f.oauth.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), nil
}

// Finish troca o código, valida o ID token e devolve a sessão criada e o caminho de volta.
func (f *Flow) Finish(ctx context.Context, state, code string) (session, returnTo string, err error) {
	var verifier, nonce string
	err = f.pool.QueryRow(ctx, `DELETE FROM sso_states WHERE state = $1 AND created_at > now() - interval '`+stateTTL+`' RETURNING verifier, nonce, return_to`, state).
		Scan(&verifier, &nonce, &returnTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", errors.New("login expirado ou inválido; tente de novo")
	}
	if err != nil {
		return "", "", err
	}
	tok, err := f.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
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
