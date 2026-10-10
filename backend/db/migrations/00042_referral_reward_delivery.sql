-- +goose Up
ALTER TABLE referrer_rewards
 ADD COLUMN source_order_id uuid REFERENCES purchase_orders(id),
 ADD COLUMN access_operation_id uuid UNIQUE REFERENCES access_operations(id),
 ADD CONSTRAINT native_referral_reward CHECK(source_order_id IS NULL OR
  (reward_type='DAYS' AND reward_level IS NOT NULL AND amount BETWEEN 1 AND 365
   AND payment_id='order:'||source_order_id::text AND legacy_reward_id IS NULL)),
 ADD CONSTRAINT referral_reward_access_source CHECK(access_operation_id IS NULL OR source_order_id IS NOT NULL);
CREATE UNIQUE INDEX referrer_rewards_native_source ON referrer_rewards(account_id,source_order_id)
 WHERE source_order_id IS NOT NULL;

-- +goose StatementBegin
CREATE FUNCTION guard_native_referral_reward() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.source_order_id IS NOT NULL THEN
  IF TG_OP='DELETE' THEN
   RAISE EXCEPTION 'native referral rewards are retained' USING ERRCODE='23514';
  END IF;
  IF (NEW.id,NEW.legacy_reward_id,NEW.account_id,NEW.reward_type,NEW.reward_level,NEW.amount,NEW.payment_id,NEW.created_at,NEW.source_order_id)
   IS DISTINCT FROM (OLD.id,OLD.legacy_reward_id,OLD.account_id,OLD.reward_type,OLD.reward_level,OLD.amount,OLD.payment_id,OLD.created_at,OLD.source_order_id)
   OR OLD.access_operation_id IS NOT NULL AND NEW.access_operation_id IS DISTINCT FROM OLD.access_operation_id
   OR OLD.rewarded_at IS NOT NULL AND NEW.rewarded_at IS DISTINCT FROM OLD.rewarded_at THEN
   RAISE EXCEPTION 'native referral reward facts are immutable' USING ERRCODE='23514';
  END IF;
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER native_referral_reward_guard BEFORE UPDATE OR DELETE ON referrer_rewards
 FOR EACH ROW EXECUTE FUNCTION guard_native_referral_reward();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM referrer_rewards WHERE source_order_id IS NOT NULL OR access_operation_id IS NOT NULL) THEN
  RAISE EXCEPTION 'referral delivery downgrade blocked: retained native rewards';
 END IF;
END $$;
-- +goose StatementEnd
DROP TRIGGER native_referral_reward_guard ON referrer_rewards;
DROP FUNCTION guard_native_referral_reward();
DROP INDEX referrer_rewards_native_source;
ALTER TABLE referrer_rewards DROP CONSTRAINT referral_reward_access_source,
 DROP CONSTRAINT native_referral_reward, DROP COLUMN access_operation_id, DROP COLUMN source_order_id;
