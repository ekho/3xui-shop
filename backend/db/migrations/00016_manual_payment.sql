-- +goose Up
ALTER TABLE purchase_orders
 ADD COLUMN payment_method text NOT NULL DEFAULT 'yoomoney',
 ADD COLUMN manual_details text,
 ADD COLUMN manual_reported_at timestamptz,
 ADD COLUMN manual_decision text,
 ADD COLUMN manual_decided_at timestamptz,
 ADD COLUMN manual_actor_id uuid REFERENCES accounts(id),
 ADD COLUMN manual_reason text;
ALTER TABLE purchase_orders DROP CONSTRAINT purchase_orders_payment_type_check;
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_orders_payment_method_check CHECK (
 (payment_method='yoomoney' AND payment_type IN ('AC','PC') AND manual_details IS NULL AND manual_reported_at IS NULL
  AND manual_decision IS NULL AND manual_decided_at IS NULL AND manual_actor_id IS NULL AND manual_reason IS NULL)
 OR (payment_method='manual' AND payment_type='MANUAL' AND manual_details IS NOT NULL
  AND char_length(btrim(manual_details)) BETWEEN 1 AND 2000));
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_manual_report_check CHECK (
 manual_reported_at IS NULL OR (payment_method='manual' AND manual_reported_at>=created_at AND manual_reported_at<expires_at));
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_manual_decision_check CHECK (
 (manual_decision IS NULL AND manual_decided_at IS NULL AND manual_actor_id IS NULL AND manual_reason IS NULL)
 OR (payment_method='manual' AND manual_decision IS NOT NULL AND manual_reported_at IS NOT NULL AND manual_actor_id IS NOT NULL
     AND manual_decided_at IS NOT NULL AND manual_decided_at>=manual_reported_at AND manual_reason IS NOT NULL
     AND char_length(btrim(manual_reason)) BETWEEN 1 AND 1000
     AND ((manual_decision='approved' AND payment_status='paid') OR (manual_decision='rejected' AND payment_status='canceled'))));
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_manual_paid_check CHECK (
 payment_method<>'manual' OR payment_status<>'paid' OR COALESCE(manual_decision='approved',false));
CREATE INDEX purchase_manual_pending ON purchase_orders(manual_reported_at,id)
 WHERE payment_method='manual' AND manual_reported_at IS NOT NULL AND manual_decision IS NULL
 AND payment_status='pending' AND NOT review_required AND fulfillment_status='not_started' AND active;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION purchase_quote_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.account_id,NEW.idempotency_key,NEW.body_hash,NEW.quote,NEW.amount_minor,NEW.payment_type,NEW.created_at,NEW.expires_at,NEW.payment_method,NEW.manual_details)
    IS DISTINCT FROM (OLD.account_id,OLD.idempotency_key,OLD.body_hash,OLD.quote,OLD.amount_minor,OLD.payment_type,OLD.created_at,OLD.expires_at,OLD.payment_method,OLD.manual_details)
    OR (OLD.funding_operation_id IS NOT NULL AND NEW.funding_operation_id IS DISTINCT FROM OLD.funding_operation_id)
    OR (OLD.payment_status='paid' AND NEW.payment_status<>'paid') THEN
  RAISE EXCEPTION 'purchase quote or funding is immutable';
 END IF;
 IF OLD.manual_reported_at IS NOT NULL AND NEW.manual_reported_at IS DISTINCT FROM OLD.manual_reported_at
    OR OLD.manual_decision IS NOT NULL AND (NEW.manual_decision,NEW.manual_actor_id,NEW.manual_decided_at,NEW.manual_reason)
       IS DISTINCT FROM (OLD.manual_decision,OLD.manual_actor_id,OLD.manual_decided_at,OLD.manual_reason) THEN
  RAISE EXCEPTION 'manual payment claim or decision is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM purchase_orders WHERE payment_method='manual') THEN
  RAISE EXCEPTION 'manual payment downgrade blocked: retained claim or financial history';
 END IF;
END $$;
-- +goose StatementEnd
DROP INDEX purchase_manual_pending;
ALTER TABLE purchase_orders DROP CONSTRAINT purchase_manual_paid_check;
ALTER TABLE purchase_orders DROP CONSTRAINT purchase_manual_decision_check;
ALTER TABLE purchase_orders DROP CONSTRAINT purchase_manual_report_check;
ALTER TABLE purchase_orders DROP CONSTRAINT purchase_orders_payment_method_check;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION purchase_quote_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.account_id,NEW.idempotency_key,NEW.body_hash,NEW.quote,NEW.amount_minor,NEW.payment_type,NEW.created_at,NEW.expires_at)
    IS DISTINCT FROM (OLD.account_id,OLD.idempotency_key,OLD.body_hash,OLD.quote,OLD.amount_minor,OLD.payment_type,OLD.created_at,OLD.expires_at)
    OR (OLD.funding_operation_id IS NOT NULL AND NEW.funding_operation_id IS DISTINCT FROM OLD.funding_operation_id)
    OR (OLD.payment_status='paid' AND NEW.payment_status<>'paid') THEN
  RAISE EXCEPTION 'purchase quote or funding is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
ALTER TABLE purchase_orders DROP COLUMN manual_reason, DROP COLUMN manual_actor_id, DROP COLUMN manual_decided_at,
 DROP COLUMN manual_decision, DROP COLUMN manual_reported_at, DROP COLUMN manual_details, DROP COLUMN payment_method;
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_orders_payment_type_check CHECK(payment_type IN ('AC','PC'));
