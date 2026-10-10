-- name: ReferralCode :one
SELECT code FROM referral_links WHERE account_id=$1;

-- name: AddReferralLink :execrows
INSERT INTO referral_links(account_id,code,created_at) VALUES($1,$2,$3)
ON CONFLICT(account_id) DO NOTHING;

-- name: ReferralCodeOwner :one
SELECT account_id FROM referral_links WHERE code=$1;

-- name: AddReferral :execrows
INSERT INTO referrals(id,referrer_account_id,referred_account_id,created_at) VALUES($1,$2,$3,$4)
ON CONFLICT(referred_account_id) DO NOTHING;

-- name: ReferralLevels :many
SELECT l.level::int AS level,
 CASE WHEN l.level=1 THEN
  (SELECT count(*) FROM referrals WHERE referrer_account_id=sqlc.arg(account_id)::uuid)
 ELSE
  (SELECT count(*) FROM referrals r JOIN referrals parent ON r.referrer_account_id=parent.referred_account_id
   WHERE parent.referrer_account_id=sqlc.arg(account_id)::uuid)
 END::bigint AS invited,
 trunc(COALESCE(sum(w.amount) FILTER(WHERE w.reward_type='DAYS' AND w.rewarded_at IS NOT NULL),0))::text AS granted_days,
 trunc(COALESCE(sum(w.amount) FILTER(WHERE w.reward_type='DAYS' AND w.rewarded_at IS NULL),0))::text AS pending_days,
 count(w.id) FILTER(WHERE w.reward_type='DAYS' AND w.rewarded_at IS NOT NULL) AS granted_rewards,
 count(w.id) FILTER(WHERE w.reward_type='DAYS' AND w.rewarded_at IS NULL) AS pending_rewards,
 count(w.id) FILTER(WHERE w.reward_type='MONEY') AS money_records,
 count(w.id) FILTER(WHERE w.reward_type='MONEY' AND w.rewarded_at IS NULL) AS pending_money_records,
 (SELECT count(*) FROM referrer_rewards WHERE account_id=sqlc.arg(account_id)::uuid AND reward_level IS NULL) AS unclassified_records
FROM (VALUES(1),(2)) AS l(level)
LEFT JOIN referrer_rewards w ON w.account_id=sqlc.arg(account_id)::uuid AND w.reward_level=l.level
GROUP BY l.level ORDER BY l.level;
