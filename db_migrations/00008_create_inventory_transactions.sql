-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS inventory_transactions (
  id            BIGSERIAL   PRIMARY KEY,
  team_id       BIGINT      NOT NULL DEFAULT 0,
  warehouse_id  BIGINT      NOT NULL,
  type          TEXT        NOT NULL,
  status        TEXT        NOT NULL DEFAULT 'active',
  created_by_id BIGINT      NOT NULL DEFAULT 0,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  canceled_at   TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_inventory_transactions_warehouse_id ON inventory_transactions (warehouse_id);
CREATE INDEX IF NOT EXISTS idx_inventory_transactions_status ON inventory_transactions (status);

CREATE TABLE IF NOT EXISTS inventory_transaction_items (
  id             BIGSERIAL        PRIMARY KEY,
  transaction_id BIGINT           NOT NULL,
  product_id     BIGINT           NOT NULL,
  count          BIGINT           NOT NULL DEFAULT 0,
  price          DOUBLE PRECISION NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_inventory_transaction_items_transaction_id ON inventory_transaction_items (transaction_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS inventory_transaction_items;
DROP TABLE IF EXISTS inventory_transactions;
-- +goose StatementEnd
