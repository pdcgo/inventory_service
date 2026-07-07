-- +goose Up
-- +goose StatementBegin
-- Purchase/order info for the selling-team create-restock flow (order id, courier,
-- ongkir, payment method). Additive & safe on existing rows.
ALTER TABLE inventory_restocks ADD COLUMN IF NOT EXISTS extern_order_id TEXT NOT NULL DEFAULT '';
ALTER TABLE inventory_restocks ADD COLUMN IF NOT EXISTS shipping_id BIGINT NOT NULL DEFAULT 0;
ALTER TABLE inventory_restocks ADD COLUMN IF NOT EXISTS shipping_cost DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE inventory_restocks ADD COLUMN IF NOT EXISTS payment_type TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE inventory_restocks DROP COLUMN IF EXISTS payment_type;
ALTER TABLE inventory_restocks DROP COLUMN IF EXISTS shipping_cost;
ALTER TABLE inventory_restocks DROP COLUMN IF EXISTS shipping_id;
ALTER TABLE inventory_restocks DROP COLUMN IF EXISTS extern_order_id;
-- +goose StatementEnd
