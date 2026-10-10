-- +goose Up
CREATE TABLE promocodes (
 id uuid PRIMARY KEY,
 code text NOT NULL UNIQUE CHECK(char_length(code) BETWEEN 1 AND 32),
 duration_days integer NOT NULL CHECK(duration_days>0),
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 created_at timestamptz,
 deleted_at timestamptz,
 is_activated boolean NOT NULL DEFAULT false,
 activated_account_id uuid REFERENCES accounts(id),
 activated_by_tg_id bigint CHECK(activated_by_tg_id>0),
 activated_at timestamptz,
 legacy_source text CHECK(char_length(legacy_source) BETWEEN 1 AND 100),
 legacy_promocode_id bigint CHECK(legacy_promocode_id>0),
 UNIQUE(legacy_source,legacy_promocode_id),
 CHECK((legacy_source IS NULL)=(legacy_promocode_id IS NULL)),
 CHECK(legacy_source IS NOT NULL OR created_at IS NOT NULL),
 CHECK(is_activated OR (activated_account_id IS NULL AND activated_by_tg_id IS NULL AND activated_at IS NULL)),
 CHECK(NOT is_activated OR deleted_at IS NULL)
);
CREATE INDEX promocodes_page ON promocodes(created_at DESC NULLS LAST,id DESC);
CREATE TABLE promocode_events (
 id uuid PRIMARY KEY,
 promocode_id uuid NOT NULL REFERENCES promocodes(id),
 actor_account_id uuid REFERENCES accounts(id),
 action text NOT NULL CHECK(action IN ('create','edit','delete','activate','legacy_import')),
 created_at timestamptz NOT NULL,
 reason text,
 before_snapshot jsonb,
 after_snapshot jsonb NOT NULL,
 CHECK((action='legacy_import' AND actor_account_id IS NULL) OR
  (action<>'legacy_import' AND actor_account_id IS NOT NULL AND reason IS NOT NULL)),
 CHECK(NOT after_snapshot ? 'code' AND (before_snapshot IS NULL OR NOT before_snapshot ? 'code'))
);
CREATE INDEX promocode_events_page ON promocode_events(promocode_id,created_at DESC,id DESC);
-- +goose StatementBegin
CREATE FUNCTION promocode_metadata_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN RAISE EXCEPTION 'promocode history must be retained'; END IF;
 IF (NEW.id,NEW.code,NEW.created_at,NEW.legacy_source,NEW.legacy_promocode_id)
   IS DISTINCT FROM (OLD.id,OLD.code,OLD.created_at,OLD.legacy_source,OLD.legacy_promocode_id)
   OR NEW.revision<>OLD.revision+1 OR OLD.deleted_at IS NOT NULL THEN
  RAISE EXCEPTION 'promocode identity or deleted state is immutable';
 END IF;
 IF OLD.is_activated AND
   (NEW.duration_days,NEW.is_activated,NEW.activated_account_id,NEW.activated_by_tg_id,NEW.activated_at,NEW.deleted_at)
   IS DISTINCT FROM
   (OLD.duration_days,OLD.is_activated,OLD.activated_account_id,OLD.activated_by_tg_id,OLD.activated_at,OLD.deleted_at) THEN
  RAISE EXCEPTION 'activated promocode must be retained';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER promocode_metadata_guard BEFORE UPDATE OR DELETE ON promocodes
 FOR EACH ROW EXECUTE FUNCTION promocode_metadata_guard();
-- +goose StatementBegin
CREATE FUNCTION promocode_history_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'promocode history is immutable'; END $$;
-- +goose StatementEnd
CREATE TRIGGER promocode_history_guard BEFORE UPDATE OR DELETE ON promocode_events
 FOR EACH ROW EXECUTE FUNCTION promocode_history_guard();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM promocodes) OR EXISTS(SELECT 1 FROM promocode_events) THEN
  RAISE EXCEPTION 'promocode downgrade blocked: retained usage/history exists';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE promocode_events,promocodes;
DROP FUNCTION promocode_history_guard(),promocode_metadata_guard();
