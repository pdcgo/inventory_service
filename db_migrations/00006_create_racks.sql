-- +goose Up
-- +goose StatementBegin
-- Legacy-compatible: `racks` may already exist from the legacy system, so create
-- it only if absent and never alter/drop the existing table.
CREATE TABLE IF NOT EXISTS racks (
  id           BIGSERIAL   PRIMARY KEY,
  warehouse_id BIGINT      NOT NULL,
  name         TEXT        NOT NULL DEFAULT '',
  is_system    BOOLEAN     NOT NULL DEFAULT FALSE,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  deleted      BOOLEAN     NOT NULL DEFAULT FALSE
);
CREATE INDEX IF NOT EXISTS idx_racks_warehouse_id ON racks (warehouse_id);
CREATE INDEX IF NOT EXISTS idx_racks_deleted ON racks (deleted);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- No-op: `racks` may be a pre-existing legacy table; never drop it on rollback.
SELECT 1;
-- +goose StatementEnd
