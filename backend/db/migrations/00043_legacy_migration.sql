-- +goose Up
ALTER TABLE accounts DROP CONSTRAINT accounts_sub_id_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_sub_id_check CHECK (
 sub_id ~ '^[0-9a-z]{16}$' OR legacy_user_id IS NOT NULL AND
 sub_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
);
ALTER TABLE campaign_acquisitions DROP CONSTRAINT campaign_acquisitions_check;
ALTER TABLE campaign_acquisitions ADD CONSTRAINT campaign_acquisitions_check CHECK (
 (channel IN ('web','telegram') AND source_code IS NOT NULL AND created_at IS NOT NULL AND legacy_source IS NULL AND legacy_payload IS NULL)
 OR (channel='legacy_name' AND legacy_source IS NOT NULL AND source_code IS NULL AND legacy_payload IS NOT NULL)
);

CREATE TABLE legacy_account_imports (
 source_id bigint PRIMARY KEY CHECK(source_id>0),
 account_id uuid NOT NULL UNIQUE REFERENCES accounts(id),
 source_tg_id bigint NOT NULL UNIQUE CHECK(source_tg_id BETWEEN 1 AND 4503599627370495),
 source_snapshot jsonb NOT NULL CHECK(jsonb_typeof(source_snapshot)='object')
);
CREATE TABLE legacy_server_imports (
 source_id bigint PRIMARY KEY CHECK(source_id>0),
 server_id text NOT NULL UNIQUE REFERENCES vpn_servers(id),
 source_snapshot jsonb NOT NULL CHECK(jsonb_typeof(source_snapshot)='object')
);
CREATE TABLE legacy_bonus_imports (
 kind text NOT NULL CHECK(kind IN ('promocode','referral','reward')),
 source_id bigint NOT NULL CHECK(source_id>0),
 entity_id uuid NOT NULL,
 source_snapshot jsonb NOT NULL CHECK(jsonb_typeof(source_snapshot)='object'),
 PRIMARY KEY(kind,source_id),
 UNIQUE(kind,entity_id)
);
CREATE TABLE legacy_stars_imports (
 source_legacy_user_id bigint PRIMARY KEY CHECK(source_legacy_user_id>0),
 account_id uuid NOT NULL UNIQUE REFERENCES accounts(id),
 source_tg_id bigint NOT NULL UNIQUE CHECK(source_tg_id BETWEEN 1 AND 4503599627370495),
 stars_charge_id text,
 is_stars_auto_renew boolean,
 stars_expires_at bigint,
 source_snapshot jsonb NOT NULL CHECK(jsonb_typeof(source_snapshot)='object')
);
CREATE TABLE legacy_migration_runs (
 source text PRIMARY KEY CHECK(source ~ '^[0-9A-Za-z_-]{1,64}$'),
 source_digest text NOT NULL CHECK(source_digest ~ '^[0-9a-f]{64}$'),
 catalogue_source jsonb NOT NULL CHECK(jsonb_typeof(catalogue_source)='object'),
 report jsonb NOT NULL CHECK(jsonb_typeof(report)='object'),
 operator_account_id uuid NOT NULL REFERENCES accounts(id),
 created_at timestamptz NOT NULL
);

-- +goose StatementBegin
CREATE FUNCTION legacy_migration_history_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'legacy migration source history is immutable' USING ERRCODE='23514'; END $$;
-- +goose StatementEnd
CREATE TRIGGER legacy_account_import_guard BEFORE UPDATE OR DELETE ON legacy_account_imports
 FOR EACH ROW EXECUTE FUNCTION legacy_migration_history_guard();
CREATE TRIGGER legacy_server_import_guard BEFORE UPDATE OR DELETE ON legacy_server_imports
 FOR EACH ROW EXECUTE FUNCTION legacy_migration_history_guard();
CREATE TRIGGER legacy_bonus_import_guard BEFORE UPDATE OR DELETE ON legacy_bonus_imports
 FOR EACH ROW EXECUTE FUNCTION legacy_migration_history_guard();
CREATE TRIGGER legacy_stars_import_guard BEFORE UPDATE OR DELETE ON legacy_stars_imports
 FOR EACH ROW EXECUTE FUNCTION legacy_migration_history_guard();
CREATE TRIGGER legacy_migration_run_guard BEFORE UPDATE OR DELETE ON legacy_migration_runs
 FOR EACH ROW EXECUTE FUNCTION legacy_migration_history_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM legacy_account_imports) OR EXISTS(SELECT 1 FROM legacy_server_imports)
  OR EXISTS(SELECT 1 FROM legacy_bonus_imports) OR EXISTS(SELECT 1 FROM legacy_stars_imports)
  OR EXISTS(SELECT 1 FROM legacy_migration_runs)
  OR EXISTS(SELECT 1 FROM accounts WHERE sub_id !~ '^[0-9a-z]{16}$')
  OR EXISTS(SELECT 1 FROM campaign_acquisitions WHERE channel='legacy_name' AND legacy_trial_used IS NULL) THEN
  RAISE EXCEPTION 'legacy migration downgrade blocked: retained source history exists';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE legacy_migration_runs,legacy_stars_imports,legacy_bonus_imports,legacy_server_imports,legacy_account_imports;
DROP FUNCTION legacy_migration_history_guard();
ALTER TABLE accounts DROP CONSTRAINT accounts_sub_id_check;
ALTER TABLE accounts ADD CONSTRAINT accounts_sub_id_check CHECK(sub_id ~ '^[0-9a-z]{16}$');
ALTER TABLE campaign_acquisitions DROP CONSTRAINT campaign_acquisitions_check;
ALTER TABLE campaign_acquisitions ADD CONSTRAINT campaign_acquisitions_check CHECK (
 (channel IN ('web','telegram') AND source_code IS NOT NULL AND created_at IS NOT NULL AND legacy_source IS NULL AND legacy_payload IS NULL)
 OR (channel='legacy_name' AND legacy_source IS NOT NULL AND source_code IS NULL AND legacy_trial_used IS NOT NULL AND legacy_payload IS NOT NULL)
);
