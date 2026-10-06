-- +goose Up
ALTER TABLE purchase_orders DROP CONSTRAINT purchase_orders_payment_method_check;
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_orders_payment_method_check CHECK (
 (payment_method IN ('yoomoney','yookassa','cryptomus','heleket')
  AND ((payment_method='yoomoney' AND payment_type IN ('AC','PC'))
    OR (payment_method='yookassa' AND payment_type='YOOKASSA')
    OR (payment_method='heleket' AND payment_type='HELEKET' AND quote->>'currency'='USD')
    OR (payment_method='cryptomus' AND payment_type='CRYPTOMUS' AND quote->>'currency'='USD'))
  AND manual_details IS NULL AND manual_reported_at IS NULL AND manual_decision IS NULL
  AND manual_decided_at IS NULL AND manual_actor_id IS NULL AND manual_reason IS NULL)
 OR (payment_method='manual' AND payment_type='MANUAL' AND manual_details IS NOT NULL
  AND char_length(btrim(manual_details)) BETWEEN 1 AND 2000));
CREATE TABLE heleket_checkouts (
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
CREATE TRIGGER heleket_checkout_immutable BEFORE UPDATE ON heleket_checkouts FOR EACH ROW EXECUTE FUNCTION cryptomus_checkout_immutable();

ALTER TABLE purchase_receipts DROP CONSTRAINT purchase_provider_net, DROP CONSTRAINT purchase_provider_data;
ALTER TABLE purchase_receipts ADD CONSTRAINT purchase_provider_net CHECK (
 net_minor IS NOT NULL OR (provider_data IS NOT NULL AND COALESCE(
  (notification_type='yookassa.succeeded' AND provider_data->>'provider'='yookassa')
  OR (notification_type IN ('cryptomus.paid','cryptomus.paid_over','heleket.paid','heleket.paid_over') AND provider_data->>'provider' IN ('cryptomus','heleket')),false)));
ALTER TABLE purchase_receipts ADD CONSTRAINT purchase_provider_data CHECK (
 provider_data IS NULL OR (jsonb_typeof(provider_data)='object' AND
  (notification_type='yookassa.succeeded' OR COALESCE(
   (notification_type IN ('cryptomus.paid','cryptomus.paid_over','heleket.paid','heleket.paid_over')
    AND provider_data->>'provider' IN ('cryptomus','heleket') AND currency='USD' AND net_minor IS NULL
    AND notification_type=(provider_data->>'provider')||'.'||(provider_data->>'status')
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
 IF EXISTS(SELECT 1 FROM heleket_checkouts) OR EXISTS(SELECT 1 FROM purchase_orders WHERE payment_method='heleket')
    OR EXISTS(SELECT 1 FROM purchase_receipts WHERE notification_type IN ('heleket.paid','heleket.paid_over')) THEN
  RAISE EXCEPTION 'provider downgrade blocked: retained payment history';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE heleket_checkouts;
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
