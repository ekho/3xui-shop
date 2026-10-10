package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// LegacyUser is the exact source identity and account metadata from SQLite.
type LegacyUser struct {
	SourceID         int64     `json:"source_id"`
	TgID             int64     `json:"tg_id"`
	VPNID            string    `json:"vpn_id"`
	SubID            string    `json:"sub_id"`
	ServerID         *int64    `json:"server_id"`
	FirstName        string    `json:"first_name"`
	LastName         *string   `json:"last_name"`
	Username         *string   `json:"username"`
	LanguageCode     string    `json:"language_code"`
	CreatedAt        time.Time `json:"created_at"`
	IsTrialUsed      *bool     `json:"is_trial_used"`
	InboundGroups    *[]string `json:"inbound_groups"`
	SourceInviteName *string   `json:"source_invite_name"`
}

func canonicalLegacyUUID(raw string) (uuid.UUID, bool) {
	id, err := uuid.Parse(raw)
	return id, err == nil && id != uuid.Nil && id.String() == raw
}

func validLegacyString(raw string) bool {
	return utf8.ValidString(raw) && !strings.ContainsRune(raw, 0)
}

func legacyProfile(groups *[]string) (*string, bool) {
	if groups == nil || len(*groups) == 0 {
		return nil, false
	}
	var profile *string
	banned := false
	for _, group := range *groups {
		switch group {
		case "banned":
			if banned {
				return nil, false
			}
			banned = true
		case "regular", "euru", "unlimited":
			if profile != nil {
				return nil, false
			}
			value := group
			profile = &value
		default:
			return nil, false
		}
	}
	return profile, banned
}

func validateLegacyUser(u LegacyUser) (uuid.UUID, error) {
	id, ok := canonicalLegacyUUID(u.VPNID)
	if !ok || u.SourceID <= 0 || u.TgID <= 0 || u.TgID > 1<<52-1 || !validLegacyTime(&u.CreatedAt) || !validLegacyString(u.FirstName) || u.FirstName == "" || utf8.RuneCountInString(u.FirstName) > 128 || !validLegacyString(u.LanguageCode) || u.LanguageCode == "" {
		return uuid.Nil, failure(400, "IMPORT_INVALID_PACKAGE")
	}
	if _, canonical := canonicalLegacyUUID(u.SubID); !canonical {
		if len(u.SubID) != 16 {
			return uuid.Nil, failure(400, "IMPORT_INVALID_PACKAGE")
		}
		for _, c := range u.SubID {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'z') {
				return uuid.Nil, failure(400, "IMPORT_INVALID_PACKAGE")
			}
		}
	}
	if u.ServerID != nil && *u.ServerID <= 0 {
		return uuid.Nil, failure(400, "IMPORT_INVALID_PACKAGE")
	}
	for _, v := range []*string{u.LastName, u.Username, u.SourceInviteName} {
		if v != nil && !validLegacyString(*v) {
			return uuid.Nil, failure(400, "IMPORT_INVALID_PACKAGE")
		}
	}
	if u.InboundGroups != nil {
		seen := make(map[string]bool, len(*u.InboundGroups))
		profiles := 0
		for _, group := range *u.InboundGroups {
			if !validLegacyString(group) || seen[group] {
				return uuid.Nil, failure(400, "IMPORT_INVALID_PACKAGE")
			}
			seen[group] = true
			switch group {
			case "banned":
			case "regular", "euru", "unlimited":
				profiles++
			default:
				return uuid.Nil, failure(400, "IMPORT_INVALID_PACKAGE")
			}
			if profiles > 1 {
				return uuid.Nil, failure(400, "IMPORT_INVALID_PACKAGE")
			}
		}
	}
	return id, nil
}

// ImportLegacyUsersTx joins the caller's transaction; a replay never rewrites native account state.
func ImportLegacyUsersTx(ctx context.Context, tx pgx.Tx, users []LegacyUser) (int, error) {
	if tx == nil || users == nil {
		return 0, failure(400, "IMPORT_INVALID_PACKAGE")
	}
	count := 0
	for _, u := range users {
		vpnID, err := validateLegacyUser(u)
		if err != nil {
			return count, err
		}
		snapshot, err := json.Marshal(u)
		if err != nil {
			return count, failure(400, "IMPORT_INVALID_PACKAGE")
		}
		var priorID uuid.UUID
		var same bool
		err = tx.QueryRow(ctx, `SELECT account_id,source_tg_id=$2 AND source_snapshot=$3::jsonb FROM legacy_account_imports WHERE source_id=$1 FOR UPDATE`, u.SourceID, u.TgID, snapshot).Scan(&priorID, &same)
		if err == nil {
			if !same {
				return count, failure(409, "IMPORT_SOURCE_CONFLICT")
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return count, unavailable()
		}
		var reserved bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM telegram_identity_reservations WHERE telegram_id=$1)`, u.TgID).Scan(&reserved); err != nil {
			return count, unavailable()
		}
		if reserved {
			return count, failure(409, "IMPORT_IDENTITY_CONFLICT")
		}
		panelKey := strconv.FormatInt(u.TgID, 10)
		var identityMatches int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE legacy_user_id=$1 OR telegram_id=$2 OR vpn_id=$3 OR sub_id=$4 OR panel_key=$5`, u.SourceID, u.TgID, vpnID, u.SubID, panelKey).Scan(&identityMatches); err != nil {
			return count, unavailable()
		}
		if identityMatches > 1 {
			return count, failure(409, "IMPORT_IDENTITY_CONFLICT")
		}
		var id uuid.UUID
		var kind, originalKind, currentSub, currentKey, currentVPN string
		var currentTG, currentLegacy *int64
		err = tx.QueryRow(ctx, `SELECT id,kind,COALESCE(original_kind,''),vpn_id::text,sub_id,panel_key,telegram_id,legacy_user_id FROM accounts WHERE legacy_user_id=$1 OR telegram_id=$2 OR vpn_id=$3 OR sub_id=$4 OR panel_key=$5 FOR UPDATE`, u.SourceID, u.TgID, vpnID, u.SubID, panelKey).Scan(&id, &kind, &originalKind, &currentVPN, &currentSub, &currentKey, &currentTG, &currentLegacy)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return count, unavailable()
		}
		if err == nil {
			if kind != "telegram" || originalKind != "" || currentTG == nil || *currentTG != u.TgID || currentLegacy != nil && *currentLegacy != u.SourceID || currentVPN != u.VPNID || currentSub != u.SubID || currentKey != panelKey {
				return count, failure(409, "IMPORT_IDENTITY_CONFLICT")
			}
			// Source proof may be attached to an otherwise identical pre-existing Telegram account.
			if currentLegacy == nil {
				if _, err = tx.Exec(ctx, `UPDATE accounts SET legacy_user_id=$2 WHERE id=$1 AND legacy_user_id IS NULL`, id, u.SourceID); err != nil {
					return count, failure(409, "IMPORT_IDENTITY_CONFLICT")
				}
			}
		} else {
			id = uuid.New()
			locale := u.LanguageCode
			if locale != "ru" && locale != "en" {
				locale = "ru"
			}
			var server *string
			if u.ServerID != nil {
				value := strconv.FormatInt(*u.ServerID, 10)
				server = &value
			}
			profile, banned := legacyProfile(u.InboundGroups)
			if _, err = tx.Exec(ctx, `INSERT INTO accounts(id,kind,telegram_id,legacy_user_id,display_name,locale,vpn_id,sub_id,panel_key,assigned_panel_id,created_at,access_profile,vpn_banned) VALUES($1,'telegram',$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, id, u.TgID, u.SourceID, u.FirstName, locale, vpnID, u.SubID, panelKey, server, u.CreatedAt, profile, banned); err != nil {
				return count, failure(409, "IMPORT_IDENTITY_CONFLICT")
			}
		}
		if _, err = tx.Exec(ctx, `INSERT INTO legacy_account_imports(source_id,account_id,source_tg_id,source_snapshot) VALUES($1,$2,$3,$4::jsonb)`, u.SourceID, id, u.TgID, snapshot); err != nil {
			return count, failure(409, "IMPORT_SOURCE_CONFLICT")
		}
		count++
	}
	return count, nil
}

// LegacyTrialStateTx returns used or unknown for imported accounts, even when source false.
func LegacyTrialStateTx(ctx context.Context, tx pgx.Tx, account uuid.UUID) (string, error) {
	if tx == nil || account == uuid.Nil {
		return "", failure(400, "IMPORT_INVALID_PACKAGE")
	}
	var used bool
	err := tx.QueryRow(ctx, `SELECT COALESCE(source_snapshot->>'is_trial_used'='true',false) FROM legacy_account_imports WHERE account_id=$1`, account).Scan(&used)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", unavailable()
	}
	if used {
		return "used", nil
	}
	return "unknown", nil
}

// LegacyAccountBySourceTgTx resolves the immutable source mapping after alias changes.
func LegacyAccountBySourceTgTx(ctx context.Context, tx pgx.Tx, tgID int64) (uuid.UUID, int64, error) {
	if tx == nil || tgID <= 0 {
		return uuid.Nil, 0, failure(400, "IMPORT_INVALID_PACKAGE")
	}
	var account uuid.UUID
	var sourceID int64
	err := tx.QueryRow(ctx, `SELECT account_id,source_id FROM legacy_account_imports WHERE source_tg_id=$1`, tgID).Scan(&account, &sourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, 0, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, 0, unavailable()
	}
	return account, sourceID, nil
}
