package subscriptions

import (
	"context"
	"errors"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// RenewalPlanIDTx uses applied access provenance, never a device-count guess.
func (s *Service) RenewalPlanIDTx(ctx context.Context, tx pgx.Tx, account uuid.UUID) (uuid.UUID, error) {
	var a accounts.Snapshot
	var err error
	if tx == nil {
		a, err = s.accountByID(ctx, account)
	} else {
		a, err = s.accounts.LookupTx(ctx, tx, account)
		err = accountError(err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return uuid.Nil, unavailable()
	}
	if a.Kind != "web" || a.VerifiedAt == nil {
		return uuid.Nil, failure(401, "INVALID_CREDENTIALS")
	}
	if a.Restricted {
		return uuid.Nil, failure(403, "ACCOUNT_RESTRICTED")
	}
	if a.VpnBanned || !a.HadSubscription || a.AssignedPanelID == nil || *a.AssignedPanelID != s.config().PanelID || s.config().PanelID == "" || (stringValue(a.AccessProfile) != "regular" && stringValue(a.AccessProfile) != "euru") {
		return uuid.Nil, failure(409, "RENEWAL_NOT_ELIGIBLE")
	}
	id, err := s.vpn.CurrentPlanIDTx(ctx, tx, account)
	if err != nil {
		return uuid.Nil, unavailable()
	}
	if id == nil || *id == uuid.Nil {
		return uuid.Nil, failure(409, "RENEWAL_NOT_ELIGIBLE")
	}
	return *id, nil
}

// Unknown disablement stays closed; a paid renewal resets proven exhaustion.
func CanActivateRenewal(view *vpn.PanelClientView, at time.Time) bool {
	return view != nil && view.ExpiryTimeMS > 0 && (view.Enabled || view.ExpiryTimeMS <= at.UnixMilli() || view.TrafficLimitBytes > 0 && view.UsedTraffic != nil && *view.UsedTraffic >= view.TrafficLimitBytes)
}
