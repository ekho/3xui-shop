-- +goose Up
CREATE TABLE infrastructure_operators (
 account_id uuid PRIMARY KEY REFERENCES operator_accounts(account_id) ON DELETE CASCADE,
 granted_at timestamptz NOT NULL
);
CREATE TABLE vpn_server_actions (
 actor_id uuid NOT NULL REFERENCES accounts(id),
 idempotency_key uuid NOT NULL,
 action text NOT NULL CHECK(action IN ('create','ping','sync','delete')),
 input_hash bytea NOT NULL CHECK(octet_length(input_hash)=32),
 server_id text,
 result jsonb NOT NULL CHECK(jsonb_typeof(result)='object'),
 created_at timestamptz NOT NULL,
 PRIMARY KEY(actor_id,idempotency_key)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM infrastructure_operators) OR EXISTS(SELECT 1 FROM vpn_server_actions) THEN
  RAISE EXCEPTION 'server management downgrade blocked: retained roles or actions';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE vpn_server_actions;
DROP TABLE infrastructure_operators;
