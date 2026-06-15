-- +goose Up
-- +goose StatementBegin
CREATE TABLE stock_placements (
    id           BIGSERIAL   PRIMARY KEY,
    product_id   BIGINT      NOT NULL,
    warehouse_id BIGINT      NOT NULL,
    rack_id      BIGINT      NOT NULL,
    count        BIGINT      NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uniq_stock_placements UNIQUE (product_id, warehouse_id, rack_id)
);
CREATE INDEX idx_stock_placements_warehouse_id ON stock_placements (warehouse_id);
CREATE INDEX idx_stock_placements_product_id   ON stock_placements (product_id);
CREATE INDEX idx_stock_placements_rack_id      ON stock_placements (rack_id);

CREATE TABLE stock_placement_logs (
    id             BIGSERIAL   PRIMARY KEY,
    product_id     BIGINT      NOT NULL,
    warehouse_id   BIGINT      NOT NULL,
    rack_id        BIGINT      NOT NULL,
    transaction_id BIGINT      NOT NULL DEFAULT 0,
    user_id        BIGINT      NOT NULL DEFAULT 0,
    change_type    INTEGER     NOT NULL DEFAULT 0,
    change         BIGINT      NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_stock_placement_logs_product_id     ON stock_placement_logs (product_id);
CREATE INDEX idx_stock_placement_logs_warehouse_id   ON stock_placement_logs (warehouse_id);
CREATE INDEX idx_stock_placement_logs_transaction_id ON stock_placement_logs (transaction_id);
CREATE INDEX idx_stock_placement_logs_user_id        ON stock_placement_logs (user_id);
CREATE INDEX idx_stock_placement_logs_created_at     ON stock_placement_logs (created_at);

CREATE TABLE stock_batches (
    id           BIGSERIAL        PRIMARY KEY,
    product_id   BIGINT           NOT NULL,
    warehouse_id BIGINT           NOT NULL,
    inbound_id   BIGINT           NOT NULL,
    start_count  BIGINT           NOT NULL,
    end_count    BIGINT           NOT NULL,
    price        DOUBLE PRECISION NOT NULL,
    created_at   TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_stock_batches_product_warehouse ON stock_batches (product_id, warehouse_id);
CREATE INDEX idx_stock_batches_inbound_id        ON stock_batches (inbound_id);
-- open (non-depleted) batches, ordered for FIFO consumption
CREATE INDEX idx_stock_batches_open ON stock_batches (product_id, warehouse_id, id) WHERE end_count > 0;

CREATE TABLE stock_batch_logs (
    id             BIGSERIAL        PRIMARY KEY,
    product_id     BIGINT           NOT NULL,
    warehouse_id   BIGINT           NOT NULL,
    user_id        BIGINT           NOT NULL DEFAULT 0,
    batch_id       BIGINT           NOT NULL,
    transaction_id BIGINT           NOT NULL DEFAULT 0,
    change_type    INTEGER          NOT NULL DEFAULT 0,
    change         BIGINT           NOT NULL,
    price          DOUBLE PRECISION NOT NULL,
    batch_count    BIGINT           NOT NULL,
    batch_amount   DOUBLE PRECISION NOT NULL,
    balance_count  BIGINT           NOT NULL,
    balance_amount DOUBLE PRECISION NOT NULL,
    created_at     TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_stock_batch_logs_batch_id       ON stock_batch_logs (batch_id);
CREATE INDEX idx_stock_batch_logs_product_id     ON stock_batch_logs (product_id);
CREATE INDEX idx_stock_batch_logs_user_id        ON stock_batch_logs (user_id);
CREATE INDEX idx_stock_batch_logs_transaction_id ON stock_batch_logs (transaction_id);
CREATE INDEX idx_stock_batch_logs_created_at     ON stock_batch_logs (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS stock_batch_logs;
DROP TABLE IF EXISTS stock_batches;
DROP TABLE IF EXISTS stock_placement_logs;
DROP TABLE IF EXISTS stock_placements;
-- +goose StatementEnd
