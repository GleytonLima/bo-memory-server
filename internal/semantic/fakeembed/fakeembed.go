// Package fakeembed: provedor de embeddings falso para os testes (só arquivos _test o importam).
// Fala os três formatos (Ollama /api/embed, OpenAI /embeddings, Azure /openai/deployments/…) e
// gera vetores por "conceito": palavras do mesmo grupo caem na mesma dimensão, então "banco de
// dados" fica perto de "PostgreSQL" sem nenhuma palavra em comum. Mesmo esquema de
// bo-memory-web/test/lib/fake-embed.js.
package fakeembed

import (
	"encoding/json"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"
)

var concepts = [][]string{
	{"banco", "postgres", "postgresql", "sql", "database", "dados", "pgvector"},
	{"login", "autenticação", "autenticacao", "sso", "senha", "oidc", "entrar"},
	{"teste", "testes", "playwright", "e2e", "cobertura"},
	{"cor", "cores", "tema", "visual", "design", "paleta"},
}

var word = regexp.MustCompile(`[\p{L}\p{N}]+`)

const hashed = 4096 // demais palavras (3+ letras): uma dimensão cada, por hash — só a mesma palavra aproxima

// Vector: uma dimensão por conceito + as palavras soltas por hash. Sem palavras, o vetor nulo
// (parecido com nada).
func Vector(text string) []float32 {
	v := make([]float32, len(concepts)+hashed)
	for _, w := range word.FindAllString(strings.ToLower(text), -1) {
		if i := concept(w); i >= 0 {
			v[i]++
		} else if utf8.RuneCountInString(w) >= 3 {
			h := fnv.New32a()
			h.Write([]byte(w))
			v[len(concepts)+int(h.Sum32()%hashed)]++
		}
	}
	return v
}

func concept(w string) int {
	for i, group := range concepts {
		if slices.Contains(group, w) {
			return i
		}
	}
	return -1
}

type Server struct {
	*httptest.Server
	Calls  atomic.Int64 // pedidos recebidos
	Fail   atomic.Bool  // responde 500 (provedor fora do ar)
	Header atomic.Value // último cabeçalho de autenticação (Authorization ou api-key)
}

// Start sobe o provedor falso (fechado no fim do teste).
func Start(t *testing.T) *Server {
	t.Helper()
	s := &Server{}
	s.Server = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.Close)
	return s
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.Calls.Add(1)
	s.Header.Store(r.Header.Get("Authorization") + r.Header.Get("api-key"))
	if s.Fail.Load() {
		http.Error(w, "fora do ar", http.StatusInternalServerError)
		return
	}
	var in struct{ Input []string }
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	vectors := make([][]float32, len(in.Input))
	for i, text := range in.Input {
		vectors[i] = Vector(text)
	}
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/api/embed" {
		_ = json.NewEncoder(w).Encode(map[string]any{"embeddings": vectors})
		return
	}
	data := make([]map[string]any, len(vectors))
	for i, v := range vectors {
		data[i] = map[string]any{"embedding": v, "index": i}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}
