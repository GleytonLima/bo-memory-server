-- Quadro de tarefas do workspace (docs/API.md → "Quadro de tarefas"): uma linha por tarefa, com
-- revisão própria (o cliente puxa "desde" o último rev visto) e tombstones nas exclusões.

-- +goose Up
CREATE SEQUENCE task_rev_seq;

CREATE TABLE tasks (
  workspace  text NOT NULL REFERENCES workspaces(key) ON DELETE CASCADE,
  id         text NOT NULL,                -- gerado pelo cliente
  key        text NOT NULL,                -- ex.: BO-27
  rev        bigint NOT NULL,
  doc        jsonb NOT NULL DEFAULT '{}',  -- conteúdo da tarefa (título, coluna, histórico…)
  deleted    boolean NOT NULL DEFAULT false,
  updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace, id)
);
CREATE INDEX tasks_rev ON tasks (workspace, rev);
CREATE UNIQUE INDEX tasks_live_key ON tasks (workspace, key) WHERE NOT deleted;

-- +goose Down
DROP TABLE tasks;
DROP SEQUENCE task_rev_seq;
