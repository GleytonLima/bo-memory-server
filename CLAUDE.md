# bo-memory-server — convenções

- Contrato em `docs/API.md` é a fonte da verdade para servidor, web (bo-memory-web) e desktop
  (organizer). Mudou a API? Atualize o documento no mesmo commit.
- Módulos pequenos e focados: nada de arquivos gigantes; um pacote por responsabilidade
  (`auth`, `memory`, `api`, `db`, `web`, `config`).
- Esquema do banco só por migrações goose novas em `internal/db/migrations`; nunca editar uma
  já aplicada.
- Toolchain fixado no `go.mod` (`toolchain go1.27.2`): o `go` da máquina baixa essa versão sozinho
  (não use `GOTOOLCHAIN=local`). Ao subir, prefira o último patch estável e rode
  `govulncheck ./...` (instale com o mesmo toolchain): a biblioteca padrão recebe correções de segurança.
- Testes: `go test ./... -p 1` com `docker compose up -d` rodando.
- Mensagens de erro da API em português.
- Commit na main ok; push só com "pode pushar" explícito.
