-- Trocar de máquina (docs/API.md → "Projeto e layout"): a definição do projeto (pastas pelo
-- remote e colunas do quadro) é do workspace; o layout (grupos e terminais) é de cada usuário.

-- +goose Up
CREATE TABLE workspace_projects (
  workspace  text PRIMARY KEY REFERENCES workspaces(key) ON DELETE CASCADE,
  name       text NOT NULL,
  folders    jsonb NOT NULL DEFAULT '[]',   -- [{ name, remote, url }]
  columns    jsonb NOT NULL DEFAULT '[]',   -- [{ id, name, description }]
  version    bigint NOT NULL,
  updated_by uuid REFERENCES users(id) ON DELETE SET NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE user_layouts (
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  workspace  text NOT NULL REFERENCES workspaces(key) ON DELETE CASCADE,
  groups     jsonb NOT NULL DEFAULT '[]',   -- [{ name, terminals: [{ name, folder, subdir, startup, color }] }]
  version    bigint NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, workspace)
);

-- +goose Down
DROP TABLE user_layouts;
DROP TABLE workspace_projects;
