-- +goose Up
CREATE TABLE notice_previews (
 id uuid PRIMARY KEY,
 operator_account_id uuid NOT NULL REFERENCES accounts(id),
 notice_id uuid NOT NULL,
 mode text NOT NULL CHECK(mode IN ('send','edit','delete')),
 audience text NOT NULL CHECK(audience IN ('personal','all')),
 html text NOT NULL,
 plain_text text NOT NULL,
 reason text NOT NULL CHECK(char_length(reason) BETWEEN 1 AND 512),
 expected_revision integer NOT NULL CHECK(expected_revision>=0 AND expected_revision<2147483647),
 recipient_snapshot jsonb NOT NULL CHECK(jsonb_typeof(recipient_snapshot)='array'),
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 superseded_at timestamptz,
 confirmed_at timestamptz,
 CHECK(expires_at>created_at),
 CHECK((mode='send' AND expected_revision=0) OR (mode<>'send' AND expected_revision>0))
);
CREATE INDEX notice_preview_owner ON notice_previews(operator_account_id,created_at DESC,id);
CREATE TABLE notices (
 id uuid PRIMARY KEY REFERENCES notice_previews(id),
 sequence bigserial UNIQUE NOT NULL,
 operator_account_id uuid NOT NULL REFERENCES accounts(id),
 current_preview_id uuid NOT NULL REFERENCES notice_previews(id),
 revision integer NOT NULL CHECK(revision>0),
 deleted boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL
);
CREATE INDEX notice_owner_last ON notices(operator_account_id,sequence DESC);
CREATE TABLE notice_recipients (
 id uuid PRIMARY KEY,
 notice_id uuid NOT NULL REFERENCES notices(id),
 account_id uuid NOT NULL REFERENCES accounts(id),
 credential_version bigint NOT NULL CHECK(credential_version>=0),
 telegram_id bigint NOT NULL CHECK(telegram_id>=0 AND telegram_id<4503599627370496),
 locale text NOT NULL CHECK(locale IN ('ru','en')),
 email_hash bytea NOT NULL CHECK(octet_length(email_hash)=32),
 email_enabled boolean NOT NULL,
 cabinet_visible boolean NOT NULL,
 dismissed_at timestamptz,
 UNIQUE(notice_id,account_id)
);
CREATE INDEX notice_recipient_account ON notice_recipients(account_id,notice_id);
CREATE TABLE notice_actions (
 id uuid PRIMARY KEY,
 preview_id uuid NOT NULL REFERENCES notice_previews(id),
 recipient_id uuid NOT NULL REFERENCES notice_recipients(id),
 cabinet_state text NOT NULL CHECK(cabinet_state IN ('succeeded','skipped','unchanged')),
 telegram_state text NOT NULL CHECK(telegram_state IN ('pending','succeeded','failed','skipped','unknown','unchanged')),
 email_state text NOT NULL CHECK(email_state IN ('pending','succeeded','failed','skipped','unknown','unchanged')),
 email_started_at timestamptz,
 telegram_started_at timestamptz,
 telegram_message_at timestamptz,
 UNIQUE(preview_id,recipient_id)
);
CREATE TABLE notice_preferences (
 account_id uuid PRIMARY KEY REFERENCES accounts(id),
 email_enabled boolean NOT NULL DEFAULT false,
 updated_at timestamptz NOT NULL
);
ALTER TABLE client_telegram_deliveries ADD COLUMN notice_action_id uuid REFERENCES notice_actions(id);
ALTER TABLE client_telegram_deliveries ADD COLUMN prior_delivery_id uuid REFERENCES client_telegram_deliveries(id);
ALTER TABLE client_telegram_deliveries ADD COLUMN notice_closed_at timestamptz;
ALTER TABLE client_telegram_deliveries ADD COLUMN notice_close_state text CHECK(notice_close_state IN ('succeeded','failed','unknown'));
ALTER TABLE client_telegram_deliveries ADD CONSTRAINT client_notice_reference CHECK(
 (notice_action_id IS NULL AND prior_delivery_id IS NULL AND notice_closed_at IS NULL AND notice_close_state IS NULL) OR (notice_action_id IS NOT NULL AND reminder_id IS NULL AND route='cabinet')
);
ALTER TABLE mail_deliveries ADD COLUMN notice_action_id uuid REFERENCES notice_actions(id);
ALTER TABLE mail_deliveries DROP CONSTRAINT mail_deliveries_kind_check;
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_deliveries_kind_check CHECK(kind IN ('registration','credential','security_notice','reminder','operator_notice'));
ALTER TABLE mail_deliveries DROP CONSTRAINT mail_proof_kind;
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_proof_kind CHECK(
 (kind='operator_notice' AND notice_action_id IS NOT NULL AND reminder_id IS NULL AND challenge_id IS NULL AND credential_challenge_id IS NULL)
 OR (kind='reminder' AND notice_action_id IS NULL AND reminder_id IS NOT NULL AND challenge_id IS NULL AND credential_challenge_id IS NULL)
 OR (kind NOT IN ('reminder','operator_notice') AND notice_action_id IS NULL AND reminder_id IS NULL AND (
 (kind='registration' AND credential_challenge_id IS NULL)
 OR (kind='credential' AND challenge_id IS NULL AND credential_challenge_id IS NOT NULL)
 OR (kind='security_notice' AND challenge_id IS NULL AND credential_challenge_id IS NULL)))
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM notice_previews) OR EXISTS(SELECT 1 FROM notices)
 OR EXISTS(SELECT 1 FROM notice_recipients) OR EXISTS(SELECT 1 FROM notice_actions)
 OR EXISTS(SELECT 1 FROM notice_preferences)
 OR EXISTS(SELECT 1 FROM client_telegram_deliveries WHERE notice_action_id IS NOT NULL OR prior_delivery_id IS NOT NULL)
 OR EXISTS(SELECT 1 FROM mail_deliveries WHERE kind='operator_notice' OR notice_action_id IS NOT NULL) THEN
  RAISE EXCEPTION 'Notice downgrade blocked: retained facts exist';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE client_telegram_deliveries DROP CONSTRAINT client_notice_reference;
ALTER TABLE client_telegram_deliveries DROP COLUMN notice_close_state;
ALTER TABLE client_telegram_deliveries DROP COLUMN notice_closed_at;
ALTER TABLE client_telegram_deliveries DROP COLUMN prior_delivery_id;
ALTER TABLE client_telegram_deliveries DROP COLUMN notice_action_id;
ALTER TABLE mail_deliveries DROP CONSTRAINT mail_proof_kind;
ALTER TABLE mail_deliveries DROP CONSTRAINT mail_deliveries_kind_check;
ALTER TABLE mail_deliveries DROP COLUMN notice_action_id;
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_deliveries_kind_check CHECK(kind IN ('registration','credential','security_notice','reminder'));
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_proof_kind CHECK(
 (kind='reminder' AND reminder_id IS NOT NULL AND challenge_id IS NULL AND credential_challenge_id IS NULL)
 OR (kind<>'reminder' AND reminder_id IS NULL AND (
 (kind='registration' AND credential_challenge_id IS NULL)
 OR (kind='credential' AND challenge_id IS NULL AND credential_challenge_id IS NOT NULL)
 OR (kind='security_notice' AND challenge_id IS NULL AND credential_challenge_id IS NULL)))
);
DROP TABLE notice_preferences;
DROP TABLE notice_actions;
DROP TABLE notice_recipients;
DROP TABLE notices;
DROP TABLE notice_previews;
