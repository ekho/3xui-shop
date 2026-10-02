-- +goose Up
ALTER TABLE trial_operations
 ADD COLUMN traffic_up_bytes bigint CHECK (traffic_up_bytes >= 0),
 ADD COLUMN traffic_down_bytes bigint CHECK (traffic_down_bytes >= 0),
 ADD COLUMN profile_snapshot jsonb;
ALTER TABLE accounts ADD COLUMN vpn_banned boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE trial_operations DROP COLUMN traffic_up_bytes, DROP COLUMN traffic_down_bytes, DROP COLUMN profile_snapshot;
ALTER TABLE accounts DROP COLUMN vpn_banned;
