-- name: TrialReferral :one
SELECT id, legacy_referral_id, referred_bonus_days, referred_rewarded_at
FROM referrals WHERE referred_account_id=$1 FOR UPDATE;

-- name: ReserveTrialBenefit :execrows
UPDATE referrals SET referred_bonus_days=sqlc.arg(referred_bonus_days)::integer
WHERE id=$1 AND legacy_referral_id IS NULL
 AND referred_bonus_days IS NULL AND referred_rewarded_at IS NULL;

-- name: ApplyTrialBenefit :one
UPDATE referrals SET referred_rewarded_at=sqlc.arg(referred_rewarded_at)::timestamptz
WHERE referred_account_id=$1 AND legacy_referral_id IS NULL
 AND referred_bonus_days IS NOT NULL AND referred_rewarded_at IS NULL
RETURNING id;
