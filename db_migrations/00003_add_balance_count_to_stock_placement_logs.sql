-- +goose Up
-- +goose StatementBegin
ALTER TABLE stock_placement_logs ADD COLUMN balance_count BIGINT NOT NULL DEFAULT 0;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE stock_placement_logs DROP COLUMN balance_count;
-- +goose StatementEnd
