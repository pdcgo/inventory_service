-- +goose Up
-- +goose StatementBegin
ALTER TABLE stock_batches ADD COLUMN batch_code TEXT NOT NULL DEFAULT '';
-- unique batch code per product; empty/unset codes are exempt (partial index),
-- so batches without a code don't collide.
CREATE UNIQUE INDEX uniq_stock_batches_product_code
    ON stock_batches (product_id, batch_code)
    WHERE batch_code <> '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS uniq_stock_batches_product_code;
ALTER TABLE stock_batches DROP COLUMN IF EXISTS batch_code;
-- +goose StatementEnd
