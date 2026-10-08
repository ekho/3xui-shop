-- +goose Up
CREATE TABLE reminders (
 id uuid PRIMARY KEY,
 account_id uuid NOT NULL REFERENCES accounts(id),
 kind text NOT NULL CHECK(kind IN ('expiry','traffic','stars_lapsed')),
 period text NOT NULL CHECK(char_length(period) BETWEEN 1 AND 96),
 threshold integer NOT NULL,
 observed_at timestamptz NOT NULL,
 expiry_ms bigint NOT NULL DEFAULT 0 CHECK(expiry_ms>=0),
 used_bytes bigint NOT NULL DEFAULT 0 CHECK(used_bytes>=0),
 limit_bytes bigint NOT NULL DEFAULT 0 CHECK(limit_bytes>=0),
 paid_until timestamptz,
 dismissed_at timestamptz,
 email_queued boolean NOT NULL DEFAULT false,
 telegram_queued boolean NOT NULL DEFAULT false,
 email_credential_version bigint CHECK(email_credential_version>=0),
 UNIQUE(account_id,kind,period,threshold),
 CHECK((kind='expiry' AND threshold IN (1,3) AND expiry_ms>0 AND paid_until IS NULL)
    OR (kind='traffic' AND threshold IN (80,100) AND limit_bytes>0 AND paid_until IS NULL)
    OR (kind='stars_lapsed' AND threshold=0 AND paid_until IS NOT NULL)),
 CHECK(email_queued=(email_credential_version IS NOT NULL))
);
CREATE INDEX reminder_account ON reminders(account_id,observed_at DESC,id);
CREATE TABLE reminder_preferences (
 account_id uuid PRIMARY KEY REFERENCES accounts(id),
 email_enabled boolean NOT NULL DEFAULT false,
 updated_at timestamptz NOT NULL
);
ALTER TABLE client_telegram_deliveries ADD COLUMN reminder_id uuid REFERENCES reminders(id);
ALTER TABLE client_telegram_deliveries DROP CONSTRAINT client_telegram_deliveries_route_check;
ALTER TABLE client_telegram_deliveries ADD CONSTRAINT client_telegram_deliveries_route_check
 CHECK(route IN ('cabinet','history','support','renew') OR route ~ '^orders:[0-9a-f-]{36}$');
ALTER TABLE mail_deliveries ADD COLUMN reminder_id uuid REFERENCES reminders(id);
ALTER TABLE mail_deliveries DROP CONSTRAINT mail_deliveries_kind_check;
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_deliveries_kind_check CHECK(kind IN ('registration','credential','security_notice','reminder'));
ALTER TABLE mail_deliveries DROP CONSTRAINT mail_proof_kind;
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_proof_kind CHECK(
 (kind='reminder' AND reminder_id IS NOT NULL AND challenge_id IS NULL AND credential_challenge_id IS NULL)
 OR (kind<>'reminder' AND reminder_id IS NULL AND (
 (kind='registration' AND credential_challenge_id IS NULL)
 OR (kind='credential' AND challenge_id IS NULL AND credential_challenge_id IS NOT NULL)
 OR (kind='security_notice' AND challenge_id IS NULL AND credential_challenge_id IS NULL)))
);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM reminders) OR EXISTS(SELECT 1 FROM reminder_preferences)
 OR EXISTS(SELECT 1 FROM client_telegram_deliveries WHERE route='renew' OR reminder_id IS NOT NULL)
 OR EXISTS(SELECT 1 FROM mail_deliveries WHERE kind='reminder' OR reminder_id IS NOT NULL) THEN
  RAISE EXCEPTION 'Reminder downgrade blocked: retained facts exist';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE mail_deliveries DROP CONSTRAINT mail_proof_kind;
ALTER TABLE mail_deliveries DROP CONSTRAINT mail_deliveries_kind_check;
ALTER TABLE mail_deliveries DROP COLUMN reminder_id;
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_deliveries_kind_check CHECK(kind IN ('registration','credential','security_notice'));
ALTER TABLE mail_deliveries ADD CONSTRAINT mail_proof_kind CHECK(
 (kind='registration' AND credential_challenge_id IS NULL)
 OR (kind='credential' AND challenge_id IS NULL AND credential_challenge_id IS NOT NULL)
 OR (kind='security_notice' AND challenge_id IS NULL AND credential_challenge_id IS NULL));
ALTER TABLE client_telegram_deliveries DROP CONSTRAINT client_telegram_deliveries_route_check;
ALTER TABLE client_telegram_deliveries ADD CONSTRAINT client_telegram_deliveries_route_check
 CHECK(route IN ('cabinet','history','support') OR route ~ '^orders:[0-9a-f-]{36}$');
ALTER TABLE client_telegram_deliveries DROP COLUMN reminder_id;
DROP TABLE reminder_preferences;
DROP TABLE reminders;
