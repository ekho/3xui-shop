-- +goose Up
ALTER TABLE purchase_orders DROP CONSTRAINT purchase_orders_payment_method_check;
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_orders_payment_method_check CHECK (
 (payment_method IN ('yoomoney','yookassa','cryptomus')
  AND ((payment_method='yoomoney' AND payment_type IN ('AC','PC'))
    OR (payment_method='yookassa' AND payment_type='YOOKASSA')
    OR (payment_method='cryptomus' AND payment_type='CRYPTOMUS' AND quote->>'currency'='USD'))
  AND manual_details IS NULL AND manual_reported_at IS NULL AND manual_decision IS NULL
  AND manual_decided_at IS NULL AND manual_actor_id IS NULL AND manual_reason IS NULL)
 OR (payment_method='manual' AND payment_type='MANUAL' AND manual_details IS NOT NULL
  AND char_length(btrim(manual_details)) BETWEEN 1 AND 2000));
CREATE TABLE cryptomus_checkouts (
 order_id uuid PRIMARY KEY REFERENCES purchase_orders(id),
 merchant_id text NOT NULL CHECK(char_length(merchant_id)=36),
 request bytea NOT NULL,
 first_attempt_at timestamptz,
 invoice_id uuid UNIQUE,
 checkout_url text,
 state text NOT NULL DEFAULT 'preparing' CHECK(state IN ('preparing','ready','unavailable')),
 observation jsonb CHECK(observation IS NULL OR jsonb_typeof(observation)='object'),
 CHECK(state<>'ready' OR (invoice_id IS NOT NULL AND checkout_url IS NOT NULL))
);
-- +goose StatementBegin
CREATE FUNCTION cryptomus_checkout_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF (NEW.order_id,NEW.merchant_id,NEW.request) IS DISTINCT FROM (OLD.order_id,OLD.merchant_id,OLD.request)
    OR (OLD.first_attempt_at IS NOT NULL AND NEW.first_attempt_at IS DISTINCT FROM OLD.first_attempt_at)
    OR (OLD.invoice_id IS NOT NULL AND NEW.invoice_id IS DISTINCT FROM OLD.invoice_id)
    OR (OLD.checkout_url IS NOT NULL AND NEW.checkout_url IS DISTINCT FROM OLD.checkout_url) THEN
  RAISE EXCEPTION 'provider request or identity is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER cryptomus_checkout_immutable BEFORE UPDATE ON cryptomus_checkouts FOR EACH ROW EXECUTE FUNCTION cryptomus_checkout_immutable();

ALTER TABLE purchase_receipts DROP CONSTRAINT purchase_provider_net, DROP CONSTRAINT purchase_provider_data;
ALTER TABLE purchase_receipts ADD CONSTRAINT purchase_provider_net CHECK (
 net_minor IS NOT NULL OR (provider_data IS NOT NULL AND COALESCE(
  (notification_type='yookassa.succeeded' AND provider_data->>'provider'='yookassa')
  OR (notification_type IN ('cryptomus.paid','cryptomus.paid_over') AND provider_data->>'provider'='cryptomus'),false)));
ALTER TABLE purchase_receipts ADD CONSTRAINT purchase_provider_data CHECK (
 provider_data IS NULL OR (jsonb_typeof(provider_data)='object' AND
  (notification_type='yookassa.succeeded' OR COALESCE(
   (notification_type IN ('cryptomus.paid','cryptomus.paid_over')
    AND provider_data->>'provider'='cryptomus' AND currency='USD' AND net_minor IS NULL
    AND notification_type='cryptomus.'||(provider_data->>'status')
    AND provider_data->>'payment_status'=provider_data->>'status'
    AND provider_data->'is_final'='true'::jsonb AND provider_data->>'currency'='USD'
    AND provider_data ?& ARRAY['invoice_id','merchant_id','order_id','amount_minor','payment_amount','payer_amount','merchant_amount','payer_currency','created_at']
    AND jsonb_typeof(provider_data->'amount_minor')='string' AND provider_data->>'amount_minor' ~ '^[0-9]{1,19}$'
    AND jsonb_typeof(provider_data->'payment_amount')='string' AND provider_data->>'payment_amount' ~ '^[0-9]{1,40}(\.[0-9]{1,40})?$'
    AND jsonb_typeof(provider_data->'payer_amount')='string' AND provider_data->>'payer_amount' ~ '^[0-9]{1,40}(\.[0-9]{1,40})?$'
    AND jsonb_typeof(provider_data->'merchant_amount')='string' AND provider_data->>'merchant_amount' ~ '^[0-9]{1,40}(\.[0-9]{1,40})?$'
    AND jsonb_typeof(provider_data->'payer_currency')='string' AND provider_data->>'payer_currency' ~ '^[A-Z0-9]{1,16}$'),false))));

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM cryptomus_checkouts) OR EXISTS(SELECT 1 FROM purchase_orders WHERE payment_method='cryptomus')
    OR EXISTS(SELECT 1 FROM purchase_receipts WHERE notification_type IN ('cryptomus.paid','cryptomus.paid_over')) THEN
  RAISE EXCEPTION 'provider downgrade blocked: retained payment history';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE purchase_receipts DROP CONSTRAINT purchase_provider_data, DROP CONSTRAINT purchase_provider_net;
ALTER TABLE purchase_receipts ADD CONSTRAINT purchase_provider_net CHECK (
 net_minor IS NOT NULL OR (provider_data IS NOT NULL AND notification_type='yookassa.succeeded'
                          AND COALESCE(provider_data->>'provider'='yookassa',false)));
ALTER TABLE purchase_receipts ADD CONSTRAINT purchase_provider_data CHECK (
 provider_data IS NULL OR (jsonb_typeof(provider_data)='object' AND notification_type='yookassa.succeeded'));
DROP FUNCTION cryptomus_checkout_immutable() CASCADE;
DROP TABLE cryptomus_checkouts;
ALTER TABLE purchase_orders DROP CONSTRAINT purchase_orders_payment_method_check;
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_orders_payment_method_check CHECK (
 (payment_method IN ('yoomoney','yookassa')
  AND ((payment_method='yoomoney' AND payment_type IN ('AC','PC')) OR (payment_method='yookassa' AND payment_type='YOOKASSA'))
  AND manual_details IS NULL AND manual_reported_at IS NULL AND manual_decision IS NULL
  AND manual_decided_at IS NULL AND manual_actor_id IS NULL AND manual_reason IS NULL)
 OR (payment_method='manual' AND payment_type='MANUAL' AND manual_details IS NOT NULL
  AND char_length(btrim(manual_details)) BETWEEN 1 AND 2000));
