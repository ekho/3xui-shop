package httpapi

import (
	"context"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"testing"
	"time"
)

// A failed payment outcome must roll back the access, assignment and profile too.
func TestRegressionPurchaseOutcomeRollsBackAccessMetadata(t *testing.T) {
	s, e, account, order := paidPurchase(t)
	ctx := context.Background()
	p := panelFixture(t, s)
	if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET access_profile='euru' WHERE id=$1`, account); err != nil {
		t.Fatal(err)
	}
	if err := s.fulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	prepared, err := s.purchaseOrder(ctx, account, order.OrderId)
	if err != nil || prepared.AccessOperationId == nil {
		t.Fatal("paid target not prepared", err)
	}
	if _, err = e.Pool.Exec(ctx, `CREATE FUNCTION reject_applied_payment() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.fulfillment_status='applied' THEN RAISE EXCEPTION 'controlled outcome failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER reject_applied_payment BEFORE UPDATE ON purchase_orders FOR EACH ROW EXECUTE FUNCTION reject_applied_payment();`); err != nil {
		t.Fatal(err)
	}
	if err = s.applyAccess(ctx, *prepared.AccessOperationId); err != nil {
		t.Fatal(err)
	}
	var assigned bool
	var status, profile string
	if err = e.Pool.QueryRow(ctx, `SELECT assigned_panel_id IS NOT NULL,access_profile FROM accounts WHERE id=$1`, account).Scan(&assigned, &profile); err != nil {
		t.Fatal(err)
	}
	if err = e.Pool.QueryRow(ctx, `SELECT status FROM access_operations WHERE id=$1`, *prepared.AccessOperationId).Scan(&status); err != nil {
		t.Fatal(err)
	}
	final, err := s.purchaseOrder(ctx, account, order.OrderId)
	if err != nil || assigned || profile != "euru" || status != "needs_review" || final.PaymentStatus != "paid" || final.FulfillmentStatus != "needs_review" || !final.ReviewRequired || p.adds != 1 {
		t.Fatalf("outcome lost atomicity: assigned=%v profile=%s access=%s money=%s fulfillment=%s review=%v adds=%d err=%v", assigned, profile, status, final.PaymentStatus, final.FulfillmentStatus, final.ReviewRequired, p.adds, err)
	}
}

func TestRegressionPurchaseMissingHooksFailClosed(t *testing.T) {
	for _, missing := range []string{"check", "outcome"} {
		t.Run(missing, func(t *testing.T) {
			s, e, account, order := paidPurchase(t)
			ctx := context.Background()
			p := panelFixture(t, s)
			if _, err := e.Pool.Exec(ctx, `UPDATE accounts SET access_profile='euru' WHERE id=$1`, account); err != nil {
				t.Fatal(err)
			}
			if err := s.fulfillPurchase(ctx, order.OrderId); err != nil {
				t.Fatal(err)
			}
			prepared, err := s.purchaseOrder(ctx, account, order.OrderId)
			if err != nil || prepared.AccessOperationId == nil {
				t.Fatal("paid target not prepared", err)
			}
			hooks := vpn.PurchaseHooks{Check: s.payments.CheckPurchaseAccess, Outcome: s.payments.RecordPurchaseAccessTx}
			if missing == "check" {
				hooks.Check = nil
			} else {
				hooks.Outcome = nil
			}
			s.vpn = vpn.New(e.Pool, s.accounts, func() *river.Client[pgx.Tx] { return s.queue }, func() vpn.Settings { return regressionVPNSettings(s.cfg) }, func() time.Time { return s.now() }, nil, hooks)
			err = s.applyAccess(ctx, *prepared.AccessOperationId)
			var assigned bool
			var status, profile string
			if e.Pool.QueryRow(ctx, `SELECT assigned_panel_id IS NOT NULL,access_profile FROM accounts WHERE id=$1`, account).Scan(&assigned, &profile) != nil || e.Pool.QueryRow(ctx, `SELECT status FROM access_operations WHERE id=$1`, *prepared.AccessOperationId).Scan(&status) != nil {
				t.Fatal("cannot read controlled outcome")
			}
			final, readErr := s.purchaseOrder(ctx, account, order.OrderId)
			if assigned || profile != "euru" || status == "applied" || readErr != nil || final.PaymentStatus != "paid" || final.FulfillmentStatus == "applied" {
				t.Fatalf("missing hook state: assigned=%v profile=%s access=%s money=%s fulfillment=%s read=%v apply=%v", assigned, profile, status, final.PaymentStatus, final.FulfillmentStatus, readErr, err)
			}
			if missing == "check" && (err != nil || p.adds != 0 || final.FulfillmentStatus != "needs_review") {
				t.Fatal("missing funding hook did not stop native write", err)
			}
			if missing == "outcome" && (err == nil || status != "provisioning" || final.FulfillmentStatus != "running" || p.adds != 0) {
				t.Fatalf("missing outcome: adds=%d access=%s fulfillment=%s error=%v", p.adds, status, final.FulfillmentStatus, err)
			}
		})
	}
}

func TestRegressionPurchasePreparationRechecksAccount(t *testing.T) {
	for _, phase := range []string{"checkout", "fulfillment"} {
		for _, mode := range []string{"restricted", "owner lost"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				s, _, account, plan := purchaseFixture(t)
				ctx := context.Background()
				var orderID uuid.UUID
				if phase == "fulfillment" {
					order, err := s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
					if err != nil {
						t.Fatal(err)
					}
					orderID = order.OrderId
					if err = s.receiveYooMoney(ctx, purchaseNotice(s, orderID, "prepare-transfer", "90071992547409.00", "90071992547409.93")); err != nil {
						t.Fatal(err)
					}
				}
				p := panelFixture(t, s)
				preparationPanel(t, s, p, func(ctx context.Context) {
					preparationNoTransaction(t, ctx, s)
					if mode == "owner lost" {
						preparationKillOwner(t, ctx, s)
					} else if _, err := s.pool.Exec(ctx, `UPDATE accounts SET restricted=true WHERE id=$1`, account); err != nil {
						t.Error("account mutation blocked during panel read", err)
					}
				})
				var err error
				if phase == "checkout" {
					_, err = s.createPurchaseOrder(ctx, account, uuid.New(), purchaseInput(plan))
					if err == nil {
						t.Fatal("stale checkout persisted")
					}
				} else {
					err = s.fulfillPurchase(ctx, orderID)
					order, readErr := s.purchaseOrder(ctx, account, orderID)
					if readErr != nil || order.PaymentStatus != "paid" || order.AccessOperationId != nil || mode == "restricted" && (err != nil || order.FulfillmentStatus != "needs_review") {
						t.Fatal("stale paid target persisted or money lost", err, readErr)
					}
				}
				if mode == "owner lost" && err == nil {
					t.Fatal("lost physical owner accepted")
				}
				operations, jobs := preparationAccessCount(t, s)
				if operations != 0 || jobs != 0 || p.adds != 0 {
					t.Fatal("preparation created stale access")
				}
			})
		}
	}
}
