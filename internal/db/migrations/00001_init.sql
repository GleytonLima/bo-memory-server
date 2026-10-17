-- Esquema inicial: usuários e tokens, workspaces, memórias (com revisão global, tombstones e
-- busca full-text sem acentos) e histórico de versões. Ver docs/API.md.

-- +goose Up
CREATE EXTENSION IF NOT EXISTS unaccent;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- unaccent não é IMMUTABLE; este invólucro permite usá-la em coluna gerada/índice.
-- +goose StatementBegin
CREATE FUNCTION f_unaccent(text) RETURNS text
LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT AS
$$ SELECT public.unaccent('public.unaccent'::regdictionary, $1) $$;
-- +goose StatementEnd

-- Documento de busca (texto + tags, sem acentos). array_to_string é STABLE; o invólucro IMMUTABLE
-- é seguro aqui porque a conversão de text[] para texto não depende de configuração.
-- +goose StatementBegin
CREATE FUNCTION memory_search_doc(body text, tags text[]) RETURNS tsvector
LANGUAGE sql IMMUTABLE PARALLEL SAFE AS
$$ SELECT to_tsvector('simple', f_unaccent(coalesce(body, '') || ' ' || coalesce(array_to_string(tags, ' '), ''))) $$;
-- +goose StatementEnd

CREATE TABLE users (
  id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  name       text NOT NULL,
  email      text NOT NULL UNIQUE,
  is_admin   boolean NOT NULL DEFAULT false,
  disabled   boolean NOT NULL DEFAULT false,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE tokens (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name         text NOT NULL,               -- ex.: "notebook do trabalho"
  hash         bytea NOT NULL UNIQUE,       -- SHA-256 do token; o token em si nunca é guardado
  created_at   timestamptz NOT NULL DEFAULT now(),
  last_used_at timestamptz,
  revoked_at   timestamptz
);

CREATE TABLE workspaces (
  key        text PRIMARY KEY,              -- identidade do projeto entre máquinas (ex.: bash-organizer)
  name       text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

-- Revisão global: cada escrita recebe o próximo número (o cliente puxa "desde" o último que viu).
CREATE SEQUENCE memory_rev_seq;

CREATE TABLE memories (
  id            uuid PRIMARY KEY,           -- gerado pelo cliente (permite criar offline)
  rev           bigint NOT NULL,
  kind          text NOT NULL CHECK (kind IN ('note', 'decision', 'progress')),
  scope         text NOT NULL CHECK (scope IN ('global', 'project', 'folder')),
  workspace     text REFERENCES workspaces(key),
  folder        text,                       -- remote git normalizado (scope = folder)
  text          text NOT NULL,
  tags          text[] NOT NULL DEFAULT '{}',
  task_key      text,
  superseded_by uuid,
  progress      jsonb,                      -- kind = progress
  author_id     uuid REFERENCES users(id),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  deleted       boolean NOT NULL DEFAULT false,
  search        tsvector GENERATED ALWAYS AS (memory_search_doc(text, tags)) STORED,
  CONSTRAINT workspace_required CHECK (scope <> 'project' AND kind <> 'progress' OR workspace IS NOT NULL),
  CONSTRAINT folder_required CHECK (scope <> 'folder' OR folder IS NOT NULL),
  CONSTRAINT decision_one_sentence CHECK (kind <> 'decision' OR (length(text) <= 280 AND position(E'\n' IN text) = 0))
);

CREATE UNIQUE INDEX memories_rev ON memories (rev);
CREATE INDEX memories_workspace ON memories (workspace, rev);
CREATE INDEX memories_folder ON memories (folder, rev) WHERE folder IS NOT NULL;
CREATE INDEX memories_search ON memories USING gin (search);
-- uma memória de progresso viva por tarefa e workspace
CREATE UNIQUE INDEX memories_progress_task ON memories (workspace, task_key) WHERE kind = 'progress' AND NOT deleted;

-- Versões anteriores de cada memória (auditoria e "o que mudou").
CREATE TABLE memory_history (
  id         bigserial PRIMARY KEY,
  memory_id  uuid NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
  rev        bigint NOT NULL,
  data       jsonb NOT NULL,                -- a memória como era antes da mudança
  changed_by uuid REFERENCES users(id),
  changed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX memory_history_memory ON memory_history (memory_id, rev);

-- +goose Down
DROP TABLE memory_history;
DROP TABLE memories;
DROP SEQUENCE memory_rev_seq;
DROP TABLE workspaces;
DROP TABLE tokens;
DROP TABLE users;
DROP FUNCTION memory_search_doc(text, text[]);
DROP FUNCTION f_unaccent(text);
