-- Login do desktop pelo navegador (docs/SSO.md → "Desktop"; RFC 8252): o callback entrega ao app,
-- num endereço local, um código de uso único; o app o troca pela sessão provando o PKCE.

-- +goose Up
CREATE TABLE sso_desktop_codes (
  code_hash  text PRIMARY KEY,               -- SHA-256 do código (o código em si não é guardado)
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  challenge  text NOT NULL,                  -- S256 do verificador que só o app conhece
  created_at timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE sso_desktop_codes;
