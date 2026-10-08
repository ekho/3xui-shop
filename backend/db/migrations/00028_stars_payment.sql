-- +goose Up
ALTER TABLE purchase_orders DROP CONSTRAINT purchase_orders_payment_method_check;
ALTER TABLE purchase_orders ADD CONSTRAINT purchase_orders_payment_method_check CHECK (
 (payment_method IN ('yoomoney','yookassa','cryptomus','heleket','telegram_stars')
  AND ((payment_method='yoomoney' AND payment_type IN ('AC','PC'))
    OR (payment_method='telegram_stars' AND payment_type='STARS' AND quote->>'currency'='XTR')
    OR (payment_method='yookassa' AND payment_type='YOOKASSA')
    OR (payment_method='heleket' AND payment_type='HELEKET' AND quote->>'currency'='USD')
    OR (payment_method='cryptomus' AND payment_type='CRYPTOMUS' AND quote->>'currency'='USD'))
  AND manual_details IS NULL AND manual_reported_at IS NULL AND manual_decision IS NULL
  AND manual_decided_at IS NULL AND manual_actor_id IS NULL AND manual_reason IS NULL)
 OR (payment_method='manual' AND payment_type='MANUAL' AND manual_details IS NOT NULL
  AND char_length(btrim(manual_details)) BETWEEN 1 AND 2000));
CREATE TABLE stars_checkouts (
 order_id uuid PRIMARY KEY REFERENCES purchase_orders(id),
 bot_id bigint NOT NULL CHECK(bot_id>0),
 payer_id bigint NOT NULL CHECK(payer_id>0 AND payer_id<=4503599627370495),
 payload text NOT NULL UNIQUE CHECK(payload='stars:v1:'||order_id::text),
 invoice_url text CHECK(invoice_url IS NULL OR (invoice_url ~ '^https://t[.]me/[$][A-Za-z0-9_-]+$' AND octet_length(invoice_url)<=270))
);
-- +goose StatementBegin
CREATE FUNCTION stars_checkout_immutable() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' OR (NEW.order_id,NEW.bot_id,NEW.payer_id,NEW.payload) IS DISTINCT FROM (OLD.order_id,OLD.bot_id,OLD.payer_id,OLD.payload)
 OR OLD.invoice_url IS NOT NULL AND NEW.invoice_url IS DISTINCT FROM OLD.invoice_url THEN
  RAISE EXCEPTION 'Stars invoice provenance is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER stars_checkout_immutable BEFORE UPDATE OR DELETE ON stars_checkouts FOR EACH ROW EXECUTE FUNCTION stars_checkout_immutable();
CREATE TABLE stars_refunds (
 receipt_operation_id text PRIMARY KEY CHECK(receipt_operation_id ~ '^stars:[0-9a-f]{64}$'),
 order_id uuid NOT NULL REFERENCES purchase_orders(id),
 bot_id bigint NOT NULL CHECK(bot_id>0),
 payer_id bigint NOT NULL CHECK(payer_id>0 AND payer_id<=4503599627370495),
 charge_id text NOT NULL CHECK(octet_length(charge_id) BETWEEN 1 AND 4096),
 payload text NOT NULL CHECK(payload='stars:v1:'||order_id::text),
 amount bigint NOT NULL CHECK(amount>0),
 currency text NOT NULL CHECK(currency='XTR'),
 state text NOT NULL CHECK(state IN ('pending','uncertain','confirmed')),
 operator_account_id uuid REFERENCES accounts(id),
 reason text CHECK(reason IS NULL OR char_length(btrim(reason)) BETWEEN 1 AND 1000),
 idempotency_key uuid,
 body_hash bytea,
 proof jsonb CHECK(proof IS NULL OR jsonb_typeof(proof)='object'),
 created_at timestamptz NOT NULL,
 confirmed_at timestamptz,
 CHECK((operator_account_id IS NULL AND reason IS NULL AND idempotency_key IS NULL AND body_hash IS NULL)
    OR (operator_account_id IS NOT NULL AND reason IS NOT NULL AND idempotency_key IS NOT NULL AND body_hash IS NOT NULL)),
 CHECK((state='confirmed')=(proof IS NOT NULL AND confirmed_at IS NOT NULL))
);
CREATE INDEX stars_refund_order ON stars_refunds(order_id);
-- +goose StatementBegin
CREATE FUNCTION stars_refund_immutable() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN
 IF TG_OP='DELETE' OR (NEW.receipt_operation_id,NEW.order_id,NEW.bot_id,NEW.payer_id,NEW.charge_id,NEW.payload,NEW.amount,NEW.currency,NEW.operator_account_id,NEW.reason,NEW.idempotency_key,NEW.body_hash,NEW.created_at)
 IS DISTINCT FROM (OLD.receipt_operation_id,OLD.order_id,OLD.bot_id,OLD.payer_id,OLD.charge_id,OLD.payload,OLD.amount,OLD.currency,OLD.operator_account_id,OLD.reason,OLD.idempotency_key,OLD.body_hash,OLD.created_at)
 OR OLD.state='confirmed' AND (NEW.state,NEW.proof,NEW.confirmed_at) IS DISTINCT FROM (OLD.state,OLD.proof,OLD.confirmed_at) THEN
  RAISE EXCEPTION 'Stars refund provenance is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER stars_refund_immutable BEFORE UPDATE OR DELETE ON stars_refunds FOR EACH ROW EXECUTE FUNCTION stars_refund_immutable();
ALTER TABLE purchase_refunds ADD COLUMN source text NOT NULL DEFAULT 'operator';
ALTER TABLE purchase_refunds ALTER COLUMN operator_account_id DROP NOT NULL;
ALTER TABLE purchase_refunds DROP CONSTRAINT purchase_refunds_payment_method_check, DROP CONSTRAINT purchase_refunds_reference_check;
ALTER TABLE purchase_refunds ADD CONSTRAINT purchase_refunds_payment_method_check CHECK(payment_method IN ('yoomoney','manual','yookassa','cryptomus','heleket','telegram_stars'));
ALTER TABLE purchase_refunds ADD CONSTRAINT purchase_refund_source CHECK(
 (source='operator' AND payment_method<>'telegram_stars' AND operator_account_id IS NOT NULL AND reference ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$')
 OR (source='telegram' AND payment_method='telegram_stars' AND reference=receipt_operation_id AND returned_currency='XTR' AND returned_amount ~ '^[1-9][0-9]{0,18}$'));
ALTER TABLE purchase_receipts DROP CONSTRAINT purchase_provider_net, DROP CONSTRAINT purchase_provider_data;
ALTER TABLE purchase_receipts ADD CONSTRAINT purchase_provider_net CHECK (
 net_minor IS NOT NULL OR (provider_data IS NOT NULL AND COALESCE(
  (notification_type='yookassa.succeeded' AND provider_data->>'provider'='yookassa')
  OR (notification_type='telegram_stars.paid' AND provider_data->>'provider'='telegram_stars' AND currency='XTR')
  OR (notification_type IN ('cryptomus.paid','cryptomus.paid_over','heleket.paid','heleket.paid_over') AND provider_data->>'provider' IN ('cryptomus','heleket')),false)));
ALTER TABLE purchase_receipts ADD CONSTRAINT purchase_provider_data CHECK (
 provider_data IS NULL OR (jsonb_typeof(provider_data)='object' AND
  (notification_type='yookassa.succeeded' OR COALESCE(
   (notification_type='telegram_stars.paid' AND currency='XTR' AND net_minor IS NULL
    AND provider_data->>'provider'='telegram_stars'
    AND provider_data ?& ARRAY['bot_id','payer_id','payload','charge_id','provider_charge_id','amount_minor','currency','recurring','first_recurring','subscription_expires_at']
    AND provider_data->>'currency'='XTR' AND provider_data->>'amount_minor'=gross_minor::text
    AND jsonb_typeof(provider_data->'bot_id')='number' AND jsonb_typeof(provider_data->'payer_id')='number'
    AND jsonb_typeof(provider_data->'charge_id')='string' AND octet_length(provider_data->>'charge_id') BETWEEN 1 AND 4096
    AND jsonb_typeof(provider_data->'payload')='string' AND octet_length(provider_data->>'payload') BETWEEN 1 AND 128
    AND jsonb_typeof(provider_data->'recurring')='boolean' AND jsonb_typeof(provider_data->'first_recurring')='boolean') OR
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
 IF EXISTS(SELECT 1 FROM stars_checkouts) OR EXISTS(SELECT 1 FROM stars_refunds)
 OR EXISTS(SELECT 1 FROM purchase_orders WHERE payment_method='telegram_stars')
 OR EXISTS(SELECT 1 FROM purchase_receipts WHERE notification_type='telegram_stars.paid')
 OR EXISTS(SELECT 1 FROM purchase_refunds WHERE source='telegram') THEN
  RAISE EXCEPTION 'Stars downgrade blocked: retained money or invoice provenance';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE stars_refunds,stars_checkouts;
DROP FUNCTION stars_refund_immutable(),stars_checkout_immutable();
ALTER TABLE purchase_refunds DROP CONSTRAINT purchase_refund_source;
ALTER TABLE purchase_refunds DROP COLUMN source;
ALTER TABLE purchase_refunds ALTER COLUMN operator_account_id SET NOT NULL;
ALTER TABLE purchase_refunds DROP CONSTRAINT purchase_refunds_payment_method_check;
ALTER TABLE purchase_refunds ADD CONSTRAINT purchase_refunds_payment_method_check CHECK(payment_method IN ('yoomoney','manual','yookassa','cryptomus','heleket'));
ALTER TABLE purchase_refunds ADD CONSTRAINT purchase_refunds_reference_check CHECK(reference ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$');
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
