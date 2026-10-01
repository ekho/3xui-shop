-- name: AddSession :exec
INSERT INTO sessions(id_hash,account_id,csrf_token,created_at,last_seen,absolute_expires_at) VALUES($1,$2,$3,$4,$5,$6);
-- name: AuthenticateSession :one
UPDATE sessions SET last_seen=GREATEST(last_seen,sqlc.arg(now)::timestamptz)
WHERE id_hash=sqlc.arg(id_hash) AND absolute_expires_at>sqlc.arg(now)::timestamptz AND last_seen>sqlc.arg(now)::timestamptz-INTERVAL '7 days'
RETURNING *;
-- name: DeleteSession :exec
DELETE FROM sessions WHERE id_hash=$1;
