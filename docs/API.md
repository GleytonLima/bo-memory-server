# bo-memory-server — contrato da API e do sync (v1)

Fonte da verdade para os três repositórios: **bo-memory-server** (Go + PostgreSQL), **bo-memory-web**
(frontend servido pelo Go) e **organizer** (desktop Bash Organizer, cliente de sync).
Mudou o contrato? Atualize este arquivo primeiro.

## Conceitos

| Conceito | O que é |
| --- | --- |
| **Workspace** | Identidade de um projeto entre máquinas. Chave textual escolhida pelo time (ex.: `bash-organizer`). No desktop, cada projeto pode ter uma `syncKey`. |
| **Memória** | `note` (texto livre), `decision` (uma frase com o porquê, ≤ 280, sem quebra de linha), `idea` (ideia que surgiu em paralelo), `howto` ("como fazer": como resolvemos, o procedimento, a quem recorrer) ou `progress` (estado de uma tarefa para outro dev retomar). |
| **Escopo** | `global` (todo o time), `project` (um workspace) ou `folder` (um repositório, em qualquer workspace que o use). |
| **Pasta (folder)** | Identificada pela **URL normalizada do remote git**, não pelo caminho (que muda entre máquinas). Ver *Normalização de remote*. |
| **Revisão (`rev`)** | Número crescente global atribuído pelo servidor a cada escrita. O cliente guarda o maior `rev` visto e puxa `since` ele. |
| **Tombstone** | Exclusão é `deleted: true` com novo `rev` — some da leitura, mas sincroniza. |

## Autenticação

- **SSO (OpenID Connect)** — Entra ID, Google Workspace, Okta, Keycloak… Configuração em
  [SSO.md](SSO.md). O servidor faz o login (`GET /auth/login?return=/` → provedor →
  `GET /auth/callback`) e devolve à web uma **sessão** (`bos_…`, expira; padrão 12 h) no fragmento
  da URL: `/#sso=<sessão>` (ou `/#sso-error=<mensagem>`). `GET /auth/config` (sem auth) diz
  `{ sso, provider, personalTokens }`; `POST /v1/logout` revoga o token em uso.
- **Tokens pessoais** (`bom_…`, sem validade): continuam para scripts, CI e o desktop.
  `BO_PERSONAL_TOKENS=false` faz o servidor aceitar só sessões SSO.
- **Membros por projeto**: cada workspace pode ter uma lista de e-mails ou domínios
  (`*@empresa.com`). Com lista, só membros (e admins) leem, sincronizam e escrevem nele; sem
  lista, é aberto a todos. No pull, workspaces de que você não é membro são ignorados; no push,
  a mudança volta como `error`; nas rotas da web, `403`. Globais e pastas não mudam.
- Todo request (exceto `GET /healthz` e os arquivos estáticos da web) usa `Authorization: Bearer <token>`.
- **Token por dev.** O servidor guarda só o hash (SHA-256). Um token pertence a um usuário; usuários `admin` gerenciam usuários e tokens.
  Emitido pela API/web (`POST /v1/tokens`, corpo `{ name, userId?, expiresInDays? }`), todo token vence: `expiresInDays` de 1 a 365, padrão `BO_TOKEN_TTL_DAYS` (90). Só o CLI do operador emite sem validade (o 1º admin).
- Primeiro acesso: `bo-memory-server admin create --name "Fulano" --email f@x` imprime um token de admin (uma única vez).
- Erros: `401` sem token/token inválido ou revogado; `403` sem permissão.

## Modelo de memória (JSON)

```json
{
  "id": "8d1c…-uuid",                 // gerado pelo CLIENTE (uuid v4) — permite criar offline
  "rev": 1532,                         // atribuído pelo servidor
  "kind": "decision",                  // note | decision | progress | idea | howto
  "scope": "project",                  // global | project | folder
  "workspace": "bash-organizer",       // obrigatório em project e progress; null em global
  "folder": "github.com/gleyton/bo-memory-server", // obrigatório em folder (remote normalizado)
  "text": "Usamos Postgres porque …",
  "tags": ["banco", "infra"],
  "taskKey": "BO-22",                  // opcional
  "supersededBy": null,                // id da decisão que substituiu esta
  "refs": [{ "id": "uuid", "type": "depends" }], // vínculos (ver "Vínculos e impacto")
  "review": null,                      // marca "a revisar" (ver "Vínculos e impacto")
  "status": null,                      // idea: open | converted (virou tarefa; taskKey = a tarefa) | discarded;
                                       // progress: null/open | done (tarefa concluída: arquivado, fora da busca padrão);
                                       // os outros tipos: sempre null
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

## Vínculos e impacto

Uma memória aponta para outras em `refs` (até 30), com um tipo:

| `type` | Leitura | Impacto |
| --- | --- | --- |
| `depends` | "apoia-se em" — esta vale *porque* a outra vale | sim: se a outra mudar, esta precisa ser revista |
| `related` | "relacionada a" | não |
| `contradicts` | "contradiz" (ex.: alternativa descartada, exceção) | não |

`refs` faz parte do conteúdo: sincroniza, vai para o histórico e entra na regra de conflito.
Auto-referência é descartada; ids repetidos ficam com o último tipo.

**Cadeia de impacto** de X = todas as memórias que dependem de X, direta ou indiretamente (segue só
`depends`, até 10 níveis, tolera ciclos). Ao editar ou substituir X, o cliente mostra a cadeia e o
usuário escolhe quais marcar **"a revisar"**:

```json
"review": { "reason": "Mudou: <texto novo de X>", "source": "<id de X>", "by": { "id": "…", "name": "Ana" }, "at": "…" }
```

A marca some quando alguém revisa a memória: edita (o cliente grava `review: null`) ou confirma
sem mudanças (mesma coisa, só com `review: null`). Desktop e web marcam do mesmo jeito: o desktop
grava a marca localmente e ela sobe no push; a web usa `POST /v1/memories/review`.

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

## Projeto e layout (trocar de máquina)

Para abrir um projeto numa máquina nova sem refazê-lo à mão, o servidor guarda dois documentos
por workspace, cada um com `version` (controle otimista, como o `rev` das memórias):

| Documento | De quem | Conteúdo |
| --- | --- | --- |
| **Projeto** | do workspace (todo o time) | `name`, `folders` (pastas pelo remote), `columns` (colunas do quadro) |
| **Layout** | de cada usuário, por workspace | `groups` com seus `terminals` |

```json
// GET /v1/workspaces/{key}/project
{
  "workspace": "bash-organizer",
  "name": "Bash Organizer",
  "folders": [{ "name": "organizer", "remote": "github.com/gleyton/organizer",
                "url": "https://github.com/Gleyton/organizer.git" }],   // url: para clonar (sem usuário/senha)
  "columns": [{ "id": "c1", "name": "A fazer", "description": "…" }],
  "version": 3, "updatedAt": "…", "updatedBy": { "id": "uuid", "name": "Gleyton" }
}
// GET /v1/workspaces/{key}/layout   (só o seu)
{
  "workspace": "bash-organizer",
  "groups": [{ "name": "Agente", "terminals": [
    { "name": "devtools", "folder": "github.com/gleyton/organizer", "subdir": "", "startup": "", "color": null }
  ] }],
  "version": 7, "updatedAt": "…"
}
```

- **Gravar:** `PUT` no mesmo caminho com o documento e `baseVersion` (a `version` que você tinha;
  `0` = ainda não existe) → `200` com o documento gravado e a nova `version`, ou `409` + `{ server }`
  se alguém gravou antes. Conteúdo idêntico ao do servidor não conta como conflito. O `PUT` do
  projeto cria o workspace se ele não existir (como o push).
- **Ler:** `404` se ainda não há documento. Workspace restrito: só membros (e admins), `403` para os outros.
- **Pastas** são identificadas pelo remote normalizado (ver *Normalização de remote*); o servidor
  normaliza `remote`, recusa `url` com usuário/senha e limita a 100 pastas, 50 colunas, 50 grupos
  e 200 terminais. O terminal aponta a pasta pelo remote (`folder`, vazio = a primeira pasta) mais
  uma `subdir` relativa (sem `..`).
- **Comandos iniciais** (`startup`) só existem no layout pessoal — o de um colega nunca chega a
  você. Mesmo assim, o desktop mostra os comandos e só os cria depois que você confirma.
- `GET /v1/workspaces` diz, em cada item, se há projeto (`hasProject`) — é a lista "Abrir projeto
  do servidor" da máquina nova.

## API para a web (e scripts)

| Método | Rota | Descrição |
| --- | --- | --- |
| `GET` | `/healthz` | `{ "ok": true }` (sem auth) |
| `GET` | `/v1/me` | usuário do token |
| `GET` | `/v1/workspaces` | workspaces com contagem de memórias |
| `POST` | `/v1/workspaces` | `{ key, name }` cria (admin) |
| `GET` | `/v1/folders` | pastas (remotes normalizados) com memórias vivas: `{ items: [{ folder, memories }] }` |
| `GET` | `/v1/memories?workspace=&scope=&kind=&q=&folder=&review=&limit=` | lista/busca → `{ items, mode?, warning? }`: full-text sem acentos; com busca semântica ligada, `mode: "hybrid"` (texto + significado) e itens achados só pelo significado com `match: "semantic"`; provedor falhou → `mode: "text"` + `warning` ([SEMANTIC.md](SEMANTIC.md)). Sem `workspace` = só globais; ideias fechadas só com `kind=idea` e progresso de tarefa concluída só com `kind=progress`; `review=true` = só as "a revisar" |
| `POST` | `/v1/memories` | cria (corpo = memória sem `rev`; `id` opcional, o servidor gera se faltar) |
| `PUT` | `/v1/memories/{id}` | edita; corpo inclui `baseRev` → `409` + `{ server }` em conflito |
| `DELETE` | `/v1/memories/{id}?baseRev=` | tombstone; `409` em conflito |
| `POST` | `/v1/memories/{id}/supersede` | `{ text, … }` cria decisão nova e marca esta como substituída |
| `POST` | `/v1/progress/{workspace}/{taskKey}/claim` | "retomar": passa a ser o `owner` (registra no histórico) |
| `GET` | `/v1/memories/{id}/history` | versões anteriores (auditoria) |
| `GET` | `/v1/memories/{id}/links` | `{ outgoing, incoming, impact }`: itens `{ id, type?, depth?, via?, memory }` (`memory` null se apagada) |
| `POST` | `/v1/memories/review` | `{ ids, reason, source }` marca "a revisar" → `{ results }` (um por id, como no push) |
| `GET/POST/DELETE` | `/v1/users`, `/v1/tokens` | administração (admin) |
| `GET/PUT` | `/v1/workspaces/{key}/members` | membros do workspace (admin): `{ members: ["ana@x.com", "*@x.com"] }`; vazio = aberto |
| `GET/PUT` | `/v1/workspaces/{key}/project` | definição do projeto (do time): pastas e colunas; `PUT` com `baseVersion` → `409` em conflito |
| `GET/PUT` | `/v1/workspaces/{key}/layout` | seu layout no projeto: grupos e terminais; `PUT` com `baseVersion` |
| `POST` | `/v1/logout` | revoga o token em uso (sair) |
| `GET` | `/auth/config`, `/auth/login`, `/auth/callback` | SSO (sem auth; ver acima) |

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
| `refs` (JSON, ids = `gid`) e `review` (JSON) | `refs` e `review` |

Projetos sem `syncKey` e pastas sem remote continuam só locais.
