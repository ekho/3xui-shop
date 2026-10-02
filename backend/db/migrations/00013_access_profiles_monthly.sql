-- +goose Up
ALTER TABLE accounts ADD COLUMN access_profile text CHECK(access_profile IN ('regular','euru','unlimited'));
UPDATE accounts SET access_profile='regular' WHERE kind='web' AND assigned_panel_id IS NULL;
ALTER TABLE access_operations ADD COLUMN sequence bigint, ADD COLUMN execution_actor_id uuid REFERENCES accounts(id), ADD COLUMN monthly_period text CHECK(monthly_period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$');
CREATE SEQUENCE access_operations_sequence_seq OWNED BY access_operations.sequence;
UPDATE access_operations AS a SET sequence=ranked.n FROM (SELECT id,row_number() OVER (ORDER BY created_at,id) AS n FROM access_operations) AS ranked WHERE ranked.id=a.id;
SELECT setval('access_operations_sequence_seq',COALESCE((SELECT max(sequence) FROM access_operations),1),(SELECT count(*)>0 FROM access_operations));
ALTER TABLE access_operations ALTER COLUMN sequence SET DEFAULT nextval('access_operations_sequence_seq'), ALTER COLUMN sequence SET NOT NULL;
ALTER TABLE access_operations ADD CONSTRAINT access_operations_sequence_key UNIQUE(sequence);
-- Only confirmed native writes and granted trials establish a pre-S08 profile.
-- Old trial targets had no profile field because provisioning could only use regular.
UPDATE accounts AS a SET access_profile = COALESCE(
 (SELECT CASE WHEN x.target->>'profile' IN ('regular','euru','unlimited') THEN x.target->>'profile' END
  FROM access_operations AS x WHERE x.account_id=a.id AND x.status='applied' ORDER BY x.sequence DESC LIMIT 1),
 (SELECT CASE WHEN o.target ? 'profile' THEN
    CASE WHEN o.target->>'profile' IN ('regular','euru','unlimited') THEN o.target->>'profile' END
   ELSE 'regular' END
  FROM trial_grants AS g JOIN trial_operations AS o ON o.id=g.operation_id
  WHERE g.account_id=a.id AND g.status='granted' AND o.status='applied' ORDER BY o.created_at DESC,o.id DESC LIMIT 1))
WHERE a.assigned_panel_id IS NOT NULL AND a.access_profile IS NULL;
ALTER TABLE access_operations DROP CONSTRAINT access_operations_kind_check;
ALTER TABLE access_operations ADD CONSTRAINT access_operations_kind_check CHECK(kind IN ('compensate','assign_plan','starter_trial','reset_traffic','set_profile','set_vpn_ban','monthly_reset'));
ALTER TABLE access_operations DROP CONSTRAINT access_operations_status_check;
ALTER TABLE access_operations ADD CONSTRAINT access_operations_status_check CHECK(status IN ('pending','provisioning','applied','needs_review','skipped'));
ALTER TABLE access_operations ADD CONSTRAINT access_monthly_period_kind CHECK((kind='monthly_reset')=(monthly_period IS NOT NULL));
ALTER TABLE access_operations DROP CONSTRAINT access_operations_check;
ALTER TABLE access_operations ADD CONSTRAINT access_operations_check CHECK (
 (kind='assign_plan' AND plan_id IS NOT NULL AND plan_revision>0 AND period_days>0)
 OR (kind='set_profile' AND ((plan_id IS NULL AND plan_revision IS NULL AND period_days IS NULL) OR (plan_id IS NOT NULL AND plan_revision>0 AND period_days IS NULL)))
 OR (kind NOT IN ('assign_plan','set_profile') AND plan_id IS NULL AND plan_revision IS NULL AND period_days IS NULL));
ALTER TABLE audit_events ADD COLUMN system_actor boolean, ADD COLUMN monthly_period text CHECK(monthly_period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$');
CREATE TABLE monthly_reset_periods (
 account_id uuid NOT NULL REFERENCES accounts(id),
 local_period text NOT NULL CHECK(local_period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'),
 timezone text NOT NULL,
 status text NOT NULL CHECK(status IN ('waiting','enqueued','skipped','period_elapsed_unserved')),
 operation_id uuid UNIQUE REFERENCES access_operations(id),
 deferred_at timestamptz,
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 PRIMARY KEY(account_id,local_period)
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM monthly_reset_periods)
 OR EXISTS(SELECT 1 FROM access_operations WHERE kind IN ('set_profile','set_vpn_ban','monthly_reset') OR execution_actor_id IS NOT NULL)
 OR EXISTS(SELECT 1 FROM accounts WHERE access_profile IS NOT NULL)
 OR EXISTS(SELECT 1 FROM audit_events WHERE system_actor IS NOT NULL OR monthly_period IS NOT NULL) THEN
  RAISE EXCEPTION 'access profile downgrade blocked: retained data exists';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE monthly_reset_periods;
ALTER TABLE audit_events DROP COLUMN system_actor, DROP COLUMN monthly_period;
ALTER TABLE access_operations DROP CONSTRAINT access_monthly_period_kind;
ALTER TABLE access_operations DROP CONSTRAINT access_operations_check;
ALTER TABLE access_operations ADD CONSTRAINT access_operations_check CHECK ((kind='assign_plan' AND plan_id IS NOT NULL AND plan_revision>0 AND period_days>0) OR (kind<>'assign_plan' AND plan_id IS NULL AND plan_revision IS NULL AND period_days IS NULL));
ALTER TABLE access_operations DROP CONSTRAINT access_operations_kind_check;
ALTER TABLE access_operations ADD CONSTRAINT access_operations_kind_check CHECK(kind IN ('compensate','assign_plan','starter_trial','reset_traffic'));
ALTER TABLE access_operations DROP CONSTRAINT access_operations_status_check;
ALTER TABLE access_operations ADD CONSTRAINT access_operations_status_check CHECK(status IN ('pending','provisioning','applied','needs_review'));
ALTER TABLE access_operations DROP COLUMN execution_actor_id, DROP COLUMN monthly_period, DROP COLUMN sequence;
ALTER TABLE accounts DROP COLUMN access_profile;
