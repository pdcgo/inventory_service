-- +goose Up
-- +goose StatementBegin
-- Free-text reason on placement log rows (PlacementMove note). Additive & safe
-- on existing rows.
ALTER TABLE stock_placement_logs ADD COLUMN IF NOT EXISTS note TEXT NOT NULL DEFAULT '';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE stock_placement_logs DROP COLUMN IF EXISTS note;
-- +goose StatementEnd
