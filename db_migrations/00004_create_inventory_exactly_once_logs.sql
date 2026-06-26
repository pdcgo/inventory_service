-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS inventory_exactly_once_logs (
  id           VARCHAR(400) NOT NULL,
  subscription VARCHAR(400) NOT NULL,
  created_at   TIMESTAMPTZ  NOT NULL DEFAULT now(),
  PRIMARY KEY (id, subscription)
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS inventory_exactly_once_logs;
-- +goose StatementEnd
