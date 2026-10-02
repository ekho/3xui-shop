-- +goose Up
ALTER TABLE telegram_deliveries ADD COLUMN result_hash bytea;
-- +goose Down
ALTER TABLE telegram_deliveries DROP COLUMN result_hash;
