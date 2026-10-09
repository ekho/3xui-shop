-- name: LockServerPool :exec
SELECT pg_advisory_xact_lock(hashtextextended('vpn-server-pool',0));

-- name: PoolServers :many
SELECT s.*, (SELECT count(*) FROM vpn_server_reservations r WHERE r.server_id=s.id) AS reserved_clients
FROM vpn_servers s ORDER BY s.id;

-- name: RegisterPoolServer :one
INSERT INTO vpn_servers(id,name,host,max_clients) VALUES($1,$2,$3,$4)
ON CONFLICT(id) DO UPDATE SET name=EXCLUDED.name,max_clients=EXCLUDED.max_clients,
 revision=vpn_servers.revision+1
WHERE vpn_servers.host=EXCLUDED.host AND NOT vpn_servers.retired
RETURNING *;

-- name: SeedPrimaryServer :exec
INSERT INTO vpn_servers(id,name,host,subscription_base_url) VALUES($1,$2,$3,$4)
ON CONFLICT DO NOTHING;

-- name: ObservePoolServer :exec
UPDATE vpn_servers SET online=sqlc.arg(online),observed_at=sqlc.arg(observed_at),
 subscription_base_url=CASE WHEN subscription_base_url='' THEN sqlc.arg(base)::text ELSE subscription_base_url END
WHERE id=sqlc.arg(id) AND revision=sqlc.arg(revision) AND NOT retired
 AND (observed_at IS NULL OR observed_at<sqlc.arg(observed_at));

-- name: ServerReservation :one
SELECT * FROM vpn_server_reservations WHERE account_id=$1;

-- name: ReserveTrialServer :exec
INSERT INTO vpn_server_reservations(account_id,server_id,trial_operation_id) VALUES($1,$2,$3);

-- name: ReserveAccessServer :exec
INSERT INTO vpn_server_reservations(account_id,server_id,access_operation_id) VALUES($1,$2,$3);

-- name: ReleaseServerReservation :exec
DELETE FROM vpn_server_reservations WHERE account_id=$1 AND server_id=$2;
