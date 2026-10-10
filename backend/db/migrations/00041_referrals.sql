-- +goose Up
CREATE TABLE referral_links (
 account_id uuid PRIMARY KEY REFERENCES accounts(id),
 code text NOT NULL UNIQUE CHECK(code ~ '^r_[0-9a-f]{32}$'),
 created_at timestamptz NOT NULL
);
CREATE TABLE referrals (
 id uuid PRIMARY KEY,
 legacy_referral_id bigint UNIQUE CHECK(legacy_referral_id > 0),
 referrer_account_id uuid NOT NULL REFERENCES accounts(id),
 referred_account_id uuid NOT NULL UNIQUE REFERENCES accounts(id),
 created_at timestamptz NOT NULL,
 referred_rewarded_at timestamptz,
 referred_bonus_days integer CHECK(referred_bonus_days >= 0),
 CHECK(referrer_account_id <> referred_account_id)
);
CREATE INDEX referrals_referrer ON referrals(referrer_account_id);
CREATE TABLE referrer_rewards (
 id uuid PRIMARY KEY,
 legacy_reward_id bigint UNIQUE CHECK(legacy_reward_id > 0),
 account_id uuid NOT NULL REFERENCES accounts(id),
 reward_type text NOT NULL CHECK(reward_type IN ('DAYS','MONEY')),
 reward_level smallint CHECK(reward_level IN (1,2)),
 amount numeric(38,18) NOT NULL CHECK(amount >= 0),
 payment_id varchar(64) NOT NULL CHECK(length(payment_id) > 0),
 created_at timestamptz NOT NULL,
 rewarded_at timestamptz,
 UNIQUE(account_id,payment_id),
 CHECK(reward_type <> 'DAYS' OR amount = trunc(amount))
);
CREATE INDEX referrer_rewards_account_level ON referrer_rewards(account_id,reward_level);

-- +goose StatementBegin
CREATE FUNCTION guard_referral_relationship() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' THEN
  RAISE EXCEPTION 'referral relationships are immutable' USING ERRCODE='23514';
 END IF;
 IF TG_OP='UPDATE' THEN
  IF (NEW.id,NEW.legacy_referral_id,NEW.referrer_account_id,NEW.referred_account_id,NEW.created_at)
   IS DISTINCT FROM (OLD.id,OLD.legacy_referral_id,OLD.referrer_account_id,OLD.referred_account_id,OLD.created_at) THEN
   RAISE EXCEPTION 'referral relationships are immutable' USING ERRCODE='23514';
  END IF;
  RETURN NEW;
 END IF;
 -- A fresh statement snapshot after this lock observes the preceding graph writer.
 IF current_setting('transaction_isolation')='repeatable read' THEN
  RAISE EXCEPTION 'referral writes require read committed or serializable' USING ERRCODE='23514';
 END IF;
 PERFORM pg_advisory_xact_lock(hashtextextended('bonuses.referral-graph',0));
 IF NEW.referrer_account_id=NEW.referred_account_id OR EXISTS(
  WITH RECURSIVE ancestors(account_id) AS (
   SELECT NEW.referrer_account_id
   UNION
   SELECT r.referrer_account_id FROM referrals r JOIN ancestors a ON r.referred_account_id=a.account_id
  ) SELECT 1 FROM ancestors WHERE account_id=NEW.referred_account_id
 ) THEN
  RAISE EXCEPTION 'referral cycle blocked' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER referral_relationship_guard BEFORE INSERT OR UPDATE OR DELETE ON referrals
 FOR EACH ROW EXECUTE FUNCTION guard_referral_relationship();

-- +goose StatementBegin
CREATE FUNCTION guard_referral_link() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'personal referral links are immutable' USING ERRCODE='23514';
END $$;
-- +goose StatementEnd
CREATE TRIGGER referral_link_guard BEFORE UPDATE OR DELETE ON referral_links
 FOR EACH ROW EXECUTE FUNCTION guard_referral_link();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM referral_links) OR EXISTS(SELECT 1 FROM referrals) OR EXISTS(SELECT 1 FROM referrer_rewards) THEN
  RAISE EXCEPTION 'referral downgrade blocked: retained links, relationships or rewards';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE referrer_rewards;
DROP TABLE referrals;
DROP TABLE referral_links;
DROP FUNCTION guard_referral_relationship();
DROP FUNCTION guard_referral_link();
