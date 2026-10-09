-- +goose Up
CREATE TABLE vpn_servers (
 id text PRIMARY KEY CHECK(id ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'),
 name text NOT NULL UNIQUE CHECK(char_length(name) BETWEEN 1 AND 100),
 host text NOT NULL UNIQUE,
 max_clients bigint CHECK(max_clients>=0),
 subscription_base_url text NOT NULL DEFAULT '',
 online boolean NOT NULL DEFAULT false,
 observed_at timestamptz,
 revision bigint NOT NULL DEFAULT 1 CHECK(revision>0),
 retired boolean NOT NULL DEFAULT false
);
-- +goose StatementBegin
CREATE FUNCTION vpn_server_identity_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF TG_OP='DELETE' OR NEW.id<>OLD.id OR NEW.host<>OLD.host OR (OLD.retired AND NOT NEW.retired) THEN
  RAISE EXCEPTION 'server identity is immutable';
 END IF;
 RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER vpn_server_identity_guard BEFORE UPDATE OR DELETE ON vpn_servers
 FOR EACH ROW EXECUTE FUNCTION vpn_server_identity_guard();
CREATE UNIQUE INDEX trial_operation_account ON trial_operations(id,account_id);
CREATE UNIQUE INDEX access_operation_account ON access_operations(id,account_id);
CREATE TABLE vpn_server_reservations (
 account_id uuid PRIMARY KEY REFERENCES accounts(id),
 server_id text NOT NULL REFERENCES vpn_servers(id),
 trial_operation_id uuid,
 access_operation_id uuid,
 CHECK(num_nonnulls(trial_operation_id,access_operation_id)=1),
 FOREIGN KEY(trial_operation_id,account_id) REFERENCES trial_operations(id,account_id),
 FOREIGN KEY(access_operation_id,account_id) REFERENCES access_operations(id,account_id)
);
CREATE INDEX vpn_server_reservation_load ON vpn_server_reservations(server_id);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM vpn_servers) OR EXISTS(SELECT 1 FROM vpn_server_reservations) THEN
  RAISE EXCEPTION 'server pool downgrade blocked: history exists';
 END IF;
END $$;
-- +goose StatementEnd
DROP TABLE vpn_server_reservations;
DROP INDEX trial_operation_account;
DROP INDEX access_operation_account;
DROP TABLE vpn_servers;
DROP FUNCTION vpn_server_identity_guard();
