-- +goose Up
ALTER TABLE trial_operations ADD COLUMN first_started_at timestamptz,
 ADD COLUMN target jsonb,
 ADD COLUMN write_started boolean NOT NULL DEFAULT false,
 ADD COLUMN attempts integer NOT NULL DEFAULT 0,
 ADD COLUMN lease_hash bytea,
 ADD COLUMN lease_expires_at timestamptz,
 ADD COLUMN worker_pid integer,
 ADD COLUMN traffic_used_bytes bigint CHECK(traffic_used_bytes>=0),
 ADD COLUMN observed_at timestamptz;
-- +goose Down
ALTER TABLE trial_operations DROP COLUMN first_started_at,DROP COLUMN target,DROP COLUMN write_started,DROP COLUMN attempts,DROP COLUMN lease_hash,DROP COLUMN lease_expires_at,DROP COLUMN worker_pid,DROP COLUMN traffic_used_bytes,DROP COLUMN observed_at;
