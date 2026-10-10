-- name: RewardRecipients :many
SELECT r.referrer_account_id AS account_id, 1::smallint AS level FROM referrals r WHERE r.referred_account_id=$1
UNION ALL
SELECT parent.referrer_account_id, 2::smallint FROM referrals r
JOIN referrals parent ON parent.referred_account_id=r.referrer_account_id WHERE r.referred_account_id=$1;

-- name: AddNativeReward :execrows
INSERT INTO referrer_rewards(id,account_id,reward_type,reward_level,amount,payment_id,source_order_id,created_at)
VALUES($1,$2,'DAYS',$3,$4,$5,$6,$7) ON CONFLICT(account_id,payment_id) DO NOTHING;

-- name: NativeReward :one
SELECT id,account_id,reward_level,amount::integer AS days,source_order_id,access_operation_id,rewarded_at
FROM referrer_rewards WHERE id=$1 AND source_order_id IS NOT NULL;

-- name: LockNativeReward :one
SELECT id,account_id,reward_level,amount::integer AS days,source_order_id,access_operation_id,rewarded_at
FROM referrer_rewards WHERE id=$1 AND source_order_id IS NOT NULL FOR UPDATE;

-- name: RewardForAccess :one
SELECT id,account_id,source_order_id,reward_level,rewarded_at FROM referrer_rewards WHERE access_operation_id=$1;

-- name: AttachRewardAccess :execrows
UPDATE referrer_rewards SET access_operation_id=$2 WHERE id=$1 AND access_operation_id IS NULL AND rewarded_at IS NULL;

-- name: CompleteReward :execrows
UPDATE referrer_rewards SET rewarded_at=$2 WHERE id=$1 AND rewarded_at IS NULL AND access_operation_id IS NOT NULL;
