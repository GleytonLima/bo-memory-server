// bo-memory-server: servidor de sincronização da memória do Bash Organizer.
//
//	bo-memory-server [serve]                      aplica as migrações e sobe o HTTP (padrão)
//	bo-memory-server migrate up|down|reset|status gerencia o esquema do banco
//	bo-memory-server admin create --name N --email E    cria um admin e imprime o token dele
//	bo-memory-server token create --email E [--name N]  novo token para um usuário existente
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"bo-memory-server/internal/api"
	"bo-memory-server/internal/auth"
	"bo-memory-server/internal/config"
	"bo-memory-server/internal/db"
	"bo-memory-server/internal/memory"
	"bo-memory-server/internal/sso"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cfg := config.Load()
	cmd, args := "serve", os.Args[1:]
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	switch cmd {
	case "serve":
		must(db.Migrate(ctx, pool, "up"))
		users := auth.NewStore(pool)
		users.PersonalTokens = os.Getenv("BO_PERSONAL_TOKENS") != "false"
		var flow *sso.Flow
		if sc := sso.LoadConfig(); sc.Enabled() {
			flow, err = sso.New(ctx, sc, pool, users)
			must(err)
			log.Printf("SSO: %s (%s), sessões de %s", sc.ProviderName, sc.Issuer, sc.SessionTTL)
		}
		if !users.PersonalTokens {
			log.Printf("tokens pessoais desligados (BO_PERSONAL_TOKENS=false): só sessões SSO")
		}
		serve(ctx, cfg, api.Handler(users, memory.NewStore(pool), cfg.WebDir, flow))
	case "migrate":
		if len(args) == 0 {
			log.Fatal("uso: bo-memory-server migrate up|down|reset|status")
		}
		must(db.Migrate(ctx, pool, args[0]))
	case "admin", "token":
		must(db.Migrate(ctx, pool, "up"))
		must(issue(ctx, auth.NewStore(pool), cmd, args))
	default:
		log.Fatalf("comando desconhecido: %s", cmd)
	}
}

func serve(ctx context.Context, cfg config.Config, h http.Handler) {
	srv := &http.Server{Addr: cfg.Addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	log.Printf("bo-memory-server em http://localhost%s (web: %s)", cfg.Addr, cfg.WebDir)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

// issue: "admin create" cria um administrador; "token create" emite token para um usuário existente.
func issue(ctx context.Context, store *auth.Store, cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	name := fs.String("name", "", "nome do usuário (admin) ou do token")
	email := fs.String("email", "", "e-mail do usuário")
	if len(args) == 0 || args[0] != "create" {
		return fmt.Errorf("uso: bo-memory-server %s create --email E [--name N]", cmd)
	}
	_ = fs.Parse(args[1:])
	var user auth.User
	var err error
	if cmd == "admin" {
		user, err = store.CreateUser(ctx, *name, *email, true)
	} else {
		user, err = store.UserByEmail(ctx, *email)
	}
	if err != nil {
		return err
	}
	tokenName := "cli"
	if cmd == "token" && *name != "" {
		tokenName = *name
	}
	token, _, err := store.CreateToken(ctx, user.ID, tokenName)
	if err != nil {
		return err
	}
	fmt.Printf("Usuário: %s <%s>\nToken (guarde agora; não será mostrado de novo):\n%s\n", user.Name, user.Email, token)
	return nil
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
