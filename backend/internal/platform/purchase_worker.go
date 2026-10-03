package platform

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/store"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"math"
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
	c, err := s.pool.Acquire(ctx)
	if err != nil {
		return unavailable()
	}
	var locked bool
	if err = c.QueryRow(ctx, "SELECT pg_try_advisory_lock(hashtextextended('account-access:'||$1::text,0))", p.account).Scan(&locked); err != nil || !locked {
		c.Release()
		if err == nil {
			return river.JobSnooze(10 * time.Second)
		}
		return unavailable()
	}
	defer releaseOwner(c, p.account)
	a, err := store.New(c).AccountByID(ctx, p.account)
	if err != nil {
		return unavailable()
	}
	if a.Restricted || a.VpnBanned || a.AccessProfile.String == "unlimited" || a.PanelKey == "" || a.VpnID == uuid.Nil || a.SubID == "" || (a.AssignedPanelID.Valid && a.AssignedPanelID.String != s.cfg.PanelID) || s.cfg.PanelID == "" {
		return s.purchaseReview(ctx, id, "account_not_eligible")
	}
	var quote wire.PurchaseQuote
	if json.Unmarshal(p.quote, &quote) != nil || quote.Devices < 1 || quote.Devices >= math.MaxInt64 || quote.TrafficGb < 0 || quote.TrafficGb > math.MaxInt64/(1024*1024*1024) || quote.PeriodDays < 1 || quote.PeriodDays > 106751 || (quote.Profile != "regular" && quote.Profile != "euru") {
		return s.purchaseReview(ctx, id, "invalid_quote")
	}
	panel := NewPanelClient(s.cfg)
	defer panel.Close()
	view, err := panel.GetClient(ctx, a.PanelKey)
	if err != nil {
		return unavailable()
	}
	now := s.now().UTC().Truncate(time.Microsecond)
	opID := uuid.New()
	t := accessTarget{OperationID: opID, PanelID: s.cfg.PanelID, PanelKey: a.PanelKey, VPNID: a.VpnID, SubID: a.SubID, DeviceCount: quote.Devices, TrafficLimitBytes: quote.TrafficGb * 1024 * 1024 * 1024, Profile: string(quote.Profile), Reset: true}
	if view == nil {
		if a.HadSubscription || a.AssignedPanelID.Valid {
			return s.purchaseReview(ctx, id, "missing_client")
		}
		t.Missing = true
	} else {
		if !a.AssignedPanelID.Valid || view.VPNID != a.VpnID || view.SubID != a.SubID || view.ExpiryTimeMS <= 0 {
			return s.purchaseReview(ctx, id, "identity_changed")
		}
		if a.AccessProfile.String != "regular" && a.AccessProfile.String != "euru" {
			return s.purchaseReview(ctx, id, "profile_unknown")
		}
		previousProfile, profileErr := panel.ProfileInboundIDs(ctx, a.AccessProfile.String)
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
			if view.ExpiryTimeMS > now.UnixMilli() {
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
	if t.PreviousExpiryMS > base {
		base = t.PreviousExpiryMS
	}
	add := quote.PeriodDays * int64(24*time.Hour/time.Millisecond)
	if base > math.MaxInt64-add {
		return s.purchaseReview(ctx, id, "expiry_overflow")
	}
	t.ExpiryTimeMS = base + add
	expires := time.UnixMilli(t.ExpiryTimeMS)
	desired := wire.AccessDesired{ExpiresAt: &expires, Devices: t.DeviceCount, TrafficLimitBytes: t.TrafficLimitBytes, Profile: wire.AccessDesiredProfile(t.Profile), PlanId: &quote.PlanId, Revision: &quote.Revision, PeriodDays: &quote.PeriodDays, ResetTraffic: true, VpnBanned: false}
	desiredRaw, _ := json.Marshal(desired)
	targetRaw, _ := json.Marshal(t)
	tx, err := c.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	current, err := store.New(tx).LockAccount(ctx, p.account)
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
	if current.Restricted || current.VpnBanned || current.PanelKey != a.PanelKey || current.VpnID != a.VpnID || current.SubID != a.SubID || current.AssignedPanelID != a.AssignedPanelID || current.AccessProfile != a.AccessProfile || current.HadSubscription != a.HadSubscription {
		return reviewTx("account_changed")
	}
	var active bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM access_operations WHERE account_id=$1 AND status IN ('pending','provisioning','needs_review')) OR EXISTS(SELECT 1 FROM trial_operations WHERE account_id=$1 AND status IN ('pending','provisioning','needs_review'))", p.account).Scan(&active); err != nil {
		return unavailable()
	}
	if active {
		return reviewTx("access_conflict")
	}
	_, err = tx.Exec(ctx, "INSERT INTO access_operations(id,account_id,kind,status,reason,plan_id,plan_revision,period_days,desired,target,completed_steps,purchase_order_id,created_at,updated_at) VALUES($1,$2,'purchase','pending','paid_order',$3,$4,$5,$6,$7,'[\"prepared\"]'::jsonb,$8,$9,$9)", opID, p.account, quote.PlanId, quote.Revision, quote.PeriodDays, desiredRaw, targetRaw, id, now)
	if err != nil {
		return unavailable()
	}
	if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET access_operation_id=$2,fulfillment_status='running' WHERE id=$1", id, opID); err != nil {
		return unavailable()
	}
	if s.queue == nil {
		return unavailable()
	}
	if _, err = s.queue.InsertTx(ctx, tx, AccessArgs{OperationID: opID}, &river.InsertOpts{Queue: "provision", MaxAttempts: 5}); err != nil {
		return unavailable()
	}
	if err = tx.Commit(ctx); err != nil {
		return unavailable()
	}
	return nil
}
