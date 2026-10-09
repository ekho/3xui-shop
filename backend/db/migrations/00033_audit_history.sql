-- +goose Up
ALTER TABLE audit_events ADD COLUMN mirror_attempted_at timestamptz;
CREATE INDEX audit_history_global ON audit_events(created_at DESC,id DESC);
CREATE INDEX audit_mirror_pending ON audit_events(created_at,id) WHERE mirror_attempted_at IS NULL;
CREATE TABLE legacy_audit_imports (
 source_id bigint PRIMARY KEY CHECK(source_id>0),
 source_hash bytea NOT NULL CHECK(octet_length(source_hash)=32)
);
CREATE TABLE legacy_audit_events (
 source_id bigint PRIMARY KEY REFERENCES legacy_audit_imports(source_id),
 created_at timestamptz NOT NULL,
 action text NOT NULL CHECK(char_length(action) BETWEEN 1 AND 128),
 target_tg_id bigint CHECK(target_tg_id>0),
 actor_type text CHECK(char_length(actor_type)<=256),
 actor_id bigint,
 actor_name text CHECK(char_length(actor_name)<=256),
 source text CHECK(char_length(source)<=256),
 payload_json bytea CHECK(octet_length(payload_json)<=65536)
);
CREATE INDEX legacy_audit_history ON legacy_audit_events(created_at DESC,source_id DESC);
CREATE INDEX legacy_audit_target_history ON legacy_audit_events(target_tg_id,created_at DESC,source_id DESC);
CREATE TABLE audit_system_events (
 id uuid PRIMARY KEY,
 created_at timestamptz NOT NULL,
 action text NOT NULL CHECK(action IN ('audit.pruned','audit.legacy_imported')),
 period_day date UNIQUE,
 cutoff timestamptz,
 retention_days integer CHECK(retention_days BETWEEN 1 AND 3650),
 native_count bigint NOT NULL CHECK(native_count>=0),
 legacy_count bigint NOT NULL CHECK(legacy_count>=0),
 system_count bigint NOT NULL CHECK(system_count>=0),
 mirror_attempted_at timestamptz,
 CHECK((action='audit.pruned' AND period_day IS NOT NULL AND cutoff IS NOT NULL AND retention_days IS NOT NULL)
    OR (action='audit.legacy_imported' AND period_day IS NULL AND cutoff IS NULL AND retention_days IS NULL))
);
CREATE INDEX audit_system_history ON audit_system_events(created_at DESC,id DESC);
CREATE INDEX audit_system_mirror_pending ON audit_system_events(created_at,id) WHERE mirror_attempted_at IS NULL;

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM legacy_audit_events) OR EXISTS(SELECT 1 FROM legacy_audit_imports)
 OR EXISTS(SELECT 1 FROM audit_system_events) THEN
  RAISE EXCEPTION 'Audit history downgrade blocked: retained facts exist';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE audit_system_events;
DROP TABLE legacy_audit_events;
DROP TABLE legacy_audit_imports;
DROP INDEX audit_mirror_pending,audit_history_global;
ALTER TABLE audit_events DROP COLUMN mirror_attempted_at;
