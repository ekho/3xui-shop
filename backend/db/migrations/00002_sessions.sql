-- +goose Up
CREATE TABLE sessions (
  id_hash bytea PRIMARY KEY CHECK (octet_length(id_hash)=32),
  account_id uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  csrf_token text NOT NULL,
  created_at timestamptz NOT NULL,
  last_seen timestamptz NOT NULL,
  absolute_expires_at timestamptz NOT NULL CHECK (absolute_expires_at>created_at)
);
CREATE INDEX sessions_account_id ON sessions(account_id);
-- +goose Down
DROP TABLE sessions;
