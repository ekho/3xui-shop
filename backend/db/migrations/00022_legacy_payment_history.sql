-- +goose Up
CREATE TABLE legacy_payment_transactions (
 source_id bigint PRIMARY KEY CHECK(source_id>0),
 account_id uuid NOT NULL REFERENCES accounts(id),
 source_legacy_user_id bigint NOT NULL CHECK(source_legacy_user_id>0),
 source_tg_id bigint NOT NULL CHECK(source_tg_id>0),
 source_payment_id text NOT NULL CHECK(octet_length(source_payment_id)<=16384),
 payment_id_hash bytea NOT NULL UNIQUE CHECK(payment_id_hash=sha256(convert_to(source_payment_id,'UTF8'))),
 subscription text NOT NULL CHECK(octet_length(subscription)<=16384),
 status text NOT NULL CHECK(status IN ('pending','completed','canceled','refunded')),
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 imported_at timestamptz NOT NULL
);
CREATE INDEX legacy_payment_account_page ON legacy_payment_transactions(account_id,created_at DESC,source_id DESC);
-- +goose StatementBegin
CREATE FUNCTION legacy_payment_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'legacy payment archive is immutable';
END $$;
-- +goose StatementEnd
CREATE TRIGGER legacy_payment_immutable BEFORE UPDATE OR DELETE ON legacy_payment_transactions
 FOR EACH ROW EXECUTE FUNCTION legacy_payment_immutable();

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM legacy_payment_transactions) THEN
  RAISE EXCEPTION 'legacy payment downgrade blocked: retained financial archive';
 END IF;
END $$;
-- +goose StatementEnd
DROP FUNCTION legacy_payment_immutable() CASCADE;
DROP TABLE legacy_payment_transactions;
