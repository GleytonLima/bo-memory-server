package sso

import (
	"log"
	"net/http"
	"net/url"
)

// Login: /auth/login?return=/caminho → redireciona ao provedor.
func (f *Flow) Login(w http.ResponseWriter, r *http.Request) {
	target, err := f.Start(r.Context(), r.URL.Query().Get("return"))
	if err != nil {
		log.Printf("SSO login: %v", err)
		http.Redirect(w, r, "/#sso-error="+url.QueryEscape("Não foi possível iniciar o login."), http.StatusFound)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// Callback: o provedor volta aqui com ?code&state. A sessão vai para a web no fragmento da URL
// (#sso=…), que não chega a servidores nem a logs de acesso.
func (f *Flow) Callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		msg := q.Get("error_description")
		if msg == "" {
			msg = e
		}
		http.Redirect(w, r, "/#sso-error="+url.QueryEscape(msg), http.StatusFound)
		return
	}
	session, returnTo, err := f.Finish(r.Context(), q.Get("state"), q.Get("code"))
	if err != nil {
		log.Printf("SSO callback: %v", err)
		http.Redirect(w, r, "/#sso-error="+url.QueryEscape(err.Error()), http.StatusFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, returnTo+"#sso="+url.QueryEscape(session), http.StatusFound)
}
