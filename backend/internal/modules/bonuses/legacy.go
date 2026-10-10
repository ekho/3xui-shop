package bonuses

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"example.com/cabinet/backend/internal/modules/accounts"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type LegacyPackage struct {
	Version    int               `json:"version"`
	Source     string            `json:"source"`
	Promocodes []LegacyPromocode `json:"promocodes"`
	Referrals  []LegacyReferral  `json:"referrals"`
	Rewards    []LegacyReward    `json:"rewards"`
}

type LegacyPromocode struct {
	SourceID    int64     `json:"source_id"`
	Code        string    `json:"code"`
	Duration    int64     `json:"duration"`
	IsActivated *bool     `json:"is_activated"`
	ActivatedBy *int64    `json:"activated_by"`
	CreatedAt   time.Time `json:"created_at"`
}

type LegacyReferral struct {
	SourceID           int64      `json:"source_id"`
	ReferredTgID       int64      `json:"referred_tg_id"`
	ReferrerTgID       int64      `json:"referrer_tg_id"`
	CreatedAt          time.Time  `json:"created_at"`
	ReferredRewardedAt *time.Time `json:"referred_rewarded_at"`
	ReferredBonusDays  *int64     `json:"referred_bonus_days"`
}

type LegacyReward struct {
	SourceID    int64      `json:"source_id"`
	UserTgID    int64      `json:"user_tg_id"`
	RewardType  string     `json:"reward_type"`
	RewardLevel *int64     `json:"reward_level"`
	Amount      string     `json:"amount"`
	PaymentID   string     `json:"payment_id"`
	CreatedAt   time.Time  `json:"created_at"`
	RewardedAt  *time.Time `json:"rewarded_at"`
}

var legacyDecimal = regexp.MustCompile(`^(0|[1-9][0-9]*)(\.[0-9]{1,18})?$`)
var legacySource = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func legacyTime(t time.Time) bool {
	return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 && t.Nanosecond()%1000 == 0
}

func legacyDecimalValid(raw string, days bool) bool {
	if !legacyDecimal.MatchString(raw) {
		return false
	}
	value, ok := new(big.Rat).SetString(raw)
	if !ok || value.Sign() < 0 || (days && !value.IsInt()) {
		return false
	}
	// NUMERIC(38,18) has at most 20 integer digits, including after carry.
	limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(20), nil)
	return value.Cmp(new(big.Rat).SetInt(limit)) < 0
}

func validateLegacyPackage(p LegacyPackage) error {
	if p.Version != 1 || !legacySource.MatchString(p.Source) || p.Promocodes == nil || p.Referrals == nil || p.Rewards == nil {
		return failure(400, "IMPORT_INVALID_PACKAGE")
	}
	promoIDs, codes := map[int64]bool{}, map[string]bool{}
	for _, v := range p.Promocodes {
		if v.SourceID <= 0 || promoIDs[v.SourceID] || v.Code == "" || len(v.Code) > 32 || !utf8.ValidString(v.Code) || strings.ContainsRune(v.Code, 0) || codes[v.Code] || v.Duration < 1 || v.Duration > 2147483647 || v.IsActivated == nil || !legacyTime(v.CreatedAt) || (v.ActivatedBy != nil && (*v.ActivatedBy <= 0 || !*v.IsActivated)) {
			return failure(400, "IMPORT_INVALID_PACKAGE")
		}
		promoIDs[v.SourceID], codes[v.Code] = true, true
	}
	referralIDs, referred := map[int64]bool{}, map[int64]int64{}
	for _, v := range p.Referrals {
		if v.SourceID <= 0 || referralIDs[v.SourceID] || v.ReferredTgID <= 0 || v.ReferrerTgID <= 0 || v.ReferredTgID == v.ReferrerTgID || referred[v.ReferredTgID] != 0 || !legacyTime(v.CreatedAt) || (v.ReferredRewardedAt != nil && !legacyTime(*v.ReferredRewardedAt)) || (v.ReferredBonusDays != nil && (*v.ReferredBonusDays < 0 || *v.ReferredBonusDays > 2147483647)) {
			return failure(400, "IMPORT_INVALID_PACKAGE")
		}
		referralIDs[v.SourceID], referred[v.ReferredTgID] = true, v.ReferrerTgID
	}
	for tg := range referred {
		seen := map[int64]bool{}
		for parent := referred[tg]; parent != 0; parent = referred[parent] {
			if parent == tg || seen[parent] {
				return failure(400, "IMPORT_INVALID_PACKAGE")
			}
			seen[parent] = true
		}
	}
	rewardIDs, payments := map[int64]bool{}, map[int64]map[string]bool{}
	for _, v := range p.Rewards {
		if v.SourceID <= 0 || rewardIDs[v.SourceID] || v.UserTgID <= 0 || (v.RewardType != "DAYS" && v.RewardType != "MONEY") || (v.RewardLevel != nil && *v.RewardLevel != 1 && *v.RewardLevel != 2) || !legacyDecimalValid(v.Amount, v.RewardType == "DAYS") || v.PaymentID == "" || len(v.PaymentID) > 64 || !utf8.ValidString(v.PaymentID) || strings.ContainsRune(v.PaymentID, 0) || !legacyTime(v.CreatedAt) || (v.RewardedAt != nil && !legacyTime(*v.RewardedAt)) {
			return failure(400, "IMPORT_INVALID_PACKAGE")
		}
		if payments[v.UserTgID] == nil {
			payments[v.UserTgID] = map[string]bool{}
		}
		if payments[v.UserTgID][v.PaymentID] {
			return failure(400, "IMPORT_INVALID_PACKAGE")
		}
		payments[v.UserTgID][v.PaymentID], rewardIDs[v.SourceID] = true, true
	}
	return nil
}

// ImportLegacyTx adds historical facts within the caller's transaction; it never grants or funds them.
func (s *Service) ImportLegacyTx(ctx context.Context, tx pgx.Tx, p LegacyPackage) (int, error) {
	if tx == nil {
		return 0, unavailable()
	}
	if err := validateLegacyPackage(p); err != nil {
		return 0, err
	}
	ids := make(map[int64]uuid.UUID)
	for _, v := range p.Promocodes {
		if v.ActivatedBy != nil {
			ids[*v.ActivatedBy] = uuid.Nil
		}
	}
	for _, v := range p.Referrals {
		ids[v.ReferredTgID], ids[v.ReferrerTgID] = uuid.Nil, uuid.Nil
	}
	for _, v := range p.Rewards {
		ids[v.UserTgID] = uuid.Nil
	}
	for tg := range ids {
		id, _, err := accounts.LegacyAccountBySourceTgTx(ctx, tx, tg)
		if err != nil || id == uuid.Nil {
			return 0, failure(409, "IMPORT_IDENTITY_CONFLICT")
		}
		ids[tg] = id
	}
	inserted := 0
	for _, v := range p.Promocodes {
		var actor *uuid.UUID
		if v.ActivatedBy != nil {
			id := ids[*v.ActivatedBy]
			actor = &id
		}
		fresh, err := legacyBonusFresh(ctx, tx, "promocode", v.SourceID, p.Source, v)
		if err != nil {
			return inserted, err
		}
		if !fresh {
			continue
		}
		id := uuid.New()
		_, err = tx.Exec(ctx, `INSERT INTO promocodes(id,code,duration_days,created_at,is_activated,activated_account_id,activated_by_tg_id,legacy_source,legacy_promocode_id)
		 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, v.Code, v.Duration, v.CreatedAt, *v.IsActivated, actor, v.ActivatedBy, p.Source, v.SourceID)
		if err != nil {
			return inserted, failure(409, "IMPORT_SOURCE_CONFLICT")
		}
		if err = legacyBonusLedger(ctx, tx, "promocode", v.SourceID, id, p.Source, v); err != nil {
			return inserted, err
		}
		// The history event records safe metadata only; the original code stays in the private ledger.
		_, err = tx.Exec(ctx, `INSERT INTO promocode_events(id,promocode_id,action,created_at,after_snapshot)
		 VALUES($1,$2,'legacy_import',$3,$4::jsonb)`, uuid.New(), id, s.now(), `{"duration_days":`+strconv.FormatInt(v.Duration, 10)+`}`)
		if err != nil {
			return inserted, unavailable()
		}
		inserted++
	}
	for _, v := range p.Referrals {
		fresh, err := legacyBonusFresh(ctx, tx, "referral", v.SourceID, p.Source, v)
		if err != nil {
			return inserted, err
		}
		if !fresh {
			continue
		}
		id := uuid.New()
		_, err = tx.Exec(ctx, `INSERT INTO referrals(id,legacy_referral_id,referrer_account_id,referred_account_id,created_at,referred_rewarded_at,referred_bonus_days)
		 VALUES($1,$2,$3,$4,$5,$6,$7)`, id, v.SourceID, ids[v.ReferrerTgID], ids[v.ReferredTgID], v.CreatedAt, v.ReferredRewardedAt, v.ReferredBonusDays)
		if err != nil {
			return inserted, failure(409, "IMPORT_SOURCE_CONFLICT")
		}
		if err = legacyBonusLedger(ctx, tx, "referral", v.SourceID, id, p.Source, v); err != nil {
			return inserted, err
		}
		inserted++
	}
	for _, v := range p.Rewards {
		fresh, err := legacyBonusFresh(ctx, tx, "reward", v.SourceID, p.Source, v)
		if err != nil {
			return inserted, err
		}
		if !fresh {
			var owner uuid.UUID
			if err := tx.QueryRow(ctx, `SELECT account_id FROM referrer_rewards WHERE legacy_reward_id=$1`, v.SourceID).Scan(&owner); err != nil || owner != ids[v.UserTgID] {
				return inserted, failure(409, "IMPORT_SOURCE_CONFLICT")
			}
			continue
		}
		id := uuid.New()
		_, err = tx.Exec(ctx, `INSERT INTO referrer_rewards(id,legacy_reward_id,account_id,reward_type,reward_level,amount,payment_id,created_at,rewarded_at)
		 VALUES($1,$2,$3,$4,$5,$6::numeric,$7,$8,$9)`, id, v.SourceID, ids[v.UserTgID], v.RewardType, v.RewardLevel, v.Amount, v.PaymentID, v.CreatedAt, v.RewardedAt)
		if err != nil {
			return inserted, failure(409, "IMPORT_SOURCE_CONFLICT")
		}
		if err = legacyBonusLedger(ctx, tx, "reward", v.SourceID, id, p.Source, v); err != nil {
			return inserted, err
		}
		inserted++
	}
	return inserted, nil
}

func legacyBonusSnapshot(source string, record any) ([]byte, error) {
	return json.Marshal(struct {
		Source string `json:"source"`
		Record any    `json:"record"`
	}{source, record})
}

func legacyBonusFresh(ctx context.Context, tx pgx.Tx, kind string, sourceID int64, source string, record any) (bool, error) {
	snapshot, err := legacyBonusSnapshot(source, record)
	if err != nil {
		return false, unavailable()
	}
	var id uuid.UUID
	var same bool
	err = tx.QueryRow(ctx, `SELECT entity_id,source_snapshot=$3::jsonb FROM legacy_bonus_imports WHERE kind=$1 AND source_id=$2`, kind, sourceID, snapshot).Scan(&id, &same)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, unavailable()
	}
	if !same {
		return false, failure(409, "IMPORT_SOURCE_CONFLICT")
	}
	var exists bool
	err = tx.QueryRow(ctx, `SELECT
	 ($3='promocode' AND EXISTS(SELECT 1 FROM promocodes WHERE id=$1 AND legacy_promocode_id=$2)) OR
	 ($3='referral' AND EXISTS(SELECT 1 FROM referrals WHERE id=$1 AND legacy_referral_id=$2)) OR
	 ($3='reward' AND EXISTS(SELECT 1 FROM referrer_rewards WHERE id=$1 AND legacy_reward_id=$2))`, id, sourceID, kind).Scan(&exists)
	if err != nil {
		return false, unavailable()
	}
	if !exists {
		return false, failure(409, "IMPORT_SOURCE_CONFLICT")
	}
	return false, nil
}

func legacyBonusLedger(ctx context.Context, tx pgx.Tx, kind string, sourceID int64, id uuid.UUID, source string, record any) error {
	snapshot, err := legacyBonusSnapshot(source, record)
	if err != nil {
		return unavailable()
	}
	_, err = tx.Exec(ctx, `INSERT INTO legacy_bonus_imports(kind,source_id,entity_id,source_snapshot) VALUES($1,$2,$3,$4::jsonb)`, kind, sourceID, id, snapshot)
	if err != nil {
		return failure(409, "IMPORT_SOURCE_CONFLICT")
	}
	return nil
}
