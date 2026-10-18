// Package config lê a configuração do servidor de variáveis de ambiente.
package config

import (
	"errors"
	"os"
	"strings"
)

// devDatabase: o PostgreSQL do docker-compose (senha de desenvolvimento, sem TLS).
const devDatabase = "postgres://bo:bo@localhost:55432/bo_memory?sslmode=disable"

type Config struct {
	Addr        string // endereço HTTP (BO_ADDR)
	DatabaseURL string // conexão PostgreSQL (DATABASE_URL)
	WebDir      string // pasta do frontend bo-memory-web servida em / (BO_WEB_DIR)
}

func Load() Config {
	return Config{
		Addr:        env("BO_ADDR", ":8787"),
		DatabaseURL: env("DATABASE_URL", devDatabase),
		WebDir:      env("BO_WEB_DIR", "../bo-memory-web"),
	}
}

// Check recusa o banco de desenvolvimento quando o servidor está publicado em HTTPS (produção):
// lá, DATABASE_URL precisa ter senha própria (e sslmode=require). Fora disso, devolve um aviso
// para o log quando o padrão está em uso.
func (c Config) Check() (warning string, err error) {
	if c.DatabaseURL != devDatabase {
		return "", nil
	}
	if strings.HasPrefix(os.Getenv("BO_PUBLIC_URL"), "https://") {
		return "", errors.New("DATABASE_URL não definido: o banco padrão (bo:bo, sem TLS) é só para desenvolvimento")
	}
	return "usando o banco de desenvolvimento (bo:bo, sem TLS); em produção defina DATABASE_URL", nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
