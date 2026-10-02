-- +goose Up
CREATE TABLE catalogue_plans (
 id uuid PRIMARY KEY,
 legacy_plan_id bigint UNIQUE CHECK (legacy_plan_id > 0),
 current_revision bigint NOT NULL CHECK (current_revision > 0),
 current_devices integer NOT NULL CHECK (current_devices BETWEEN 1 AND 10000),
 current_profile text NOT NULL CHECK (current_profile IN ('regular','euru','unlimited')),
 current_hidden boolean NOT NULL,
 archived boolean NOT NULL DEFAULT false
);
CREATE UNIQUE INDEX catalogue_active_devices ON catalogue_plans(current_devices) WHERE NOT archived;
CREATE TABLE catalogue_revisions (
 plan_id uuid NOT NULL REFERENCES catalogue_plans(id),
 revision bigint NOT NULL CHECK (revision > 0),
 terms jsonb NOT NULL CHECK (jsonb_typeof(terms)='object'),
 archived boolean NOT NULL,
 actor_account_id uuid REFERENCES accounts(id),
 source text NOT NULL CHECK (source IN ('operator','legacy_import','unlimited_seed')),
 changed_at timestamptz NOT NULL,
 reason text CHECK (reason IS NULL OR char_length(reason) BETWEEN 1 AND 1000),
 PRIMARY KEY(plan_id,revision),
 CHECK ((source='operator' AND actor_account_id IS NOT NULL AND reason IS NOT NULL)
     OR (source<>'operator' AND actor_account_id IS NULL AND reason IS NULL))
);
-- +goose StatementBegin
CREATE FUNCTION catalogue_revision_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'catalogue revision is immutable';
END $$;
-- +goose StatementEnd
CREATE TRIGGER catalogue_revision_immutable BEFORE UPDATE OR DELETE ON catalogue_revisions
 FOR EACH ROW EXECUTE FUNCTION catalogue_revision_immutable();

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
 IF EXISTS(SELECT 1 FROM catalogue_plans) THEN
  RAISE EXCEPTION 'catalogue downgrade blocked: plans exist';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE catalogue_revisions;
DROP FUNCTION catalogue_revision_immutable();
DROP TABLE catalogue_plans;
