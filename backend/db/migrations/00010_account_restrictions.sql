-- +goose Up
ALTER TABLE accounts ADD COLUMN restriction_changed_at timestamptz,
 ADD COLUMN restriction_operator_account_id uuid REFERENCES accounts(id);
CREATE TABLE legacy_approval_snapshots (
 account_id uuid PRIMARY KEY REFERENCES accounts(id),
 source_legacy_user_id bigint NOT NULL UNIQUE CHECK (source_legacy_user_id > 0),
 source_tg_id bigint NOT NULL UNIQUE CHECK (source_tg_id > 0),
 status text NOT NULL CHECK (status IN ('pending','approved','rejected')),
 requested_at timestamptz,
 decided_at timestamptz,
 decided_by bigint CHECK (decided_by > 0)
);
CREATE TABLE legacy_approval_events (
 source_id bigint PRIMARY KEY CHECK (source_id > 0),
 account_id uuid NOT NULL REFERENCES accounts(id),
 target_tg_id bigint NOT NULL CHECK (target_tg_id > 0),
 created_at timestamptz NOT NULL,
 action text NOT NULL CHECK (action IN ('approval.approve','approval.reject')),
 actor_type text,
 actor_id bigint CHECK (actor_id > 0),
 actor_name text,
 source text
);
CREATE INDEX legacy_approval_page ON legacy_approval_events(account_id,created_at DESC,source_id DESC);

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM legacy_approval_snapshots)
    OR EXISTS(SELECT 1 FROM legacy_approval_events)
    OR EXISTS(SELECT 1 FROM accounts WHERE restriction_changed_at IS NOT NULL) THEN
  RAISE EXCEPTION 'account restriction downgrade blocked: history exists';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE legacy_approval_events;
DROP TABLE legacy_approval_snapshots;
ALTER TABLE accounts DROP COLUMN restriction_operator_account_id, DROP COLUMN restriction_changed_at;
