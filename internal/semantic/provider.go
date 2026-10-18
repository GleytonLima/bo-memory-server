package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"time"
)

const maxResponse = 64 << 20 // resposta do provedor (32 vetores grandes cabem com folga)

// Embedder transforma textos em vetores (mesma ordem).
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

type httpEmbedder struct {
	cfg    Config
	client *http.Client
}

// NewEmbedder: cliente HTTP do provedor configurado (Ollama, compatível com OpenAI ou Azure OpenAI).
func NewEmbedder(cfg Config) (Embedder, error) {
	if err := cfg.Check(); err != nil {
		return nil, err
	}
	return &httpEmbedder{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}, nil
}

func (e *httpEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body := map[string]any{"model": e.cfg.Model, "input": texts}
	endpoint, header := e.cfg.URL+"/embeddings", "Authorization"
	switch e.cfg.Provider {
	case "ollama":
		endpoint = e.cfg.URL + "/api/embed"
	case "azure":
		endpoint = fmt.Sprintf("%s/openai/deployments/%s/embeddings?api-version=%s", e.cfg.URL, url.PathEscape(e.cfg.Model), url.QueryEscape(e.cfg.APIVersion))
		header = "api-key"
		delete(body, "model")
	}
	var out struct {
		Embeddings [][]float32 `json:"embeddings"` // Ollama
		Data       []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"` // OpenAI / Azure
	}
	if err := e.post(ctx, endpoint, header, body, &out); err != nil {
		return nil, err
	}
	vectors := out.Embeddings
	if e.cfg.Provider != "ollama" {
		for _, d := range out.Data {
			vectors = append(vectors, d.Embedding)
		}
	}
	if len(vectors) != len(texts) || slices.ContainsFunc(vectors, func(v []float32) bool { return len(v) == 0 }) {
		return nil, errors.New("resposta de embeddings inesperada")
	}
	return vectors, nil
}

func (e *httpEmbedder) post(ctx context.Context, endpoint, header string, body any, out any) error {
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.cfg.Key != "" {
		value := e.cfg.Key
		if header == "Authorization" {
			value = "Bearer " + value
		}
		req.Header.Set(header, value)
	}
	res, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("provedor de embeddings: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxResponse))
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("provedor de embeddings: HTTP %d %.200s", res.StatusCode, data)
	}
	return json.Unmarshal(data, out)
}
