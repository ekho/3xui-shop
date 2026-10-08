-- +goose Up
ALTER TABLE accounts ADD COLUMN registration_source_code text
 CHECK(registration_source_code IS NULL OR
  (char_length(registration_source_code) BETWEEN 1 AND 512 AND registration_source_code ~ '^[A-Za-z0-9_-]+$'));
ALTER TABLE registration_challenges ADD COLUMN source_code text
 CHECK(source_code IS NULL OR source_code ~ '^[A-Za-z0-9_-]{1,64}$');

-- +goose StatementBegin
CREATE FUNCTION registration_source_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NEW.registration_source_code IS DISTINCT FROM OLD.registration_source_code THEN
  RAISE EXCEPTION 'registration source is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER registration_source_immutable BEFORE UPDATE ON accounts
 FOR EACH ROW EXECUTE FUNCTION registration_source_immutable();

CREATE TABLE campaigns (
 id uuid PRIMARY KEY,
 name text NOT NULL UNIQUE CHECK(char_length(name) BETWEEN 1 AND 100),
 code text UNIQUE CHECK(code IS NULL OR
  (code ~ '^[A-Za-z0-9_-]{1,64}$' AND code ~ '[A-Za-z_-]')),
 state text NOT NULL CHECK(state IN ('active','paused','deleted')),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 web_visits bigint NOT NULL DEFAULT 0 CHECK(web_visits>=0),
 source text NOT NULL CHECK(source IN ('operator','legacy_import','legacy_orphan')),
 created_at timestamptz,
 legacy_source text,
 legacy_invite_id bigint CHECK(legacy_invite_id>0),
 legacy_clicks bigint CHECK(legacy_clicks>=0),
 legacy_payload jsonb,
 UNIQUE(legacy_source,legacy_invite_id),
 CHECK((source='operator' AND code IS NOT NULL AND created_at IS NOT NULL AND legacy_source IS NULL AND legacy_payload IS NULL)
  OR (source='legacy_import' AND code IS NOT NULL AND legacy_source IS NOT NULL AND legacy_invite_id IS NOT NULL AND legacy_clicks IS NOT NULL AND legacy_payload IS NOT NULL)
  OR (source='legacy_orphan' AND code IS NULL AND state='deleted' AND legacy_source IS NOT NULL AND legacy_payload IS NOT NULL))
);
CREATE INDEX campaigns_page ON campaigns(created_at DESC NULLS LAST,id DESC);
CREATE TABLE campaign_acquisitions (
 account_id uuid PRIMARY KEY REFERENCES accounts(id),
 campaign_id uuid NOT NULL REFERENCES campaigns(id),
 channel text NOT NULL CHECK(channel IN ('web','telegram','legacy_name')),
 created_at timestamptz,
 source_code text,
 legacy_source text,
 legacy_trial_used boolean,
 legacy_payload jsonb,
 CHECK((channel IN ('web','telegram') AND source_code IS NOT NULL AND created_at IS NOT NULL AND legacy_source IS NULL AND legacy_payload IS NULL)
  OR (channel='legacy_name' AND legacy_source IS NOT NULL AND source_code IS NULL AND legacy_trial_used IS NOT NULL AND legacy_payload IS NOT NULL))
);
CREATE INDEX campaign_cohort ON campaign_acquisitions(campaign_id,account_id);
CREATE UNIQUE INDEX campaign_legacy_user ON campaign_acquisitions(legacy_source,(legacy_payload->>'source_legacy_user_id')) WHERE channel='legacy_name';
CREATE UNIQUE INDEX campaign_legacy_telegram ON campaign_acquisitions(legacy_source,(legacy_payload->>'source_tg_id')) WHERE channel='legacy_name';
CREATE TABLE campaign_events (
 id uuid PRIMARY KEY,
 campaign_id uuid NOT NULL REFERENCES campaigns(id),
 actor_account_id uuid REFERENCES accounts(id),
 action text NOT NULL CHECK(action IN ('create','state','legacy_import')),
 created_at timestamptz NOT NULL,
 reason text,
 before_snapshot jsonb,
 after_snapshot jsonb NOT NULL,
 CHECK((action='legacy_import' AND actor_account_id IS NULL)
  OR (action IN ('create','state') AND actor_account_id IS NOT NULL AND reason IS NOT NULL))
);
CREATE INDEX campaign_events_page ON campaign_events(campaign_id,created_at DESC,id DESC);
-- +goose StatementBegin
CREATE FUNCTION campaign_metadata_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'campaign history must be retained'; END IF;
 IF (NEW.id,NEW.name,NEW.code,NEW.created_at,NEW.source,NEW.legacy_source,NEW.legacy_invite_id,NEW.legacy_clicks,NEW.legacy_payload)
  IS DISTINCT FROM (OLD.id,OLD.name,OLD.code,OLD.created_at,OLD.source,OLD.legacy_source,OLD.legacy_invite_id,OLD.legacy_clicks,OLD.legacy_payload)
  OR (OLD.state='deleted' AND NEW.state<>'deleted') THEN
  RAISE EXCEPTION 'campaign source is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER campaign_metadata_immutable BEFORE UPDATE OR DELETE ON campaigns
 FOR EACH ROW EXECUTE FUNCTION campaign_metadata_immutable();
-- +goose StatementBegin
CREATE FUNCTION campaign_history_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'campaign history is immutable'; END $$;
-- +goose StatementEnd
CREATE TRIGGER campaign_acquisitions_immutable BEFORE UPDATE OR DELETE ON campaign_acquisitions
 FOR EACH ROW EXECUTE FUNCTION campaign_history_immutable();
CREATE TRIGGER campaign_events_immutable BEFORE UPDATE OR DELETE ON campaign_events
 FOR EACH ROW EXECUTE FUNCTION campaign_history_immutable();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM campaigns) OR EXISTS(SELECT 1 FROM campaign_acquisitions)
  OR EXISTS(SELECT 1 FROM campaign_events)
  OR EXISTS(SELECT 1 FROM accounts WHERE registration_source_code IS NOT NULL)
  OR EXISTS(SELECT 1 FROM registration_challenges WHERE source_code IS NOT NULL) THEN
  RAISE EXCEPTION 'campaign downgrade blocked: retained source/history exists';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE campaign_events,campaign_acquisitions,campaigns;
DROP FUNCTION campaign_history_immutable();
DROP FUNCTION campaign_metadata_immutable();
DROP TRIGGER registration_source_immutable ON accounts;
DROP FUNCTION registration_source_immutable();
ALTER TABLE accounts DROP COLUMN registration_source_code;
ALTER TABLE registration_challenges DROP COLUMN source_code;
