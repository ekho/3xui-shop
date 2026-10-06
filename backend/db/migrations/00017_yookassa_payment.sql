-- +goose Up
ALTER TABLE purchase_orders DROP CONSTRAINT purchase_orders_payment_method_check;
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_orders_payment_method_check CHECK (
 (payment_method IN ('yoomoney','yookassa')
  AND ((payment_method='yoomoney' AND payment_type IN ('AC','PC')) OR (payment_method='yookassa' AND payment_type='YOOKASSA'))
  AND manual_details IS NULL AND manual_reported_at IS NULL AND manual_decision IS NULL
  AND manual_decided_at IS NULL AND manual_actor_id IS NULL AND manual_reason IS NULL)
 OR (payment_method='manual' AND payment_type='MANUAL' AND manual_details IS NOT NULL
  AND char_length(btrim(manual_details)) BETWEEN 1 AND 2000));
CREATE TABLE yookassa_checkouts (
 order_id uuid PRIMARY KEY REFERENCES purchase_orders(id),
 shop_id text NOT NULL CHECK(char_length(shop_id) BETWEEN 1 AND 20),
 test_mode boolean NOT NULL,
 request bytea NOT NULL,
 first_attempt_at timestamptz,
 payment_id uuid UNIQUE,
 confirmation_url text,
 income_minor bigint CHECK(income_minor IS NULL OR income_minor>=0),
 state text NOT NULL DEFAULT 'preparing' CHECK(state IN ('preparing','ready','unavailable')),
 observation jsonb CHECK(observation IS NULL OR jsonb_typeof(observation)='object'),
 CHECK(state<>'ready' OR (payment_id IS NOT NULL AND confirmation_url IS NOT NULL))
);
-- +goose StatementBegin
CREATE FUNCTION yookassa_checkout_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.order_id,NEW.shop_id,NEW.test_mode,NEW.request) IS DISTINCT FROM (OLD.order_id,OLD.shop_id,OLD.test_mode,OLD.request)
    OR (OLD.first_attempt_at IS NOT NULL AND NEW.first_attempt_at IS DISTINCT FROM OLD.first_attempt_at)
    OR (OLD.payment_id IS NOT NULL AND NEW.payment_id IS DISTINCT FROM OLD.payment_id)
    OR (OLD.confirmation_url IS NOT NULL AND NEW.confirmation_url IS DISTINCT FROM OLD.confirmation_url)
    OR (OLD.income_minor IS NOT NULL AND NEW.income_minor IS DISTINCT FROM OLD.income_minor) THEN
  RAISE EXCEPTION 'provider request or identity is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER yookassa_checkout_immutable BEFORE UPDATE ON yookassa_checkouts FOR EACH ROW EXECUTE FUNCTION yookassa_checkout_immutable();
ALTER TABLE purchase_receipts ALTER COLUMN net_minor DROP NOT NULL;
ALTER TABLE purchase_receipts ADD COLUMN provider_data jsonb;
ALTER TABLE purchase_receipts ADD CONSTRAINT purchase_provider_net CHECK (
 net_minor IS NOT NULL OR (provider_data IS NOT NULL AND notification_type='yookassa.succeeded'
                          AND COALESCE(provider_data->>'provider'='yookassa',false)));
ALTER TABLE purchase_receipts ADD CONSTRAINT purchase_provider_data CHECK (
 provider_data IS NULL OR (jsonb_typeof(provider_data)='object' AND notification_type='yookassa.succeeded'));
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION purchase_receipt_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.operation_id,NEW.order_id,NEW.occurred_at,NEW.gross_minor,NEW.net_minor,NEW.currency,NEW.notification_type,NEW.codepro,NEW.unaccepted,NEW.created_at,NEW.provider_data)
    IS DISTINCT FROM (OLD.operation_id,OLD.order_id,OLD.occurred_at,OLD.gross_minor,OLD.net_minor,OLD.currency,OLD.notification_type,OLD.codepro,OLD.unaccepted,OLD.created_at,OLD.provider_data) THEN
  RAISE EXCEPTION 'purchase receipt is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM yookassa_checkouts) OR EXISTS(SELECT 1 FROM purchase_orders WHERE payment_method='yookassa')
    OR EXISTS(SELECT 1 FROM purchase_receipts WHERE provider_data IS NOT NULL) THEN
  RAISE EXCEPTION 'provider downgrade blocked: retained payment history';
 END IF;
END $$;
-- +goose StatementEnd
DROP FUNCTION yookassa_checkout_immutable() CASCADE;
DROP TABLE yookassa_checkouts;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION purchase_receipt_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.operation_id,NEW.order_id,NEW.occurred_at,NEW.gross_minor,NEW.net_minor,NEW.currency,NEW.notification_type,NEW.codepro,NEW.unaccepted,NEW.created_at)
    IS DISTINCT FROM (OLD.operation_id,OLD.order_id,OLD.occurred_at,OLD.gross_minor,OLD.net_minor,OLD.currency,OLD.notification_type,OLD.codepro,OLD.unaccepted,OLD.created_at) THEN
  RAISE EXCEPTION 'purchase receipt is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
ALTER TABLE purchase_receipts DROP CONSTRAINT purchase_provider_data, DROP CONSTRAINT purchase_provider_net;
ALTER TABLE purchase_receipts DROP COLUMN provider_data;
ALTER TABLE purchase_receipts ALTER COLUMN net_minor SET NOT NULL;
ALTER TABLE purchase_orders DROP CONSTRAINT purchase_orders_payment_method_check;
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_orders_payment_method_check CHECK (
 (payment_method='yoomoney' AND payment_type IN ('AC','PC') AND manual_details IS NULL AND manual_reported_at IS NULL
  AND manual_decision IS NULL AND manual_decided_at IS NULL AND manual_actor_id IS NULL AND manual_reason IS NULL)
 OR (payment_method='manual' AND payment_type='MANUAL' AND manual_details IS NOT NULL
  AND char_length(btrim(manual_details)) BETWEEN 1 AND 2000));
