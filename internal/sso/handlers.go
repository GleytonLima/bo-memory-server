package sso

import (
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"bo-memory-server/internal/auth"
)

const stateCookie = "bo_sso_state"

// Login: /auth/login?return=/caminho → redireciona ao provedor. Um cookie (só HTTP) amarra o login a
// este navegador: o callback recusa um state iniciado em outro (login CSRF / fixação de sessão).
func (f *Flow) Login(w http.ResponseWriter, r *http.Request) {
	target, state, err := f.Start(r.Context(), r.URL.Query().Get("return"))
	if err != nil {
		log.Printf("SSO login: %v", err)
		http.Redirect(w, r, "/#sso-error="+url.QueryEscape("Não foi possível iniciar o login."), http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: StateHash(state), Path: "/auth/", MaxAge: 600,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: strings.HasPrefix(f.cfg.PublicURL, "https://"),
	})
	http.Redirect(w, r, target, http.StatusFound)
}

// userMessage: só os erros pensados para a pessoa vão para a tela; o resto fica no log.
func userMessage(err error) string {
	for _, known := range []error{ErrExpired, ErrUnverified, ErrBrowser, auth.ErrDisabled, auth.ErrNoEmail} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	return "Não foi possível concluir o login."
}

// Callback: o provedor volta aqui com ?code&state. A sessão vai para a web no fragmento da URL
// (#sso=…), que não chega a servidores nem a logs de acesso.
func (f *Flow) Callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		log.Printf("SSO callback: o provedor devolveu %s: %s", e, q.Get("error_description"))
		http.Redirect(w, r, "/#sso-error="+url.QueryEscape("O provedor não concluiu o login."), http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: "", Path: "/auth/", MaxAge: -1, HttpOnly: true})
	c, cerr := r.Cookie(stateCookie)
	var session, returnTo string
	err := ErrBrowser
	if cerr == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(StateHash(q.Get("state")))) == 1 {
		session, returnTo, err = f.Finish(r.Context(), q.Get("state"), q.Get("code"))
	}
	if err != nil {
		log.Printf("SSO callback: %v", err)
		http.Redirect(w, r, "/#sso-error="+url.QueryEscape(userMessage(err)), http.StatusFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, returnTo+"#sso="+url.QueryEscape(session), http.StatusFound)
}
