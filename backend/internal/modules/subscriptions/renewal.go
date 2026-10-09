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

// RenewalPlanIDTx preserves the public plan reader for existing consumers.
func (s *Service) RenewalPlanIDTx(ctx context.Context, tx pgx.Tx, account uuid.UUID) (uuid.UUID, error) {
	source, err := s.CurrentPlanSourceTx(ctx, tx, account)
	if err != nil {
		return uuid.Nil, err
	}
	return *source.PlanID, nil
}

// CurrentPlanSourceTx uses applied provenance, including same-plan reassignment.
func (s *Service) CurrentPlanSourceTx(ctx context.Context, tx pgx.Tx, account uuid.UUID) (vpn.PlanAssignment, error) {
	var empty vpn.PlanAssignment
	var a accounts.Snapshot
	var err error
	if tx == nil {
		a, err = s.accountByID(ctx, account)
	} else {
		a, err = s.accounts.LookupTx(ctx, tx, account)
		err = accountError(err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return empty, unavailable()
	}
	if !accounts.SourceEligible(a) {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	if a.Restricted {
		return empty, failure(403, "ACCOUNT_RESTRICTED")
	}
	if a.VpnBanned || !a.HadSubscription || a.AssignedPanelID == nil || (stringValue(a.AccessProfile) != "regular" && stringValue(a.AccessProfile) != "euru") {
		return empty, failure(409, "RENEWAL_NOT_ELIGIBLE")
	}
	if _, err := s.vpn.ServerTx(ctx, tx, *a.AssignedPanelID); err != nil {
		return empty, failure(409, "RENEWAL_NOT_ELIGIBLE")
	}
	source, err := s.vpn.CurrentPlanSourceTx(ctx, tx, account)
	if err != nil {
		return empty, unavailable()
	}
	if source == nil || source.PlanID == nil || *source.PlanID == uuid.Nil || source.OperationID == uuid.Nil {
		return empty, failure(409, "RENEWAL_NOT_ELIGIBLE")
	}
	return *source, nil
}

// Unknown disablement stays closed; a paid renewal resets proven exhaustion.
func CanActivateRenewal(view *vpn.PanelClientView, at time.Time) bool {
	return view != nil && view.ExpiryTimeMS > 0 && (view.Enabled || view.ExpiryTimeMS <= at.UnixMilli() || view.TrafficLimitBytes > 0 && view.UsedTraffic != nil && *view.UsedTraffic >= view.TrafficLimitBytes)
}
