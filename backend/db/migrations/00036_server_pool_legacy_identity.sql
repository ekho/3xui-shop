-- +goose Up
-- Previously configured panel IDs were unrestricted nonempty text.
ALTER TABLE vpn_servers DROP CONSTRAINT vpn_servers_id_check,
 ADD CONSTRAINT vpn_servers_id_check CHECK(id<>''),
 DROP CONSTRAINT vpn_servers_name_check,
 ADD CONSTRAINT vpn_servers_name_check CHECK(char_length(name)>0);

-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM vpn_servers) OR EXISTS(SELECT 1 FROM vpn_server_reservations) THEN
  RAISE EXCEPTION 'server pool downgrade blocked: history exists';
 END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE vpn_servers DROP CONSTRAINT vpn_servers_id_check,
 ADD CONSTRAINT vpn_servers_id_check CHECK(id ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'),
 DROP CONSTRAINT vpn_servers_name_check,
 ADD CONSTRAINT vpn_servers_name_check CHECK(char_length(name) BETWEEN 1 AND 100);
