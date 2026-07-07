-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS inventory_opnames (
  id                       BIGSERIAL   PRIMARY KEY,
  team_id                  BIGINT      NOT NULL DEFAULT 0,
  warehouse_id             BIGINT      NOT NULL,
  name                     TEXT        NOT NULL DEFAULT '',
  status                   TEXT        NOT NULL DEFAULT 'pending',
  inventory_transaction_id BIGINT,
  created_by_id            BIGINT      NOT NULL DEFAULT 0,
  completed_by_id          BIGINT      NOT NULL DEFAULT 0,
  created_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at               TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  completed_at             TIMESTAMPTZ,
  canceled_at              TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_inventory_opnames_warehouse_id ON inventory_opnames (warehouse_id);
CREATE INDEX IF NOT EXISTS idx_inventory_opnames_status ON inventory_opnames (status);

CREATE TABLE IF NOT EXISTS inventory_opname_lines (
  id             BIGSERIAL PRIMARY KEY,
  opname_id      BIGINT    NOT NULL,
  rack_id        BIGINT    NOT NULL,
  product_id     BIGINT    NOT NULL,
  expected_count BIGINT    NOT NULL DEFAULT 0,
  counted_count  BIGINT    NOT NULL DEFAULT 0,
  counted        BOOLEAN   NOT NULL DEFAULT FALSE
);
CREATE INDEX IF NOT EXISTS idx_inventory_opname_lines_opname_id ON inventory_opname_lines (opname_id);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_opname_line ON inventory_opname_lines (opname_id, rack_id, product_id);

CREATE TABLE IF NOT EXISTS inventory_opname_logs (
  id            BIGSERIAL   PRIMARY KEY,
  opname_id     BIGINT      NOT NULL,
  action        TEXT        NOT NULL DEFAULT '',
  note          TEXT        NOT NULL DEFAULT '',
  created_by_id BIGINT      NOT NULL DEFAULT 0,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_inventory_opname_logs_opname_id ON inventory_opname_logs (opname_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS inventory_opname_logs;
DROP TABLE IF EXISTS inventory_opname_lines;
DROP TABLE IF EXISTS inventory_opnames;
-- +goose StatementEnd
