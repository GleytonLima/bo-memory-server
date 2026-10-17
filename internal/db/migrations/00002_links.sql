-- Vínculos entre memórias e a marca "a revisar".
--   refs:   [{ "id": "<uuid>", "type": "depends" | "related" | "contradicts" }] — de quem esta memória
--           depende, com quem se relaciona ou a quem contradiz. Vive na própria memória (sincroniza e
--           entra no controle de conflito como o resto do conteúdo).
--   review: { reason, source, by, at } quando algo de que ela depende mudou; some ao revisar.

-- +goose Up
ALTER TABLE memories ADD COLUMN refs jsonb NOT NULL DEFAULT '[]';
ALTER TABLE memories ADD COLUMN review jsonb;
ALTER TABLE memories ADD CONSTRAINT refs_is_array CHECK (jsonb_typeof(refs) = 'array');
-- "quem aponta para X" (vínculos de entrada e cadeia de impacto): refs @> '[{"id": X}]'
CREATE INDEX memories_refs ON memories USING gin (refs jsonb_path_ops);

-- +goose Down
DROP INDEX memories_refs;
ALTER TABLE memories DROP CONSTRAINT refs_is_array;
ALTER TABLE memories DROP COLUMN review;
ALTER TABLE memories DROP COLUMN refs;
