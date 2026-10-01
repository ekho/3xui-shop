-- +goose Up
CREATE TABLE accounts (
  id uuid PRIMARY KEY,
  email_key text NOT NULL UNIQUE,
  locale text NOT NULL CHECK (locale IN ('ru','en')),
  password_hash text NOT NULL,
  verified_at timestamptz NOT NULL,
  restricted boolean NOT NULL DEFAULT false,
  vpn_id uuid NOT NULL UNIQUE,
  sub_id text NOT NULL UNIQUE CHECK (sub_id ~ '^[0-9a-z]{16}$'),
  panel_key text NOT NULL UNIQUE,
  terms_version text NOT NULL,
  privacy_version text NOT NULL,
  telegram_id bigint UNIQUE,
  legacy_user_id bigint UNIQUE
);
CREATE TABLE registration_challenges (
  id uuid PRIMARY KEY,
  email_key text NOT NULL,
  locale text NOT NULL CHECK (locale IN ('ru','en')),
  terms_version text NOT NULL,
  privacy_version text NOT NULL,
  token_hash bytea NOT NULL UNIQUE,
  code_hash bytea NOT NULL,
  created_at timestamptz NOT NULL,
  token_expires_at timestamptz NOT NULL,
  code_expires_at timestamptz NOT NULL,
  failed_guesses integer NOT NULL DEFAULT 0 CHECK (failed_guesses BETWEEN 0 AND 5),
  revoked boolean NOT NULL DEFAULT false
);
CREATE UNIQUE INDEX one_live_registration ON registration_challenges(email_key) WHERE NOT revoked;
CREATE TABLE mail_deliveries (
  id uuid PRIMARY KEY,
  challenge_id uuid REFERENCES registration_challenges(id),
  email_key text NOT NULL,
  ciphertext bytea,
  created_at timestamptz NOT NULL,
  delivered_at timestamptz
);
-- +goose Down
DROP TABLE mail_deliveries;
DROP TABLE registration_challenges;
DROP TABLE accounts;
