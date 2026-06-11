-- +goose Up
-- +goose StatementBegin
CREATE TABLE stock_placements (
    id           BIGSERIAL   PRIMARY KEY,
    sku_id       TEXT        NOT NULL,
    warehouse_id BIGINT      NOT NULL,
    rack_id      BIGINT      NOT NULL,
    count        BIGINT      NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uniq_stock_placements UNIQUE (sku_id, warehouse_id, rack_id)
);
CREATE INDEX idx_stock_placements_warehouse_id ON stock_placements (warehouse_id);
CREATE INDEX idx_stock_placements_sku_id       ON stock_placements (sku_id);
CREATE INDEX idx_stock_placements_rack_id      ON stock_placements (rack_id);

CREATE TABLE stock_placement_logs (
    id           BIGSERIAL   PRIMARY KEY,
    sku_id       TEXT        NOT NULL,
    warehouse_id BIGINT      NOT NULL,
    rack_id      BIGINT      NOT NULL,
    change_type  SMALLINT    NOT NULL DEFAULT 0,
    change       BIGINT      NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_stock_placement_logs_sku_id       ON stock_placement_logs (sku_id);
CREATE INDEX idx_stock_placement_logs_warehouse_id ON stock_placement_logs (warehouse_id);
CREATE INDEX idx_stock_placement_logs_created_at   ON stock_placement_logs (created_at);

CREATE TABLE batch_stocks (
    id           BIGSERIAL        PRIMARY KEY,
    sku_id       TEXT             NOT NULL,
    warehouse_id BIGINT           NOT NULL,
    inbound_id   BIGINT           NOT NULL,
    start_count  BIGINT           NOT NULL,
    end_count    BIGINT           NOT NULL,
    price        DOUBLE PRECISION NOT NULL,
    created_at   TIMESTAMPTZ      NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_batch_stocks_sku_warehouse ON batch_stocks (sku_id, warehouse_id);
CREATE INDEX idx_batch_stocks_inbound_id    ON batch_stocks (inbound_id);
-- open (non-depleted) batches, ordered for FIFO consumption
CREATE INDEX idx_batch_stocks_open ON batch_stocks (sku_id, warehouse_id, id) WHERE end_count > 0;

CREATE TABLE batch_stock_logs (
    id             BIGSERIAL        PRIMARY KEY,
    sku_id         TEXT             NOT NULL,
    warehouse_id   BIGINT           NOT NULL,
    batch_id       BIGINT           NOT NULL,
    change_type    SMALLINT         NOT NULL DEFAULT 0,
    change         BIGINT           NOT NULL,
    price          DOUBLE PRECISION NOT NULL,
    batch_count    BIGINT           NOT NULL,
    batch_amount   DOUBLE PRECISION NOT NULL,
    balance_count  BIGINT           NOT NULL,
    balance_amount DOUBLE PRECISION NOT NULL,
    created_at     TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_batch_stock_logs_batch_id   ON batch_stock_logs (batch_id);
CREATE INDEX idx_batch_stock_logs_sku_id     ON batch_stock_logs (sku_id);
CREATE INDEX idx_batch_stock_logs_created_at ON batch_stock_logs (created_at);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS batch_stock_logs;
DROP TABLE IF EXISTS batch_stocks;
DROP TABLE IF EXISTS stock_placement_logs;
DROP TABLE IF EXISTS stock_placements;
-- +goose StatementEnd
