// Package semantic: busca por significado (opcional). Um provedor de embeddings transforma o texto
// das memórias em vetores, guardados no PostgreSQL com pgvector (tabela memory_embeddings, criada na
// subida se a extensão existir); a busca da web funde o ranking por texto com o por similaridade.
// Desligada por padrão: BO_EMBED_PROVIDER=ollama | openai | azure liga (docs/SEMANTIC.md).
package semantic

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Provider      string  // off | ollama | openai | azure
	URL           string  // base do provedor
	Model         string  // modelo (no Azure, o nome da implantação)
	Key           string  // chave de API (openai, azure); nunca vai para o log
	APIVersion    string  // Azure OpenAI
	MinSimilarity float64 // abaixo disso, "parecido" não entra no resultado
}

var defaults = map[string]Config{
	"ollama": {URL: "http://127.0.0.1:11434", Model: "nomic-embed-text"},
	"openai": {URL: "https://api.openai.com/v1", Model: "text-embedding-3-small"},
	"azure":  {APIVersion: "2024-10-21"},
}

// LoadConfig lê BO_EMBED_* (provedor, URL, modelo, chave, versão da API, similaridade mínima).
func LoadConfig() Config {
	c := Config{
		Provider:   strings.ToLower(strings.TrimSpace(os.Getenv("BO_EMBED_PROVIDER"))),
		URL:        strings.TrimRight(os.Getenv("BO_EMBED_URL"), "/"),
		Model:      os.Getenv("BO_EMBED_MODEL"),
		Key:        os.Getenv("BO_EMBED_KEY"),
		APIVersion: os.Getenv("BO_EMBED_API_VERSION"),
	}
	if c.Provider == "" {
		c.Provider = "off"
	}
	d := defaults[c.Provider]
	c.URL = or(c.URL, d.URL)
	c.Model = or(c.Model, d.Model)
	c.APIVersion = or(c.APIVersion, d.APIVersion)
	c.MinSimilarity = 0.3
	if v, err := strconv.ParseFloat(os.Getenv("BO_EMBED_MIN_SIMILARITY"), 64); err == nil && v >= 0 && v < 1 {
		c.MinSimilarity = v
	}
	return c
}

func (c Config) Enabled() bool { return c.Provider != "off" }

// Check: provedor conhecido e o que ele exige preenchido.
func (c Config) Check() error {
	if _, ok := defaults[c.Provider]; !ok {
		return fmt.Errorf("BO_EMBED_PROVIDER desconhecido: %q (use ollama, openai ou azure)", c.Provider)
	}
	if c.URL == "" || c.Model == "" {
		return fmt.Errorf("embeddings %s: defina BO_EMBED_URL e BO_EMBED_MODEL", c.Provider)
	}
	if c.Provider == "azure" && c.Key == "" {
		return fmt.Errorf("embeddings azure: defina BO_EMBED_KEY")
	}
	return nil
}

func or(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
