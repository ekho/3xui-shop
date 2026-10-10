-- +goose Up
CREATE TABLE maintenance_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    enabled boolean NOT NULL DEFAULT false,
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
    changed_at timestamptz
);
INSERT INTO maintenance_state (singleton) VALUES (true);

CREATE TABLE maintenance_commands (
    actor_id uuid NOT NULL REFERENCES accounts(id),
    idempotency_key uuid NOT NULL,
    request_hash bytea NOT NULL,
    request jsonb NOT NULL,
    result jsonb NOT NULL,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (actor_id, idempotency_key)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM maintenance_commands) THEN
  RAISE EXCEPTION 'maintenance downgrade blocked: command history exists';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE maintenance_commands;
DROP TABLE maintenance_state;
