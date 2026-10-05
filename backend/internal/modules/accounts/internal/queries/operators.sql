-- name: AnyWebOperator :one
SELECT EXISTS(SELECT 1 FROM operator_accounts o JOIN accounts a ON a.id=o.account_id
 WHERE a.kind='web' AND NOT a.restricted AND a.verified_at IS NOT NULL);

-- name: GrantOperator :execrows
INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)
ON CONFLICT (account_id) DO NOTHING;

-- name: RevokeOperator :execrows
DELETE FROM operator_accounts WHERE account_id=$1;

-- name: CountOperatorClients :one
SELECT count(*) FROM accounts
WHERE sqlc.arg(query)::text='' OR
 strpos(lower(COALESCE(email_key,'')),lower(sqlc.arg(query)::text))>0 OR
 strpos(lower(COALESCE(display_name,'')),lower(sqlc.arg(query)::text))>0 OR
 strpos(id::text,lower(sqlc.arg(query)::text))>0 OR
 strpos(COALESCE(telegram_id::text,''),sqlc.arg(query)::text)>0;

-- name: SearchOperatorClients :many
SELECT * FROM accounts
WHERE sqlc.arg(query)::text='' OR
 strpos(lower(COALESCE(email_key,'')),lower(sqlc.arg(query)::text))>0 OR
 strpos(lower(COALESCE(display_name,'')),lower(sqlc.arg(query)::text))>0 OR
 strpos(id::text,lower(sqlc.arg(query)::text))>0 OR
 strpos(COALESCE(telegram_id::text,''),sqlc.arg(query)::text)>0
ORDER BY created_at DESC NULLS LAST,id DESC LIMIT sqlc.arg(page_limit)::int OFFSET sqlc.arg(page_offset)::bigint;

-- name: AddTelegramAccount :exec
INSERT INTO accounts(id,kind,display_name,telegram_id,locale,vpn_id,sub_id,panel_key,access_profile)
VALUES($1,'telegram',$2,$3,$4,$5,$6,$7,'regular');

-- name: AccountByTelegramID :one
SELECT * FROM accounts WHERE telegram_id=$1;

-- name: AddOperatorAudit :exec
INSERT INTO audit_events(id,created_at,action,account_id,request_id,operation_id,operator_account_id,reason)
VALUES($1,$2,$3,$4,$5,$6,$7,$8);
-- name: OperatorExists :one
SELECT EXISTS(SELECT 1 FROM operator_accounts WHERE account_id=$1);
-- name: LockOperatorRole :one
SELECT account_id FROM operator_accounts WHERE account_id=$1 FOR SHARE;
