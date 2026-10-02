-- +goose Up
ALTER TABLE accounts ADD COLUMN assigned_panel_id text, ADD COLUMN had_subscription boolean NOT NULL DEFAULT false;
CREATE TABLE trial_requests (
 id uuid PRIMARY KEY,
 sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
 account_id uuid NOT NULL REFERENCES accounts(id),
 status text NOT NULL CHECK(status IN ('pending','approved','rejected')),
 comment text NOT NULL CHECK(char_length(comment)<=1000),
 created_at timestamptz NOT NULL,
 decided_at timestamptz,
 operator_tg_id bigint,
 reason text,
 operation_id uuid,
 previous_request_id uuid UNIQUE REFERENCES trial_requests(id),
 CHECK ((status='pending' AND decided_at IS NULL AND operator_tg_id IS NULL AND operation_id IS NULL) OR (status='rejected' AND decided_at IS NOT NULL AND operator_tg_id>0 AND operation_id IS NULL) OR (status='approved' AND decided_at IS NOT NULL AND operator_tg_id>0 AND operation_id IS NOT NULL))
);
CREATE UNIQUE INDEX one_pending_trial ON trial_requests(account_id) WHERE status='pending';
CREATE TABLE trial_operations (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id),
 request_id uuid NOT NULL UNIQUE REFERENCES trial_requests(id),
 status text NOT NULL CHECK(status IN ('pending','provisioning','needs_review','applied')),
 trial_enabled boolean NOT NULL CHECK(trial_enabled),
 period_days bigint NOT NULL CHECK(period_days>0),
 traffic_gb bigint NOT NULL CHECK(traffic_gb>=0),
 devices bigint NOT NULL CHECK(devices>=0),
 panel_id text NOT NULL,
 created_at timestamptz NOT NULL
);
ALTER TABLE trial_requests ADD CONSTRAINT request_operation_fk FOREIGN KEY(operation_id) REFERENCES trial_operations(id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE trial_grants (
 account_id uuid PRIMARY KEY REFERENCES accounts(id),
 request_id uuid NOT NULL UNIQUE REFERENCES trial_requests(id),
 operation_id uuid NOT NULL UNIQUE REFERENCES trial_operations(id),
 status text NOT NULL CHECK(status IN ('reserved','granted')),
 created_at timestamptz NOT NULL,
 granted_at timestamptz
);
CREATE TABLE idempotency_records (
 principal text NOT NULL,
 operation text NOT NULL,
 key uuid NOT NULL,
 body_hash bytea NOT NULL,
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY(principal,operation,key)
);
CREATE TABLE decision_callbacks (
 id text PRIMARY KEY CHECK(char_length(id) BETWEEN 1 AND 128),
 request_id uuid NOT NULL REFERENCES trial_requests(id),
 operator_tg_id bigint NOT NULL,
 body_hash bytea NOT NULL,
 result jsonb NOT NULL,
 created_at timestamptz NOT NULL
);
CREATE TABLE audit_events (
 id uuid PRIMARY KEY,
 created_at timestamptz NOT NULL,
 action text NOT NULL,
 account_id uuid NOT NULL REFERENCES accounts(id),
 request_id uuid REFERENCES trial_requests(id),
 operation_id uuid REFERENCES trial_operations(id),
 operator_tg_id bigint,
 reason text CHECK(char_length(reason)<=1000)
);
CREATE TABLE telegram_deliveries (
 id uuid PRIMARY KEY,
 sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
 request_id uuid NOT NULL REFERENCES trial_requests(id),
 operation_id uuid REFERENCES trial_operations(id),
 chat_id bigint NOT NULL CHECK(chat_id>0),
 kind text NOT NULL CHECK(kind IN ('approval_card','request_decided','provision_review','provision_applied')),
 payload jsonb NOT NULL,
 state text NOT NULL CHECK(state IN ('pending','sent','failed')),
 created_at timestamptz NOT NULL,
 available_at timestamptz NOT NULL,
 lease_hash bytea,
 lease_expires_at timestamptz,
 attempts integer NOT NULL DEFAULT 0,
 message_id bigint,
 completed_at timestamptz,
 failure_code text
);
CREATE INDEX pending_telegram ON telegram_deliveries(available_at,sequence) WHERE state='pending';
-- +goose Down
DROP TABLE telegram_deliveries,audit_events,decision_callbacks,idempotency_records,trial_grants;
ALTER TABLE trial_requests DROP CONSTRAINT request_operation_fk;
DROP TABLE trial_operations,trial_requests;
ALTER TABLE accounts DROP COLUMN assigned_panel_id,DROP COLUMN had_subscription;
