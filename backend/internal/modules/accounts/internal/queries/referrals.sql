-- name: ReferralInviter :one
-- One snapshot keeps the owner visible while a retired identity is re-linked.
SELECT * FROM accounts WHERE id=COALESCE(
 (SELECT id FROM accounts WHERE telegram_id=sqlc.arg(telegram_id)::bigint),
 (SELECT account_id FROM telegram_identity_reservations WHERE telegram_id=sqlc.arg(telegram_id)::bigint)
);
