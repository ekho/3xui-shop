-- +goose Up
ALTER TABLE support_messages ADD COLUMN telegram_only boolean;
ALTER TABLE support_messages DROP CONSTRAINT support_messages_check1;
ALTER TABLE support_messages ADD CONSTRAINT support_message_content CHECK(text<>'' OR attachment_bytes IS NOT NULL OR telegram_only=true);

ALTER TABLE audit_events ADD COLUMN operator_source text CHECK(operator_source IS NULL OR operator_source='telegram_support');
ALTER TABLE audit_events DROP CONSTRAINT audit_single_operator;
ALTER TABLE audit_events ADD CONSTRAINT audit_single_operator CHECK(operator_tg_id IS NULL OR operator_account_id IS NULL OR operator_source='telegram_support');
ALTER TABLE audit_system_events ADD COLUMN support_telegram jsonb CHECK(support_telegram IS NULL OR (jsonb_typeof(support_telegram)='object' AND octet_length(support_telegram::text)<=8192));
ALTER TABLE audit_system_events DROP CONSTRAINT audit_system_events_action_check, DROP CONSTRAINT audit_system_events_check;
ALTER TABLE audit_system_events ADD CONSTRAINT audit_system_events_action_check CHECK(action IN ('audit.pruned','audit.legacy_imported','support.telegram'));
ALTER TABLE audit_system_events ADD CONSTRAINT audit_system_events_check CHECK(
 (action='audit.pruned' AND period_day IS NOT NULL AND cutoff IS NOT NULL AND retention_days IS NOT NULL AND support_telegram IS NULL)
 OR (action='audit.legacy_imported' AND period_day IS NULL AND cutoff IS NULL AND retention_days IS NULL AND support_telegram IS NULL)
 OR (action='support.telegram' AND period_day IS NULL AND cutoff IS NULL AND retention_days IS NULL AND support_telegram IS NOT NULL));

CREATE TABLE legacy_support_imports (
 bot_id bigint NOT NULL CHECK(bot_id>0), group_id bigint NOT NULL CHECK(group_id<0), source_id bigint NOT NULL CHECK(source_id>0),
 source_hash bytea NOT NULL CHECK(octet_length(source_hash)=32),
 source_json bytea NOT NULL CHECK(octet_length(source_json)<=65536),
 PRIMARY KEY(bot_id,group_id,source_id)
);
CREATE TABLE support_telegram_topics (
 id uuid PRIMARY KEY,
 bot_id bigint NOT NULL CHECK(bot_id>0), group_id bigint NOT NULL CHECK(group_id<0),
 kind text NOT NULL CHECK(kind IN ('account','guest','orphan')),
 account_id uuid REFERENCES accounts(id), guest_tg_id bigint CHECK(guest_tg_id>0),
 thread_id bigint CHECK(thread_id>0),
 status text NOT NULL CHECK(status IN ('pending','sending','ready','failed','unknown','retired')),
 closed boolean NOT NULL DEFAULT false,
 support_banned boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 source_id bigint,
 CHECK((kind='account' AND account_id IS NOT NULL AND guest_tg_id IS NULL)
 OR (kind='guest' AND account_id IS NULL AND guest_tg_id IS NOT NULL)
 OR (kind='orphan' AND account_id IS NULL AND guest_tg_id IS NULL AND source_id IS NOT NULL)),
 CHECK(status<>'ready' OR thread_id IS NOT NULL),
 FOREIGN KEY(bot_id,group_id,source_id) REFERENCES legacy_support_imports(bot_id,group_id,source_id)
);
CREATE UNIQUE INDEX support_topic_account ON support_telegram_topics(bot_id,group_id,account_id) WHERE status<>'retired' AND account_id IS NOT NULL;
CREATE UNIQUE INDEX support_topic_guest ON support_telegram_topics(bot_id,group_id,guest_tg_id) WHERE status<>'retired' AND guest_tg_id IS NOT NULL;
CREATE UNIQUE INDEX support_topic_thread ON support_telegram_topics(bot_id,group_id,thread_id) WHERE status<>'retired' AND thread_id IS NOT NULL;
CREATE UNIQUE INDEX support_topic_legacy ON support_telegram_topics(bot_id,group_id,source_id) WHERE source_id IS NOT NULL AND status<>'retired';
CREATE TABLE support_telegram_guest_bans (
 telegram_id bigint PRIMARY KEY CHECK(telegram_id>0),
 banned boolean NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE TABLE support_telegram_receipts (
 id uuid PRIMARY KEY,
 bot_id bigint NOT NULL CHECK(bot_id>0), group_id bigint NOT NULL CHECK(group_id<0),
 chat_id bigint NOT NULL CHECK(chat_id<>0), message_id bigint NOT NULL CHECK(message_id>0),
 update_id bigint NOT NULL CHECK(update_id>=0), actor_tg_id bigint CHECK(actor_tg_id>0), thread_id bigint CHECK(thread_id>0),
 action text NOT NULL CHECK(char_length(action) BETWEEN 1 AND 64), callback_id text NOT NULL CHECK(char_length(callback_id)<=128),
 digest bytea NOT NULL CHECK(octet_length(digest)=32),
 actor_account_id uuid REFERENCES accounts(id), target_account_id uuid REFERENCES accounts(id),
 topic_id uuid REFERENCES support_telegram_topics(id), support_message_id uuid REFERENCES support_messages(id),
 owner_key uuid NOT NULL UNIQUE,
 result jsonb NOT NULL CHECK(jsonb_typeof(result)='object' AND octet_length(result::text)<=32768),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(bot_id,chat_id,message_id,action,callback_id)
);
CREATE TABLE support_telegram_deliveries (
 id uuid PRIMARY KEY,
 sequence bigint GENERATED ALWAYS AS IDENTITY NOT NULL UNIQUE,
 topic_id uuid REFERENCES support_telegram_topics(id),
 message_id uuid REFERENCES support_messages(id), receipt_id uuid REFERENCES support_telegram_receipts(id),
 kind text NOT NULL CHECK(kind IN ('message','topic_create','topic_action','ack')),
 status text NOT NULL CHECK(status IN ('queued','sending','sent','failed','unknown','skipped')),
 code text NOT NULL DEFAULT '' CHECK(code ~ '^[A-Z_]{0,48}$'),
 parts jsonb NOT NULL CHECK(jsonb_typeof(parts)='array' AND jsonb_array_length(parts) BETWEEN 1 AND 8 AND octet_length(parts::text)<=131072),
 lease uuid, lease_expires_at timestamptz,
 available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 prior_delivery_id uuid REFERENCES support_telegram_deliveries(id),
 CHECK((lease IS NULL)=(lease_expires_at IS NULL)),
 CHECK(topic_id IS NOT NULL OR (kind='ack' AND receipt_id IS NOT NULL))
);
CREATE UNIQUE INDEX support_active_message_delivery ON support_telegram_deliveries(message_id) WHERE status IN ('queued','sending') AND message_id IS NOT NULL;
CREATE INDEX support_delivery_pending ON support_telegram_deliveries(sequence) WHERE status IN ('queued','sending');

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM support_telegram_deliveries) OR EXISTS(SELECT 1 FROM support_telegram_receipts)
 OR EXISTS(SELECT 1 FROM support_telegram_topics) OR EXISTS(SELECT 1 FROM support_telegram_guest_bans)
 OR EXISTS(SELECT 1 FROM legacy_support_imports) OR EXISTS(SELECT 1 FROM audit_system_events WHERE support_telegram IS NOT NULL)
 OR EXISTS(SELECT 1 FROM audit_events WHERE operator_source IS NOT NULL) OR EXISTS(SELECT 1 FROM support_messages WHERE telegram_only IS NOT NULL) THEN
  RAISE EXCEPTION 'Support Telegram downgrade blocked: retained facts exist';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE support_telegram_deliveries,support_telegram_receipts,support_telegram_guest_bans,support_telegram_topics,legacy_support_imports;
ALTER TABLE support_messages DROP CONSTRAINT support_message_content, DROP COLUMN telegram_only;
ALTER TABLE support_messages ADD CONSTRAINT support_messages_check1 CHECK(text<>'' OR attachment_bytes IS NOT NULL);
ALTER TABLE audit_events DROP CONSTRAINT audit_single_operator, DROP COLUMN operator_source;
ALTER TABLE audit_events ADD CONSTRAINT audit_single_operator CHECK(operator_tg_id IS NULL OR operator_account_id IS NULL);
ALTER TABLE audit_system_events DROP CONSTRAINT audit_system_events_action_check, DROP CONSTRAINT audit_system_events_check, DROP COLUMN support_telegram;
ALTER TABLE audit_system_events ADD CONSTRAINT audit_system_events_action_check CHECK(action IN ('audit.pruned','audit.legacy_imported'));
ALTER TABLE audit_system_events ADD CONSTRAINT audit_system_events_check CHECK(
 (action='audit.pruned' AND period_day IS NOT NULL AND cutoff IS NOT NULL AND retention_days IS NOT NULL)
 OR (action='audit.legacy_imported' AND period_day IS NULL AND cutoff IS NULL AND retention_days IS NULL));
