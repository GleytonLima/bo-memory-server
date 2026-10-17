-- SSO por OpenID Connect (ex.: Microsoft Entra ID): usuários vinculados ao provedor, sessões que
-- expiram, membros dos workspaces por e-mail e os estados temporários do login.

-- +goose Up
-- usuário do SSO: identificado pelo emissor + "sub" (estável); e-mail e employeeid vêm do token
ALTER TABLE users ADD COLUMN sso_issuer text;
ALTER TABLE users ADD COLUMN sso_subject text;
ALTER TABLE users ADD COLUMN employee_id text;
ALTER TABLE users ADD COLUMN last_login_at timestamptz;
CREATE UNIQUE INDEX users_sso ON users (sso_issuer, sso_subject) WHERE sso_subject IS NOT NULL;

-- token pessoal (criado por você/admin, sem validade) ou sessão (criada no login SSO, expira)
ALTER TABLE tokens ADD COLUMN kind text NOT NULL DEFAULT 'personal' CHECK (kind IN ('personal', 'session'));
ALTER TABLE tokens ADD COLUMN expires_at timestamptz;

-- quem pertence a cada workspace: e-mail exato ou domínio inteiro (*@empresa.com).
-- Workspace sem membros continua aberto a todos os usuários autenticados.
CREATE TABLE workspace_members (
  workspace     text NOT NULL REFERENCES workspaces(key) ON DELETE CASCADE,
  email_pattern text NOT NULL CHECK (email_pattern = lower(email_pattern)),
  added_by      uuid REFERENCES users(id) ON DELETE SET NULL,
  added_at      timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (workspace, email_pattern)
);

-- login em andamento (state → verificador PKCE, nonce, para onde voltar); vale poucos minutos
CREATE TABLE sso_states (
  state      text PRIMARY KEY,
  verifier   text NOT NULL,
  nonce      text NOT NULL,
  return_to  text NOT NULL DEFAULT '/',
  created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE sso_states;
DROP TABLE workspace_members;
ALTER TABLE tokens DROP COLUMN expires_at;
ALTER TABLE tokens DROP COLUMN kind;
DROP INDEX users_sso;
ALTER TABLE users DROP COLUMN last_login_at;
ALTER TABLE users DROP COLUMN employee_id;
ALTER TABLE users DROP COLUMN sso_subject;
ALTER TABLE users DROP COLUMN sso_issuer;
