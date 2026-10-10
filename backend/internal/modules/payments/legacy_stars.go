package payments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"example.com/cabinet/backend/internal/modules/accounts"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type LegacyStarsUser struct {
	SourceLegacyUserID int64   `json:"source_legacy_user_id"`
	SourceTgID         int64   `json:"source_tg_id"`
	StarsChargeID      *string `json:"stars_charge_id"`
	IsStarsAutoRenew   *bool   `json:"is_stars_auto_renew"`
	StarsExpiresAt     *int64  `json:"stars_expires_at"`
}

func (u *LegacyStarsUser) UnmarshalJSON(raw []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if _, ok := fields["is_stars_auto_renew"]; !ok {
		return fmt.Errorf("is_stars_auto_renew is required")
	}
	type plain LegacyStarsUser
	return json.Unmarshal(raw, (*plain)(u))
}

func validateLegacyStars(users []LegacyStarsUser) error {
	if users == nil {
		return failure(400, "IMPORT_INVALID_PACKAGE")
	}
	legacyIDs, tgIDs := map[int64]bool{}, map[int64]bool{}
	for _, u := range users {
		if u.SourceLegacyUserID <= 0 || u.SourceTgID <= 0 || legacyIDs[u.SourceLegacyUserID] || tgIDs[u.SourceTgID] {
			return failure(400, "IMPORT_INVALID_PACKAGE")
		}
		if u.StarsChargeID != nil && (len(*u.StarsChargeID) > 64 || !utf8.ValidString(*u.StarsChargeID) || strings.ContainsRune(*u.StarsChargeID, 0)) {
			return failure(400, "IMPORT_INVALID_PACKAGE")
		}
		legacyIDs[u.SourceLegacyUserID], tgIDs[u.SourceTgID] = true, true
	}
	return nil
}

// ImportLegacyStarsTx retains raw recurring facts without creating orders or enabling renewal.
func (s *Service) ImportLegacyStarsTx(ctx context.Context, tx pgx.Tx, users []LegacyStarsUser) (int, error) {
	if tx == nil {
		return 0, unavailable()
	}
	if err := validateLegacyStars(users); err != nil {
		return 0, err
	}
	byTelegram := make(map[int64]LegacyStarsUser, len(users))
	for _, u := range users {
		byTelegram[u.SourceTgID] = u
	}
	ids := make(map[int64]uuid.UUID, len(users))
	for tg, u := range byTelegram {
		id, sourceID, err := accounts.LegacyAccountBySourceTgTx(ctx, tx, tg)
		if err != nil || id == uuid.Nil || sourceID != u.SourceLegacyUserID {
			return 0, failure(409, "IMPORT_IDENTITY_CONFLICT")
		}
		ids[tg] = id
	}
	inserted := 0
	for _, u := range users {
		id := ids[u.SourceTgID]
		if id == uuid.Nil {
			return inserted, failure(409, "IMPORT_IDENTITY_CONFLICT")
		}
		snapshot, err := json.Marshal(u)
		if err != nil {
			return inserted, unavailable()
		}
		var previousID uuid.UUID
		var same bool
		err = tx.QueryRow(ctx, `SELECT account_id,source_snapshot=$2::jsonb FROM legacy_stars_imports WHERE source_legacy_user_id=$1`, u.SourceLegacyUserID, snapshot).Scan(&previousID, &same)
		if err == nil {
			if !same || previousID != id {
				return inserted, failure(409, "IMPORT_SOURCE_CONFLICT")
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return inserted, unavailable()
		}
		_, err = tx.Exec(ctx, `INSERT INTO legacy_stars_imports(source_legacy_user_id,account_id,source_tg_id,stars_charge_id,is_stars_auto_renew,stars_expires_at,source_snapshot)
		 VALUES($1,$2,$3,$4,$5,$6,$7::jsonb)`, u.SourceLegacyUserID, id, u.SourceTgID, u.StarsChargeID, u.IsStarsAutoRenew, u.StarsExpiresAt, snapshot)
		if err != nil {
			return inserted, failure(409, "IMPORT_SOURCE_CONFLICT")
		}
		inserted++
	}
	return inserted, nil
}
