-- +goose Up
-- +goose StatementBegin
-- Restock audit trail (RestockLogList): one row per lifecycle event.
CREATE TABLE IF NOT EXISTS inventory_restock_logs (
  id            BIGSERIAL   PRIMARY KEY,
  restock_id    BIGINT      NOT NULL,
  action        TEXT        NOT NULL,
  note          TEXT        NOT NULL DEFAULT '',
  created_by_id BIGINT      NOT NULL DEFAULT 0,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_inventory_restock_logs_restock_id ON inventory_restock_logs (restock_id);

-- Per-item problem detail (RestockUpdate{problem}.items).
ALTER TABLE inventory_restock_items ADD COLUMN IF NOT EXISTS problem_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE inventory_restock_items ADD COLUMN IF NOT EXISTS problem_note TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE inventory_restock_items DROP COLUMN IF EXISTS problem_note;
ALTER TABLE inventory_restock_items DROP COLUMN IF EXISTS problem_count;
DROP TABLE IF EXISTS inventory_restock_logs;
-- +goose StatementEnd
