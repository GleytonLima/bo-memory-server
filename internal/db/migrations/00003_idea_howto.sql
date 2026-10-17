-- Tipos "idea" (ideia que surgiu em paralelo) e "howto" (como fazer: solução, procedimento, a quem
-- recorrer) e o estado da ideia: open (aberta), converted (virou tarefa), discarded (descartada).

-- +goose Up
ALTER TABLE memories DROP CONSTRAINT memories_kind_check;
ALTER TABLE memories ADD CONSTRAINT memories_kind_check CHECK (kind IN ('note', 'decision', 'progress', 'idea', 'howto'));
ALTER TABLE memories ADD COLUMN status text;
ALTER TABLE memories ADD CONSTRAINT idea_status CHECK (
  (kind = 'idea' AND status IN ('open', 'converted', 'discarded')) OR (kind <> 'idea' AND status IS NULL));

-- +goose Down
ALTER TABLE memories DROP CONSTRAINT idea_status;
ALTER TABLE memories DROP COLUMN status;
DELETE FROM memories WHERE kind IN ('idea', 'howto');
ALTER TABLE memories DROP CONSTRAINT memories_kind_check;
ALTER TABLE memories ADD CONSTRAINT memories_kind_check CHECK (kind IN ('note', 'decision', 'progress'));
