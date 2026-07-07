-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS inventory_transfers (
  id                 BIGSERIAL   PRIMARY KEY,
  team_id            BIGINT      NOT NULL DEFAULT 0,
  from_warehouse_id  BIGINT      NOT NULL,
  to_warehouse_id    BIGINT      NOT NULL,
  note               TEXT        NOT NULL DEFAULT '',
  status             TEXT        NOT NULL DEFAULT 'pending',
  out_transaction_id BIGINT      NOT NULL DEFAULT 0,
  in_transaction_id  BIGINT      NOT NULL DEFAULT 0,
  created_by_id      BIGINT      NOT NULL DEFAULT 0,
  created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  accepted_at        TIMESTAMPTZ,
  canceled_at        TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_inventory_transfers_from_warehouse_id ON inventory_transfers (from_warehouse_id);
CREATE INDEX IF NOT EXISTS idx_inventory_transfers_to_warehouse_id ON inventory_transfers (to_warehouse_id);
CREATE INDEX IF NOT EXISTS idx_inventory_transfers_status ON inventory_transfers (status);

CREATE TABLE IF NOT EXISTS inventory_transfer_items (
  id          BIGSERIAL        PRIMARY KEY,
  transfer_id BIGINT           NOT NULL,
  product_id  BIGINT           NOT NULL,
  count       BIGINT           NOT NULL DEFAULT 0,
  price       DOUBLE PRECISION NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_inventory_transfer_items_transfer_id ON inventory_transfer_items (transfer_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS inventory_transfer_items;
DROP TABLE IF EXISTS inventory_transfers;
-- +goose StatementEnd
