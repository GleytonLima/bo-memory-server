// Package config lê a configuração do servidor de variáveis de ambiente.
package config

import "os"

type Config struct {
	Addr        string // endereço HTTP (BO_ADDR)
	DatabaseURL string // conexão PostgreSQL (DATABASE_URL)
	WebDir      string // pasta do frontend bo-memory-web servida em / (BO_WEB_DIR)
}

func Load() Config {
	return Config{
		Addr:        env("BO_ADDR", ":8787"),
		DatabaseURL: env("DATABASE_URL", "postgres://bo:bo@localhost:55432/bo_memory?sslmode=disable"),
		WebDir:      env("BO_WEB_DIR", "../bo-memory-web"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
