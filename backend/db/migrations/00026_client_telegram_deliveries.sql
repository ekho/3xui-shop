-- +goose Up
CREATE TABLE client_telegram_deliveries (
 id uuid PRIMARY KEY,
 sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
 account_id uuid NOT NULL REFERENCES accounts(id),
 telegram_id bigint NOT NULL CHECK(telegram_id>0),
 credential_version bigint NOT NULL CHECK(credential_version>=0),
 locale text NOT NULL CHECK(locale IN ('ru','en')),
 event_key text NOT NULL CHECK(char_length(event_key) BETWEEN 1 AND 128),
 route text NOT NULL CHECK(route IN ('cabinet','history','support') OR route ~ '^orders:[0-9a-f-]{36}$'),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','sent','failed','skipped')),
 created_at timestamptz NOT NULL,
 available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 lease_hash bytea CHECK(lease_hash IS NULL OR octet_length(lease_hash)=32),
 lease_expires_at timestamptz,
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 message_id bigint CHECK(message_id IS NULL OR message_id>0),
 completed_at timestamptz,
 failure_code text,
 result_hash bytea CHECK(result_hash IS NULL OR octet_length(result_hash)=32),
 UNIQUE(account_id,event_key)
);
CREATE INDEX pending_client_telegram ON client_telegram_deliveries(available_at,sequence) WHERE state='pending';
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM client_telegram_deliveries) THEN
  RAISE EXCEPTION 'Client Telegram downgrade blocked: delivery facts exist';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE client_telegram_deliveries;
