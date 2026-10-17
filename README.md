# bo-memory-server

Servidor de sincronização da memória do [Bash Organizer](../organizer): decisões, notas e
progresso de tarefas compartilhados entre máquinas e devs. Go + PostgreSQL. O contrato
(rotas, protocolo de sync, conflitos) está em [docs/API.md](docs/API.md).

## Rodando

```bash
docker compose up -d                       # PostgreSQL 16 na porta 55432 (BO_DB_PORT)
go run ./cmd/bo-memory-server admin create --name "Seu Nome" --email voce@empresa.com
go run ./cmd/bo-memory-server              # aplica as migrações e sobe em :8787
```

O token impresso pelo `admin create` só aparece uma vez. Outros devs recebem token por
`POST /v1/tokens` (admin) ou `bo-memory-server token create --email ...`.

| Variável       | Padrão                                                      |
|----------------|-------------------------------------------------------------|
| `BO_ADDR`      | `:8787`                                                     |
| `DATABASE_URL` | `postgres://bo:bo@localhost:55432/bo_memory?sslmode=disable` |
| `BO_WEB_DIR`   | `../bo-memory-web` (frontend servido em `/`)                |

**SSO (Entra ID, Google, Okta, Keycloak…):** login pelo provedor da empresa via OpenID Connect,
usuário por e-mail e employeeid e membros por projeto — passo a passo em [docs/SSO.md](docs/SSO.md).

## Banco e migrações

O esquema evolui só por migrações SQL versionadas (goose) em `internal/db/migrations`,
embutidas no binário. `serve` aplica as pendentes ao subir; para operar à mão:

```bash
go run ./cmd/bo-memory-server migrate status   # up | down | reset | status
```

Nova mudança de esquema = novo arquivo `000NN_descricao.sql` com blocos `-- +goose Up` e
`-- +goose Down`. Nunca edite uma migração já aplicada.

## Testes

```bash
go test ./... -p 1
```

Os testes de integração usam o banco `bo_memory_test` do compose (ou `TEST_DATABASE_URL`),
recriando o esquema pelas migrações a cada teste; sem PostgreSQL eles são pulados.
