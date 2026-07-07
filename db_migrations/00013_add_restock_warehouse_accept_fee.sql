-- +goose Up
-- +goose StatementBegin
-- Fee the accepting warehouse charges the selling team on accept; posted as a
-- payable via invoice_v2 in the same tx. Additive & safe on existing rows.
ALTER TABLE inventory_restocks ADD COLUMN IF NOT EXISTS warehouse_accept_fee DOUBLE PRECISION NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE inventory_restocks DROP COLUMN IF EXISTS warehouse_accept_fee;
-- +goose StatementEnd
