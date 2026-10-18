package config

import "testing"

// Banco de desenvolvimento: aviso em dev, recusado com o servidor publicado em HTTPS.
func TestCheckDevDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("BO_PUBLIC_URL", "http://localhost:8787")
	if w, err := Load().Check(); err != nil || w == "" {
		t.Fatalf("em dev: aviso, sem erro (%q, %v)", w, err)
	}
	t.Setenv("BO_PUBLIC_URL", "https://memoria.empresa.com")
	if _, err := Load().Check(); err == nil {
		t.Fatal("em produção (https) o banco padrão deveria ser recusado")
	}
	t.Setenv("DATABASE_URL", "postgres://app:segredo@db:5432/bo?sslmode=require")
	if w, err := Load().Check(); err != nil || w != "" {
		t.Fatalf("com DATABASE_URL próprio: nada (%q, %v)", w, err)
	}
}
