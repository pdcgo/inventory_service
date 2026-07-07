-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS inventory_restocks (
  id             BIGSERIAL   PRIMARY KEY,
  team_id        BIGINT      NOT NULL DEFAULT 0,
  warehouse_id   BIGINT      NOT NULL,
  supplier       TEXT        NOT NULL DEFAULT '',
  receipt        TEXT        NOT NULL DEFAULT '',
  note           TEXT        NOT NULL DEFAULT '',
  status         TEXT        NOT NULL DEFAULT 'pending',
  inventory_transaction_id BIGINT,
  created_by_id  BIGINT      NOT NULL DEFAULT 0,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  accepted_at    TIMESTAMPTZ,
  canceled_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_inventory_restocks_warehouse_id ON inventory_restocks (warehouse_id);
CREATE INDEX IF NOT EXISTS idx_inventory_restocks_status ON inventory_restocks (status);

CREATE TABLE IF NOT EXISTS inventory_restock_items (
  id         BIGSERIAL        PRIMARY KEY,
  restock_id BIGINT           NOT NULL,
  product_id BIGINT           NOT NULL,
  count      BIGINT           NOT NULL DEFAULT 0,
  price      DOUBLE PRECISION NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_inventory_restock_items_restock_id ON inventory_restock_items (restock_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS inventory_restock_items;
DROP TABLE IF EXISTS inventory_restocks;
-- +goose StatementEnd
