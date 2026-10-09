package payments

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"math"
	"reflect"
	"time"
)

type PurchaseArgs struct {
	OrderID uuid.UUID `json:"order_id"`
}

func (PurchaseArgs) Kind() string { return "purchase_fulfillment" }

type PurchaseWorker struct {
	river.WorkerDefaults[PurchaseArgs]
	Service *Service
}

func (w *PurchaseWorker) Work(ctx context.Context, job *river.Job[PurchaseArgs]) error {
	return w.Service.FulfillPurchase(ctx, job.Args.OrderID)
}
func (w *PurchaseWorker) Timeout(*river.Job[PurchaseArgs]) time.Duration {
	return 2*time.Minute + 5*time.Second
}
func (w *PurchaseWorker) NextRetry(job *river.Job[PurchaseArgs]) time.Time {
	return time.Now().Add(time.Duration(min(job.Attempt, 5)) * 10 * time.Second)
}

func (s *Service) purchaseReview(ctx context.Context, id uuid.UUID, reason string) error {
	_, err := s.pool.Exec(ctx, "UPDATE purchase_orders SET fulfillment_status='needs_review',review_required=true,review_reason=$2 WHERE id=$1 AND payment_status='paid' AND access_operation_id IS NULL", id, reason)
	if err != nil {
		return unavailable()
	}
	return nil
}

// FulfillPurchase owns the account across native reads and the target commit.
// No SQL transaction spans a panel call; the existing AccessWorker owns writes.
func (s *Service) FulfillPurchase(parent context.Context, id uuid.UUID) error {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	p, err := scanPurchase(s.pool.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return unavailable()
	}
	if p.paymentStatus != "paid" || p.accessID != nil || p.fulfillmentStatus == "needs_review" {
		return nil
	}
	var funded bool
	if err = s.pool.QueryRow(ctx, purchaseFundingCheck, id).Scan(&funded); err != nil {
		return unavailable()
	}
	if !funded {
		return s.purchaseReview(ctx, id, "funding_invalid")
	}
	owner, err := s.vpn.OpenAccessOwner(ctx, p.account)
	if err != nil {
		return unavailable()
	}
	defer owner.Release()
	if err = owner.TryLock(ctx); err != nil {
		if errors.Is(err, vpn.ErrBusy) {
			return river.JobSnooze(10 * time.Second)
		}
		return unavailable()
	}
	preTx, err := owner.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	a, err := s.accountByIDTx(ctx, preTx, p.account)
	reason, policyErr := s.purchasePolicyTx(ctx, preTx, p)
	closeErr := preTx.Rollback(ctx)
	if err != nil || closeErr != nil {
		return unavailable()
	}
	if policyErr != nil {
		return policyErr
	}
	if reason != "" {
		return s.purchaseReview(ctx, id, reason)
	}
	if a.Restricted || a.VpnBanned || stringValue(a.AccessProfile) == "unlimited" || a.PanelKey == "" || a.VpnID == uuid.Nil || a.SubID == "" {
		return s.purchaseReview(ctx, id, "account_not_eligible")
	}
	var quote PurchaseQuote
	if json.Unmarshal(p.quote, &quote) != nil || quote.Devices < 1 || quote.Devices >= math.MaxInt64 || quote.TrafficGb < 0 || quote.TrafficGb > math.MaxInt64/(1024*1024*1024) || quote.PeriodDays < 1 || quote.PeriodDays > 106751 || (quote.Profile != "regular" && quote.Profile != "euru") {
		return s.purchaseReview(ctx, id, "invalid_quote")
	}
	panelID := stringValue(a.AssignedPanelID)
	if panelID == "" {
		server, err := owner.AvailableServer(ctx)
		if errors.Is(err, vpn.ErrPanel) {
			return river.JobSnooze(10 * time.Second)
		}
		if err != nil {
			return unavailable()
		}
		panelID = server.ID
	}
	panel, err := owner.PanelFor(ctx, panelID)
	if err != nil {
		return unavailable()
	}
	defer panel.Close()
	view, err := panel.GetClient(ctx, a.PanelKey)
	if err != nil {
		return unavailable()
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	opID := uuid.New()
	t := vpn.AccessTarget{OperationID: opID, PanelID: panelID, PanelKey: a.PanelKey, VPNID: a.VpnID, SubID: a.SubID, DeviceCount: quote.Devices, TrafficLimitBytes: quote.TrafficGb * 1024 * 1024 * 1024, Profile: string(quote.Profile), Reset: true}
	if view == nil {
		if a.HadSubscription || a.AssignedPanelID != nil {
			return s.purchaseReview(ctx, id, "missing_client")
		}
		t.Missing = true
	} else {
		if a.AssignedPanelID == nil || view.VPNID != a.VpnID || view.SubID != a.SubID || view.ExpiryTimeMS <= 0 {
			return s.purchaseReview(ctx, id, "identity_changed")
		}
		if stringValue(a.AccessProfile) != "regular" && stringValue(a.AccessProfile) != "euru" {
			return s.purchaseReview(ctx, id, "profile_unknown")
		}
		previousProfile, profileErr := panel.ProfileInboundIDs(ctx, stringValue(a.AccessProfile))
		if profileErr != nil {
			return unavailable()
		}
		attach, detach, diffErr := panel.MembershipDiff(ctx, view.InboundIDs, previousProfile)
		if diffErr != nil || len(attach) > 0 || len(detach) > 0 {
			return s.purchaseReview(ctx, id, "membership_unknown")
		}
		t.PreviousExpiryMS = view.ExpiryTimeMS
		t.PreviousLimitIP = view.LimitIP
		t.PreviousTrafficLimitBytes = view.TrafficLimitBytes
		t.PreviousInboundIDs = append([]int64{}, view.InboundIDs...)
		if !view.Enabled {
			if view.ExpiryTimeMS > now.UnixMilli() && (p.action == "purchase" || !subscriptions.CanActivateRenewal(view, now)) {
				return s.purchaseReview(ctx, id, "disabled_client")
			}
			t.Enable = true
		}
	}
	ids, err := panel.ProfileInboundIDs(ctx, t.Profile)
	if err != nil {
		return unavailable()
	}
	t.InboundIDs = ids
	base := now.UnixMilli()
	if p.action != "change_plan" && t.PreviousExpiryMS > base {
		base = t.PreviousExpiryMS
	}
	add := quote.PeriodDays * int64(24*time.Hour/time.Millisecond)
	if base > math.MaxInt64-add {
		return s.purchaseReview(ctx, id, "expiry_overflow")
	}
	t.ExpiryTimeMS = base + add
	expires := time.UnixMilli(t.ExpiryTimeMS)
	desired := vpn.AccessDesired{ExpiresAt: &expires, Devices: t.DeviceCount, TrafficLimitBytes: t.TrafficLimitBytes, Profile: t.Profile, PlanId: &quote.PlanId, Revision: &quote.Revision, PeriodDays: &quote.PeriodDays, ResetTraffic: true, VpnBanned: false}
	desiredRaw, _ := json.Marshal(desired)
	targetRaw, _ := json.Marshal(t)
	tx, err := owner.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	current, err := s.lockAccount(ctx, tx, p.account)
	if err != nil {
		return unavailable()
	}
	latest, err := scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return unavailable()
	}
	if latest.paymentStatus != "paid" || latest.accessID != nil || latest.fulfillmentStatus == "needs_review" {
		return nil
	}
	if err = tx.QueryRow(ctx, purchaseFundingCheck, id).Scan(&funded); err != nil {
		return unavailable()
	}
	reviewTx := func(reason string) error {
		if _, e := tx.Exec(ctx, "UPDATE purchase_orders SET fulfillment_status='needs_review',review_required=true,review_reason=$2 WHERE id=$1", id, reason); e != nil {
			return unavailable()
		}
		if e := tx.Commit(ctx); e != nil {
			return unavailable()
		}
		return nil
	}
	if !funded {
		return reviewTx("funding_invalid")
	}
	reason, policyErr = s.purchasePolicyTx(ctx, tx, p)
	if policyErr != nil {
		return policyErr
	}
	if reason != "" {
		return reviewTx(reason)
	}
	if current.Restricted || current.VpnBanned || current.PanelKey != a.PanelKey || current.VpnID != a.VpnID || current.SubID != a.SubID || !reflect.DeepEqual(current.AssignedPanelID, a.AssignedPanelID) || !reflect.DeepEqual(current.AccessProfile, a.AccessProfile) || current.HadSubscription != a.HadSubscription {
		return reviewTx("account_changed")
	}
	active, err := s.vpn.UnresolvedTx(ctx, tx, p.account)
	if err != nil {
		return unavailable()
	}
	if active {
		return reviewTx("access_conflict")
	}
	_, err = s.vpn.QueueAccessTx(ctx, tx, vpn.AccessWrite{ID: opID, AccountID: p.account, Kind: "purchase", Reason: "paid_order", PlanID: &quote.PlanId, Revision: &quote.Revision, PeriodDays: &quote.PeriodDays, Desired: desiredRaw, Target: targetRaw, PurchaseOrderID: &id, CreatedAt: now})
	if errors.Is(err, vpn.ErrBusy) || errors.Is(err, vpn.ErrPanel) {
		return river.JobSnooze(10 * time.Second)
	}
	if err != nil {
		return unavailable()
	}
	if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET access_operation_id=$2,fulfillment_status='running' WHERE id=$1", id, opID); err != nil {
		return unavailable()
	}
	if err = tx.Commit(ctx); err != nil {
		return unavailable()
	}
	return nil
}
