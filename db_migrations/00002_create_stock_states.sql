-- +goose Up
-- +goose StatementBegin
CREATE TABLE stock_states (
    id                 BIGSERIAL        PRIMARY KEY,
    product_id         BIGINT           NOT NULL,
    warehouse_id       BIGINT           NOT NULL,
    stock_ready        BIGINT           NOT NULL DEFAULT 0,
    stock_ready_amount DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    CONSTRAINT uniq_stock_states UNIQUE (product_id, warehouse_id)
);
CREATE INDEX idx_stock_states_warehouse_id ON stock_states (warehouse_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS stock_states;
-- +goose StatementEnd
