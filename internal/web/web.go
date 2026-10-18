// Package web serve o frontend bo-memory-web (arquivos estáticos) a partir de BO_WEB_DIR.
// Rotas sem arquivo correspondente caem no index.html (navegação do lado do cliente).
// Cache-Control: no-cache faz o navegador revalidar (304 se nada mudou): uma versão nova da web
// chega a todos sem precisar limpar o cache.
package web

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// public: só os arquivos da interface. O resto da pasta (.git/, test/, CLAUDE.md…) não é servido.
func public(rel string) bool {
	if strings.Contains(rel, "/.") {
		return false
	}
	return rel == "/index.html" || rel == "/favicon.svg" || strings.HasPrefix(rel, "/js/") || strings.HasPrefix(rel, "/styles/")
}

func Handler(dir string) http.Handler {
	index := filepath.Join(dir, "index.html")
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := os.Stat(index); err != nil {
			http.Error(w, "Frontend não encontrado: defina BO_WEB_DIR com a pasta do bo-memory-web.", http.StatusNotFound)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		rel := path.Clean("/" + r.URL.Path)
		clean := filepath.Join(dir, filepath.FromSlash(strings.TrimPrefix(rel, "/")))
		if info, err := os.Stat(clean); err == nil && !info.IsDir() && public(rel) {
			files.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, index)
	})
}
