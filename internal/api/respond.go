// Package api expõe as rotas HTTP descritas em docs/API.md.
package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"bo-memory-server/internal/memory"
)

const maxBody = 4 << 20 // 4 MiB por request

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// fail escolhe o status a partir do erro: validação 400, não encontrado 404, o resto 500 (logado).
func fail(w http.ResponseWriter, err error) {
	switch {
	case memory.IsValidation(err):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, memory.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		log.Printf("erro interno: %v", err)
		writeError(w, http.StatusInternalServerError, "Erro interno no servidor.")
	}
}

// decode lê o corpo JSON; em erro já responde 400 e devolve false.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "JSON inválido: "+err.Error())
		return false
	}
	return true
}

// writeResult traduz o resultado de uma escrita: ok 200, conflito 409 (com a versão do servidor), erro 400.
func writeResult(w http.ResponseWriter, res memory.Result) {
	switch res.Status {
	case "ok":
		writeJSON(w, http.StatusOK, res)
	case "conflict":
		writeJSON(w, http.StatusConflict, res)
	default:
		writeError(w, http.StatusBadRequest, res.Error)
	}
}
