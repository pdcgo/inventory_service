-- +goose Up
-- +goose StatementBegin
ALTER TABLE inventory_opname_lines ADD COLUMN IF NOT EXISTS reason TEXT NOT NULL DEFAULT '';
ALTER TABLE inventory_opname_lines ADD COLUMN IF NOT EXISTS note TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE inventory_opname_lines DROP COLUMN IF EXISTS note;
ALTER TABLE inventory_opname_lines DROP COLUMN IF EXISTS reason;
-- +goose StatementEnd
