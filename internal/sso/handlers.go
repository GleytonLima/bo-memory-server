package sso

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strconv"
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
	f.setStateCookie(w, state)
	http.Redirect(w, r, target, http.StatusFound)
}

func (f *Flow) setStateCookie(w http.ResponseWriter, state string) {
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: StateHash(state), Path: "/auth/", MaxAge: 600,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: strings.HasPrefix(f.cfg.PublicURL, "https://"),
	})
}

// userMessage: só os erros pensados para a pessoa vão para a tela; o resto fica no log.
func userMessage(err error) string {
	for _, known := range []error{ErrExpired, ErrUnverified, ErrBrowser, ErrDesktopArg, auth.ErrDisabled, auth.ErrNoEmail} {
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
	var user auth.User
	var returnTo string
	err := ErrBrowser
	if cerr == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(StateHash(q.Get("state")))) == 1 {
		user, returnTo, err = f.finishUser(r.Context(), q.Get("state"), q.Get("code"))
	}
	if err != nil {
		log.Printf("SSO callback: %v", err)
		http.Redirect(w, r, "/#sso-error="+url.QueryEscape(userMessage(err)), http.StatusFound)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if port, challenge, ok := parseDesktop(returnTo); ok {
		f.toDesktop(w, r, user, port, challenge)
		return
	}
	session, err := f.users.CreateSession(r.Context(), user.ID, f.cfg.SessionTTL)
	if err != nil {
		log.Printf("SSO callback: %v", err)
		http.Redirect(w, r, "/#sso-error="+url.QueryEscape(userMessage(err)), http.StatusFound)
		return
	}
	http.Redirect(w, r, returnTo+"#sso="+url.QueryEscape(session), http.StatusFound)
}

// toDesktop: o navegador leva ao app (127.0.0.1) um código de uso único — nunca a sessão.
func (f *Flow) toDesktop(w http.ResponseWriter, r *http.Request, user auth.User, port int, challenge string) {
	code, err := f.desktopCode(r.Context(), user.ID, challenge)
	if err != nil {
		log.Printf("SSO desktop: %v", err)
		http.Redirect(w, r, loopback(port)+"?error="+url.QueryEscape(userMessage(err)), http.StatusFound)
		return
	}
	http.Redirect(w, r, loopback(port)+"?code="+url.QueryEscape(code), http.StatusFound)
}

// DesktopLogin: /auth/desktop?port=&challenge= — como /auth/login, para o app desktop (desktop.go).
func (f *Flow) DesktopLogin(w http.ResponseWriter, r *http.Request) {
	port, _ := strconv.Atoi(r.URL.Query().Get("port"))
	target, state, err := f.StartDesktop(r.Context(), port, r.URL.Query().Get("challenge"))
	if err != nil {
		if !errors.Is(err, ErrDesktopArg) {
			log.Printf("SSO desktop: %v", err)
		}
		http.Redirect(w, r, "/#sso-error="+url.QueryEscape(userMessage(err)), http.StatusFound)
		return
	}
	f.setStateCookie(w, state)
	http.Redirect(w, r, target, http.StatusFound)
}

// DesktopToken: POST /auth/desktop/token { code, verifier } → { session }.
func (f *Flow) DesktopToken(w http.ResponseWriter, r *http.Request) {
	var body struct{ Code, Verifier string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON inválido."})
		return
	}
	session, err := f.ExchangeDesktop(r.Context(), body.Code, body.Verifier)
	if errors.Is(err, ErrCode) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": ErrCode.Error()})
		return
	}
	if err != nil {
		log.Printf("SSO desktop: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Erro interno no servidor."})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"session": session})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
