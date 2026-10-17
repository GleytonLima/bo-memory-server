// Package sso faz o login pelo provedor de identidade da empresa via OpenID Connect (código +
// PKCE, cliente confidencial no servidor): Microsoft Entra ID, Google Workspace, Okta, Keycloak…
// O servidor valida o ID token, cria/atualiza o usuário e devolve uma sessão própria que expira.
// Ver docs/SSO.md.
package sso

import (
	"os"
	"strings"
	"time"
)

type Config struct {
	Issuer       string // BO_OIDC_ISSUER — ex.: https://login.microsoftonline.com/<tenant>/v2.0
	ClientID     string // BO_OIDC_CLIENT_ID
	ClientSecret string // BO_OIDC_CLIENT_SECRET
	PublicURL    string // BO_PUBLIC_URL — endereço do servidor visto pelo navegador (redirect URI = PublicURL + /auth/callback)
	Scopes       []string
	EmailClaims  []string // claims tentadas, em ordem, para o e-mail
	EmployeeID   []string // claims tentadas para o employeeid
	ProviderName string   // rótulo do botão ("Microsoft")
	SessionTTL   time.Duration
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func list(key, fallback string) []string { return strings.Fields(strings.ReplaceAll(env(key, fallback), ",", " ")) }

func LoadConfig() Config {
	ttl, err := time.ParseDuration(env("BO_SESSION_TTL", "12h"))
	if err != nil || ttl <= 0 {
		ttl = 12 * time.Hour
	}
	return Config{
		Issuer:       strings.TrimRight(env("BO_OIDC_ISSUER", ""), "/"),
		ClientID:     env("BO_OIDC_CLIENT_ID", ""),
		ClientSecret: env("BO_OIDC_CLIENT_SECRET", ""),
		PublicURL:    strings.TrimRight(env("BO_PUBLIC_URL", "http://localhost:8787"), "/"),
		Scopes:       list("BO_OIDC_SCOPES", "openid profile email"),
		// Entra ID: "email" só vem se o usuário tiver e-mail; preferred_username/upn costumam ser o e-mail corporativo
		EmailClaims:  list("BO_OIDC_EMAIL_CLAIM", "email preferred_username upn"),
		EmployeeID:   list("BO_OIDC_EMPLOYEE_ID_CLAIM", "employeeid employee_id employeeId"),
		ProviderName: env("BO_OIDC_PROVIDER_NAME", "SSO"),
		SessionTTL:   ttl,
	}
}

// Enabled: há provedor configurado.
func (c Config) Enabled() bool { return c.Issuer != "" && c.ClientID != "" }

func (c Config) RedirectURL() string { return c.PublicURL + "/auth/callback" }
