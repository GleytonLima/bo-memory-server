# bo-memory-server — contrato da API e do sync (v1)

Fonte da verdade para os três repositórios: **bo-memory-server** (Go + PostgreSQL), **bo-memory-web**
(frontend servido pelo Go) e **organizer** (desktop Bash Organizer, cliente de sync).
Mudou o contrato? Atualize este arquivo primeiro.

## Conceitos

| Conceito | O que é |
| --- | --- |
| **Workspace** | Identidade de um projeto entre máquinas. Chave textual escolhida pelo time (ex.: `bash-organizer`). No desktop, cada projeto pode ter uma `syncKey`. |
| **Memória** | `note` (texto livre), `decision` (uma frase com o porquê, ≤ 280, sem quebra de linha) ou `progress` (estado de uma tarefa para outro dev retomar). |
| **Escopo** | `global` (todo o time), `project` (um workspace) ou `folder` (um repositório, em qualquer workspace que o use). |
| **Pasta (folder)** | Identificada pela **URL normalizada do remote git**, não pelo caminho (que muda entre máquinas). Ver *Normalização de remote*. |
| **Revisão (`rev`)** | Número crescente global atribuído pelo servidor a cada escrita. O cliente guarda o maior `rev` visto e puxa `since` ele. |
| **Tombstone** | Exclusão é `deleted: true` com novo `rev` — some da leitura, mas sincroniza. |

## Autenticação

- Todo request (exceto `GET /healthz` e os arquivos estáticos da web) usa `Authorization: Bearer <token>`.
- **Token por dev.** O servidor guarda só o hash (SHA-256). Um token pertence a um usuário; usuários `admin` gerenciam usuários e tokens.
- Primeiro acesso: `bo-memory-server admin create --name "Fulano" --email f@x` imprime um token de admin (uma única vez).
- Erros: `401` sem token/token inválido ou revogado; `403` sem permissão.

## Modelo de memória (JSON)

```json
{
  "id": "8d1c…-uuid",                 // gerado pelo CLIENTE (uuid v4) — permite criar offline
  "rev": 1532,                         // atribuído pelo servidor
  "kind": "decision",                  // note | decision | progress
  "scope": "project",                  // global | project | folder
  "workspace": "bash-organizer",       // obrigatório em project e progress; null em global
  "folder": "github.com/gleyton/bo-memory-server", // obrigatório em folder (remote normalizado)
  "text": "Usamos Postgres porque …",
  "tags": ["banco", "infra"],
  "taskKey": "BO-22",                  // opcional
  "supersededBy": null,                // id da decisão que substituiu esta
  "progress": null,                    // só em kind=progress (ver abaixo)
  "author": { "id": "uuid", "name": "Gleyton" },
  "createdAt": "2026-10-17T10:00:00Z",
  "updatedAt": "2026-10-17T10:05:00Z",
  "deleted": false
}
```

`progress` (kind = `progress`, um por tarefa e workspace, chave natural `workspace + taskKey`):

```json
{
  "status": "Em andamento — login feito, falta refresh token",
  "nextSteps": ["Implementar refresh", "Testar expiração"],
  "repo": "github.com/gleyton/bo-memory-server",
  "branch": "feat/refresh-token",
  "task": { "key": "BO-22", "title": "…", "description": "…" },
  "owner": { "id": "uuid", "name": "Gleyton" }     // quem está com a tarefa agora
}
```

Validações do servidor (iguais às do desktop): `decision` ≤ 280 caracteres e sem `\n`; `scope`/`kind`
válidos; `workspace` obrigatório em `project`/`progress`; `folder` obrigatório em `folder`.

## Sync (desktop ⇄ servidor)

### Pull — `POST /v1/sync/pull`

```json
{ "since": 1500, "workspaces": ["bash-organizer"], "folders": ["github.com/gleyton/bo-memory-server"] }
```

Resposta: memórias com `rev > since` que valem para o cliente — `global` + `project`/`progress` dos
`workspaces` + `folder` das `folders` — incluindo tombstones. Ordenadas por `rev`, máx. 500 por página.

```json
{ "items": [ … ], "rev": 1532, "more": false }
```

Se `more: true`, repetir com `since = rev`.

### Push — `POST /v1/sync/push`

Cada mudança local leva o `baseRev`: o `rev` daquela memória que o cliente tinha quando editou
(`0` para memória nova).

```json
{ "changes": [
  { "op": "upsert", "baseRev": 0,    "memory": { "id": "…", "kind": "note", … } },
  { "op": "upsert", "baseRev": 1490, "memory": { "id": "…", "text": "editado", … } },
  { "op": "delete", "baseRev": 1488, "id": "…" }
] }
```

Resultado por mudança, na mesma ordem:

```json
{ "results": [
  { "id": "…", "status": "ok", "rev": 1533 },
  { "id": "…", "status": "conflict", "server": { …memória atual no servidor… } },
  { "id": "…", "status": "error", "error": "Uma decisão deve caber numa frase…" }
] }
```

**Regra de conflito:** `baseRev` diferente do `rev` atual do servidor ⇒ `conflict` (alguém mudou antes).
Exceções sem conflito: memória nova (`baseRev: 0` e id inexistente); conteúdo idêntico ao do servidor.

### Resolução de conflito (desktop e web)

O cliente marca a memória como **em conflito**, mostra as duas versões e o usuário escolhe
**a minha**, **a do servidor** ou **edita uma mescla**. Para gravar a escolha, faz novo push com
`baseRev = server.rev`. Escolher "a do servidor" é só aceitar o item recebido (sem push).

### Ordem recomendada no cliente

1. push das mudanças locais pendentes → trata `ok` / `conflict` / `error`;
2. pull `since` o maior `rev` conhecido → aplica (servidor vence apenas onde não há mudança local pendente);
3. repetir a cada 30 s e logo após cada gravação local.

## API para a web (e scripts)

| Método | Rota | Descrição |
| --- | --- | --- |
| `GET` | `/healthz` | `{ "ok": true }` (sem auth) |
| `GET` | `/v1/me` | usuário do token |
| `GET` | `/v1/workspaces` | workspaces com contagem de memórias |
| `POST` | `/v1/workspaces` | `{ key, name }` cria (admin) |
| `GET` | `/v1/folders` | pastas (remotes normalizados) com memórias vivas: `{ items: [{ folder, memories }] }` |
| `GET` | `/v1/memories?workspace=&scope=&kind=&q=&folder=&limit=` | lista/busca (full-text, sem acentos); sem `workspace` = só globais |
| `POST` | `/v1/memories` | cria (corpo = memória sem `rev`; `id` opcional, o servidor gera se faltar) |
| `PUT` | `/v1/memories/{id}` | edita; corpo inclui `baseRev` → `409` + `{ server }` em conflito |
| `DELETE` | `/v1/memories/{id}?baseRev=` | tombstone; `409` em conflito |
| `POST` | `/v1/memories/{id}/supersede` | `{ text, … }` cria decisão nova e marca esta como substituída |
| `POST` | `/v1/progress/{workspace}/{taskKey}/claim` | "retomar": passa a ser o `owner` (registra no histórico) |
| `GET` | `/v1/memories/{id}/history` | versões anteriores (auditoria) |
| `GET/POST/DELETE` | `/v1/users`, `/v1/tokens` | administração (admin) |

Erros: `{ "error": "mensagem em português" }` com `400` validação, `401`, `403`, `404`, `409` conflito.

## Normalização de remote

`git@github.com:Gleyton/Repo.git`, `https://github.com/Gleyton/Repo.git` e
`ssh://git@github.com/gleyton/repo` ⇒ `github.com/gleyton/repo`
(sem protocolo, sem usuário, sem `.git`, `:` vira `/`, minúsculas). Repositório sem remote não
participa de memórias de escopo `folder` sincronizadas (fica só local).

## Mapeamento no desktop (organizer)

| Local (memory.db) | Servidor |
| --- | --- |
| `gid` (uuid, novo) | `id` |
| `rev`, `dirty`, `conflict` (novos) | controle de sync |
| `scope` project + `project_id` | `workspace` = `syncKey` do projeto |
| `scope` folder + `folder` (caminho) | `folder` = remote normalizado do repo nesse caminho |
| `kind` note/decision | idem; `progress` é novo |

Projetos sem `syncKey` e pastas sem remote continuam só locais.
