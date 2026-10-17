package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type ctxKey struct{}

// Middleware exige "Authorization: Bearer <token>" e põe o usuário no contexto.
func Middleware(s *Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			deny(w, http.StatusUnauthorized, "Informe o token: Authorization: Bearer <token>.")
			return
		}
		user, err := s.UserByToken(r.Context(), strings.TrimSpace(token))
		if err != nil {
			deny(w, http.StatusUnauthorized, "Token inválido ou revogado.")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, user)))
	})
}

// UserFrom devolve o usuário autenticado do request.
func UserFrom(ctx context.Context) User {
	u, _ := ctx.Value(ctxKey{}).(User)
	return u
}

// RequireAdmin envolve um handler que só administradores podem usar.
func RequireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !UserFrom(r.Context()).IsAdmin {
			deny(w, http.StatusForbidden, "Apenas administradores.")
			return
		}
		next(w, r)
	}
}

func deny(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
