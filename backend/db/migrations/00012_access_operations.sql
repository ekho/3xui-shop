-- +goose Up
CREATE TABLE access_operations (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id),
 operator_account_id uuid REFERENCES accounts(id),
 kind text NOT NULL CHECK(kind IN ('compensate','assign_plan','starter_trial','reset_traffic')),
 status text NOT NULL CHECK(status IN ('pending','provisioning','applied','needs_review')),
 reason text NOT NULL CHECK(char_length(reason) BETWEEN 1 AND 1000),
 plan_id uuid REFERENCES catalogue_plans(id),
 plan_revision bigint,
 period_days bigint,
 desired jsonb NOT NULL CHECK(jsonb_typeof(desired)='object'),
 target jsonb NOT NULL CHECK(jsonb_typeof(target)='object'),
 completed_steps jsonb NOT NULL DEFAULT '[]'::jsonb CHECK(jsonb_typeof(completed_steps)='array'),
 review_reason text,
 write_started boolean NOT NULL DEFAULT false,
 reset_started boolean NOT NULL DEFAULT false,
 reset_acknowledged boolean NOT NULL DEFAULT false,
 attempts integer NOT NULL DEFAULT 0,
 lease_hash bytea,
 lease_expires_at timestamptz,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 CHECK ((kind='assign_plan' AND plan_id IS NOT NULL AND plan_revision>0 AND period_days>0)
   OR (kind<>'assign_plan' AND plan_id IS NULL AND plan_revision IS NULL AND period_days IS NULL)),
 FOREIGN KEY(plan_id,plan_revision) REFERENCES catalogue_revisions(plan_id,revision)
);
CREATE UNIQUE INDEX access_one_unresolved_account ON access_operations(account_id)
 WHERE status IN ('pending','provisioning','needs_review');
CREATE INDEX access_latest_applied ON access_operations(account_id,updated_at DESC,id DESC) WHERE status='applied';
ALTER TABLE audit_events ADD COLUMN access_operation_id uuid REFERENCES access_operations(id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM access_operations) OR EXISTS(SELECT 1 FROM audit_events WHERE access_operation_id IS NOT NULL) THEN
  RAISE EXCEPTION 'access operation downgrade blocked: history exists';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE audit_events DROP COLUMN access_operation_id;
DROP TABLE access_operations;
