-- +goose Up
-- +goose StatementBegin
CREATE TABLE product_configs (
  id                BIGSERIAL   PRIMARY KEY,
  product_id        BIGINT      NOT NULL,
  warehouse_id      BIGINT      NOT NULL,
  queue_type        INTEGER     NOT NULL DEFAULT 0,
  placement_picking INTEGER     NOT NULL DEFAULT 0,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT uniq_product_configs UNIQUE (product_id, warehouse_id)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS product_configs;
-- +goose StatementEnd
