-- Dono da tarefa do quadro (docs/API.md → "Quadro de tarefas"): quem criou, até alguém retomá-la.

-- +goose Up
ALTER TABLE tasks ADD COLUMN owner_id uuid REFERENCES users(id) ON DELETE SET NULL;
UPDATE tasks SET owner_id = updated_by;

-- +goose Down
ALTER TABLE tasks DROP COLUMN owner_id;
