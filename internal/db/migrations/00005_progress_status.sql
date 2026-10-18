-- Progresso de tarefa ganha estado: open (padrão) ou done — a tarefa foi concluída e o progresso,
-- arquivado, sai da busca padrão (continua no filtro de progresso). Os outros tipos seguem sem estado.

-- +goose Up
ALTER TABLE memories DROP CONSTRAINT idea_status;
ALTER TABLE memories ADD CONSTRAINT memory_status CHECK (
  (kind = 'idea' AND status IN ('open', 'converted', 'discarded'))
  OR (kind = 'progress' AND (status IS NULL OR status IN ('open', 'done')))
  OR (kind NOT IN ('idea', 'progress') AND status IS NULL));

-- +goose Down
UPDATE memories SET status = NULL WHERE kind = 'progress';
ALTER TABLE memories DROP CONSTRAINT memory_status;
ALTER TABLE memories ADD CONSTRAINT idea_status CHECK (
  (kind = 'idea' AND status IN ('open', 'converted', 'discarded')) OR (kind <> 'idea' AND status IS NULL));
