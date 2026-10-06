package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

type kassaTransport func(*http.Request) (*http.Response, error)

func (f kassaTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Only the fixed provider endpoint is replaced; PostgreSQL, module code and TLS are real.
type kassaStub struct {
	mu             sync.Mutex
	payment        map[string]any
	posts          [][]byte
	keys           []string
	gets           int
	failFirst      bool
	responseStatus int
	raw            string
}

func kassaFixture(t *testing.T, setup ...func(*regressionFixture, *testkit.Env, uuid.UUID, uuid.UUID)) (*regressionFixture, *testkit.Env, uuid.UUID, wire.PurchaseOrder, *kassaStub) {
	t.Helper()
	s, e, account, plan := purchaseFixture(t)
	s.cfg.Payments.YooKassaEnabled = true
	s.cfg.Payments.YooKassaShopID = "100001"
	s.cfg.Payments.YooKassaToken = "test-only-api-token"
	s.cfg.Payments.YooKassaTestMode = true
	s.cfg.Payments.ShopEmail = "receipts@example.test"
	for _, prepare := range setup {
		prepare(s, e, account, plan)
	}
	in := purchaseInput(plan)
	in.PaymentMethod, in.PaymentType = "yookassa", "YOOKASSA"
	order, err := s.createPurchaseOrder(context.Background(), account, uuid.New(), in)
	if err != nil {
		t.Fatal(err)
	}
	f := &kassaStub{payment: map[string]any{
		"id": uuid.NewString(), "status": "pending", "paid": false, "test": true, "refundable": false,
		"amount":     map[string]any{"value": "90071992547409.93", "currency": "RUB"},
		"created_at": e.Clock().Format(time.RFC3339Nano), "recipient": map[string]any{"account_id": "100001"},
		"metadata":     map[string]any{"order_id": order.OrderId.String()},
		"confirmation": map[string]any{"type": "redirect", "confirmation_url": "https://yoomoney.ru/checkout/payments/" + uuid.NewString()},
	}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		shop, token, ok := r.BasicAuth()
		if !ok || shop != "100001" || token != "test-only-api-token" {
			w.WriteHeader(401)
			return
		}
		if r.Method == "POST" && r.URL.Path == "/v3/payments" {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error("stub cannot read request")
				w.WriteHeader(500)
				return
			}
			f.posts = append(f.posts, data)
			f.keys = append(f.keys, r.Header.Get("Idempotence-Key"))
			if f.failFirst && len(f.posts) == 1 {
				w.WriteHeader(500)
				return
			}
		} else if r.Method == "GET" && r.URL.Path == "/v3/payments/"+f.payment["id"].(string) {
			f.gets++
		} else {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if f.responseStatus != 0 {
			w.Header().Set("Location", "https://attacker.example.test/")
			w.WriteHeader(f.responseStatus)
		}
		if f.raw != "" {
			io.WriteString(w, f.raw)
			return
		}
		json.NewEncoder(w).Encode(f.payment)
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	prior := http.DefaultTransport
	http.DefaultTransport = kassaTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.yookassa.ru" {
			return nil, errors.New("test forbids any external provider endpoint")
		}
		copy := r.Clone(r.Context())
		u := *r.URL
		u.Host = target.Host
		copy.URL = &u
		return server.Client().Transport.RoundTrip(copy)
	})
	t.Cleanup(func() { http.DefaultTransport = prior })
	return s, e, account, order, f
}
func syncKassa(t *testing.T, s *regressionFixture, id uuid.UUID) {
	t.Helper()
	err := s.payments.SyncYooKassa(context.Background(), id)
	var waiting *river.JobSnoozeError
	if err != nil && !errors.As(err, &waiting) {
		t.Fatal("provider synchronization failed", err)
	}
}
func settleKassa(e *testkit.Env, f *kassaStub) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payment["status"], f.payment["paid"] = "succeeded", true
	f.payment["captured_at"] = e.Clock().Format(time.RFC3339Nano)
}

// A new key/body after ambiguous500, a lost request snapshot or invented net breaks this.
func TestYooKassaRequestAndRecovery(t *testing.T) {
	s, e, account, order, f := kassaFixture(t)
	f.failFirst = true
	if err := s.payments.SyncYooKassa(context.Background(), order.OrderId); err == nil {
		t.Fatal("ambiguous500 was treated as successful creation")
	}
	manualCounts(t, s, order.OrderId, 0, 0)
	syncKassa(t, s, order.OrderId)
	if len(f.posts) != 2 || !bytes.Equal(f.posts[0], f.posts[1]) || f.keys[0] != order.OrderId.String() || f.keys[1] != f.keys[0] {
		t.Fatal("recovery recreated the payment with changed bytes/key")
	}
	var request struct {
		Amount       struct{ Value, Currency string }
		Capture      bool
		Save         bool `json:"save_payment_method"`
		Confirmation struct {
			Type      string
			ReturnURL string `json:"return_url"`
		}
		Metadata map[string]string
		Receipt  struct {
			Customer struct{ Email string }
			Items    []struct {
				Quantity string
				VAT      int `json:"vat_code"`
				Amount   struct{ Value, Currency string }
			}
		}
	}
	if err := json.Unmarshal(f.posts[0], &request); err != nil {
		t.Fatal(err)
	}
	if request.Amount.Value != "90071992547409.93" || request.Amount.Currency != "RUB" || !request.Capture || request.Save || request.Confirmation.Type != "redirect" || request.Confirmation.ReturnURL != s.cfg.HTTP.CabinetOrigin+"/orders/"+order.OrderId.String() || request.Metadata["order_id"] != order.OrderId.String() || request.Receipt.Customer.Email != "receipts@example.test" || len(request.Receipt.Items) != 1 || request.Receipt.Items[0].Quantity != "1" || request.Receipt.Items[0].VAT != 1 || request.Receipt.Items[0].Amount.Value != "90071992547409.93" {
		t.Fatal("provider request changed quote, binding or retained receipt parameters")
	}
	ready, err := s.purchaseOrder(context.Background(), account, order.OrderId)
	if err != nil || !ready.CanPay || ready.YookassaCheckout == nil || ready.YookassaCheckout.State != "ready" || ready.YookassaCheckout.Url == nil {
		t.Fatal("known pending provider payment has no safe checkout", err)
	}
	s.cfg.Payments.YooKassaEnabled = false
	settleKassa(e, f)
	for range 2 {
		if err = s.payments.ReceiveYooKassa(context.Background(), f.payment["id"].(string)); err != nil {
			t.Fatal(err)
		}
	}
	manualCounts(t, s, order.OrderId, 1, 1)
	var net *int64
	if err = s.pool.QueryRow(context.Background(), "SELECT net_minor FROM purchase_receipts WHERE order_id=$1", order.OrderId).Scan(&net); err != nil || net != nil {
		t.Fatal("missing income invented a commission/net amount", err)
	}
	if err = s.fulfillPurchase(context.Background(), order.OrderId); err != nil {
		t.Fatal(err)
	}
	paid, err := s.purchaseOrder(context.Background(), account, order.OrderId)
	if err != nil || paid.PaymentStatus != "paid" || paid.AccessOperationId == nil {
		t.Fatal("valid settled payment failed existing preparation", err)
	}
	if err = s.fulfillPurchase(context.Background(), order.OrderId); err != nil {
		t.Fatal(err)
	}
	var operations int
	if err = s.pool.QueryRow(context.Background(), "SELECT count(*) FROM access_operations WHERE purchase_order_id=$1", order.OrderId).Scan(&operations); err != nil || operations != 1 {
		t.Fatal("payment/restart duplicated access", err)
	}
}

func TestYooKassaUnknownCreationDeadlineAndDrift(t *testing.T) {
	for _, name := range []string{"deadline", "shop", "test"} {
		t.Run(name, func(t *testing.T) {
			s, e, account, order, f := kassaFixture(t)
			f.failFirst = true
			if err := s.payments.SyncYooKassa(context.Background(), order.OrderId); err == nil {
				t.Fatal("expected ambiguous creation failure")
			}
			switch name {
			case "deadline":
				e.Advance(25 * time.Hour)
			case "shop":
				s.cfg.Payments.YooKassaShopID = "100002"
			case "test":
				s.cfg.Payments.YooKassaTestMode = false
			}
			syncKassa(t, s, order.OrderId)
			if len(f.posts) != 1 {
				t.Fatal("unknown payment was recreated past idempotency window or at a different merchant/mode")
			}
			got, err := s.purchaseOrder(context.Background(), account, order.OrderId)
			if err != nil || !got.ReviewRequired || got.CanPay || got.AccessOperationId != nil {
				t.Fatal("ambiguous creation was not retained for review", err)
			}
			manualCounts(t, s, order.OrderId, 0, 0)
		})
	}
}

// Removing a binding/status/amount/time/refund guard must change receipt/job outcomes here.
func TestYooKassaFundingBoundary(t *testing.T) {
	for _, tc := range []struct {
		name           string
		receipts, jobs int
		review         bool
	}{
		{"settled-unknown-net", 1, 1, false}, {"settled-known-net", 1, 1, false},
		{"pending", 0, 0, false}, {"capture", 0, 0, false}, {"canceled", 0, 0, false},
		{"amount", 1, 0, true}, {"currency", 1, 0, true}, {"merchant", 1, 0, true}, {"test", 1, 0, true}, {"metadata", 1, 0, true},
		{"refund", 1, 0, true}, {"late", 1, 0, true}, {"local-canceled", 1, 0, true},
		{"missing-paid", 0, 0, true}, {"missing-test", 0, 0, true}, {"missing-capture", 0, 0, true}, {"future-time", 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, e, account, order, f := kassaFixture(t)
			syncKassa(t, s, order.OrderId)
			if tc.name == "late" {
				e.Advance(time.Hour)
			}
			if tc.name == "local-canceled" {
				if _, err := s.cancelPurchaseOrder(context.Background(), account, order.OrderId, uuid.New()); err != nil {
					t.Fatal(err)
				}
			}
			settleKassa(e, f)
			switch tc.name {
			case "settled-known-net":
				f.payment["income_amount"] = map[string]any{"value": "90071992547408.93", "currency": "RUB"}
			case "pending":
				f.payment["status"], f.payment["paid"] = "pending", false
			case "capture":
				f.payment["status"] = "waiting_for_capture"
			case "canceled":
				f.payment["status"], f.payment["paid"] = "canceled", false
			case "amount":
				f.payment["amount"].(map[string]any)["value"] = "1.00"
			case "currency":
				f.payment["amount"].(map[string]any)["currency"] = "USD"
			case "merchant":
				f.payment["recipient"].(map[string]any)["account_id"] = "100002"
			case "test":
				f.payment["test"] = false
			case "metadata":
				f.payment["metadata"].(map[string]any)["order_id"] = uuid.NewString()
			case "refund":
				f.payment["refunded_amount"] = map[string]any{"value": "0.01", "currency": "RUB"}
			case "missing-paid":
				delete(f.payment, "paid")
			case "missing-test":
				delete(f.payment, "test")
			case "missing-capture":
				delete(f.payment, "captured_at")
			case "future-time":
				f.payment["captured_at"] = e.Clock().Add(time.Hour).Format(time.RFC3339Nano)
			}
			for range 2 {
				if err := s.payments.ReceiveYooKassa(context.Background(), f.payment["id"].(string)); err != nil {
					t.Fatal("trusted provider observation not retained", err)
				}
			}
			manualCounts(t, s, order.OrderId, tc.receipts, tc.jobs)
			got, err := s.purchaseOrder(context.Background(), account, order.OrderId)
			if err != nil || got.ReviewRequired != tc.review || got.AccessOperationId != nil {
				t.Fatal("incorrect money boundary", err)
			}
			if tc.review && (got.CanPay || got.FulfillmentStatus != "needs_review") {
				t.Fatal("review still allows checkout/preparation")
			}
			if tc.jobs == 1 {
				if err = s.fulfillPurchase(context.Background(), order.OrderId); err != nil {
					t.Fatal(err)
				}
				got, err = s.purchaseOrder(context.Background(), account, order.OrderId)
				if err != nil || got.AccessOperationId == nil {
					t.Fatal("valid provider proof rejected by common funding guard", err)
				}
			}
		})
	}
}

func TestYooKassaReceiptConflictAndForeignCollision(t *testing.T) {
	s, e, account, order, f := kassaFixture(t)
	syncKassa(t, s, order.OrderId)
	settleKassa(e, f)
	id := f.payment["id"].(string)
	if err := s.payments.ReceiveYooKassa(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	// Income may appear later; do not rewrite the first unknown net or invent a conflict.
	f.payment["income_amount"] = map[string]any{"value": "90071992547408.93", "currency": "RUB"}
	if err := s.payments.ReceiveYooKassa(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	got, err := s.purchaseOrder(context.Background(), account, order.OrderId)
	if err != nil || got.ReviewRequired {
		t.Fatal("later optional income fabricated a contradiction", err)
	}
	fields := purchaseNotice(s, order.OrderId, "yookassa:"+id, "90071992547408.93", "90071992547409.93")
	if err = s.receiveYooMoney(context.Background(), fields); err != nil {
		t.Fatal("foreign receipt collision could not handle unknown net", err)
	}
	manualCounts(t, s, order.OrderId, 1, 1)
	if err = s.fulfillPurchase(context.Background(), order.OrderId); err != nil {
		t.Fatal(err)
	}
	got, err = s.purchaseOrder(context.Background(), account, order.OrderId)
	if err != nil || !got.ReviewRequired || got.AccessOperationId != nil {
		t.Fatal("conflicting receipt funded access", err)
	}
	var gross int64
	var net *int64
	if err = s.pool.QueryRow(context.Background(), "SELECT gross_minor,net_minor FROM purchase_receipts WHERE order_id=$1", order.OrderId).Scan(&gross, &net); err != nil || gross != 9007199254740993 || net != nil {
		t.Fatal("first monetary facts overwritten", err)
	}
}

func TestYooKassaProviderHTTPFailures(t *testing.T) {
	for _, name := range []string{"redirect", "oversize", "malformed"} {
		t.Run(name, func(t *testing.T) {
			s, _, _, order, f := kassaFixture(t)
			switch name {
			case "redirect":
				f.responseStatus = 302
			case "oversize":
				f.raw = strings.Repeat("a", 65537)
			case "malformed":
				f.raw = `{"id":`
			}
			if err := s.payments.SyncYooKassa(context.Background(), order.OrderId); err == nil {
				t.Fatal("unsafe provider response accepted")
			}
			manualCounts(t, s, order.OrderId, 0, 0)
		})
	}
}

func TestYooKassaHTTPAuthoritativeStatus(t *testing.T) {
	s, _, _, order, f := kassaFixture(t)
	syncKassa(t, s, order.OrderId)
	h := New(app.NewModules(s.pool, s.limiter, s.queue, s.cfg), s.pool, s.cfg.HTTP)
	request := func(body, ip string) int {
		r := httptest.NewRequest("POST", "/webhooks/yookassa", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = ip + ":12345"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	unknown := `{"type":"notification","event":"payment.succeeded","object":{"id":"` + uuid.NewString() + `"}}`
	before := f.gets
	if code := request(unknown, "127.0.0.1"); code != 200 || f.gets != before {
		t.Fatal("unknown payment caused an API request or failed acknowledgement", code)
	}
	for _, body := range []string{`{}`, `{"type":"notification","event":"payment.succeeded","object":{"id":"bad"}}`, strings.Repeat("a", 16385), unknown + unknown} {
		if code := request(body, "127.0.0.1"); code != 400 {
			t.Fatal("malformed notification accepted", code)
		}
	}
	forged := `{"type":"notification","event":"payment.succeeded","object":{"id":"` + f.payment["id"].(string) + `","status":"succeeded","paid":true,"amount":{"value":"1.00","currency":"RUB"}}}`
	if code := request(forged, "127.0.0.1"); code != 200 {
		t.Fatal("known provider hint failed", code)
	}
	manualCounts(t, s, order.OrderId, 0, 0)
	if f.gets != before+1 {
		t.Fatal("notification trusted its body instead of reading API")
	}
}

func TestYooKassaChangedFactsBlockQueuedAccess(t *testing.T) {
	for _, name := range []string{"status", "capture", "missing-paid"} {
		t.Run(name, func(t *testing.T) {
			s, e, account, order, f := kassaFixture(t)
			syncKassa(t, s, order.OrderId)
			settleKassa(e, f)
			if err := s.payments.ReceiveYooKassa(context.Background(), f.payment["id"].(string)); err != nil {
				t.Fatal(err)
			}
			if err := s.fulfillPurchase(context.Background(), order.OrderId); err != nil {
				t.Fatal(err)
			}
			got, err := s.purchaseOrder(context.Background(), account, order.OrderId)
			if err != nil || got.AccessOperationId == nil {
				t.Fatal("valid first proof did not prepare access", err)
			}
			if reason, err := s.payments.CheckPurchaseAccess(context.Background(), nil, order.OrderId, account, *got.AccessOperationId); err != nil || reason != "" {
				t.Fatal("valid proof rejected", reason, err)
			}
			switch name {
			case "status":
				f.payment["status"], f.payment["paid"] = "canceled", false
			case "capture":
				delete(f.payment, "captured_at")
			case "missing-paid":
				delete(f.payment, "paid")
			}
			if err = s.payments.ReceiveYooKassa(context.Background(), f.payment["id"].(string)); err != nil {
				t.Fatal(err)
			}
			if reason, err := s.payments.CheckPurchaseAccess(context.Background(), nil, order.OrderId, account, *got.AccessOperationId); err != nil || reason != "purchase_funding_invalid" {
				t.Fatal("changed provider facts still authorize a queued access write", reason, err)
			}
			manualCounts(t, s, order.OrderId, 1, 1)
		})
	}
}

func TestYooKassaCanceledSettlementRace(t *testing.T) {
	s, e, account, order, f := kassaFixture(t)
	syncKassa(t, s, order.OrderId)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	gate, err := e.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Release()
	if _, err = gate.Exec(ctx, "SELECT pg_advisory_lock(742012)"); err != nil {
		t.Fatal(err)
	}
	defer gate.Exec(context.Background(), "SELECT pg_advisory_unlock(742012)")
	if _, err = e.Pool.Exec(ctx, `CREATE FUNCTION hold_kassa_settlement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.payment_status='paid' AND OLD.payment_status<>'paid' THEN PERFORM pg_advisory_xact_lock(742012); END IF; RETURN NEW; END $$;
CREATE TRIGGER hold_kassa_settlement AFTER UPDATE ON purchase_orders FOR EACH ROW EXECUTE FUNCTION hold_kassa_settlement();`); err != nil {
		t.Fatal(err)
	}
	waitBlocked := func(n int) {
		t.Helper()
		for ctx.Err() == nil {
			var count int
			if err := e.Pool.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND cardinality(pg_blocking_pids(pid))>0").Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count >= n {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("controlled provider observations did not reach the SQL barrier")
	}
	settleKassa(e, f)
	id := f.payment["id"].(string)
	settled, canceled := make(chan error, 1), make(chan error, 1)
	go func() { settled <- s.payments.ReceiveYooKassa(ctx, id) }()
	waitBlocked(1) // Receipt is still uncommitted; settlement owns account/order.
	f.mu.Lock()
	f.payment["status"], f.payment["paid"] = "canceled", false
	f.mu.Unlock()
	go func() { canceled <- s.payments.ReceiveYooKassa(ctx, id) }()
	waitBlocked(2) // Old code has read no receipt and is waiting on its UPDATE.
	if _, err = gate.Exec(ctx, "SELECT pg_advisory_unlock(742012)"); err != nil {
		t.Fatal(err)
	}
	for _, result := range []chan error{settled, canceled} {
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.purchaseOrder(ctx, account, order.OrderId)
	if err != nil || !got.ReviewRequired || got.PaymentStatus != "paid" {
		t.Fatal("concurrent canceled observation lost the settled conflict", got.ReviewRequired, err)
	}
	manualCounts(t, s, order.OrderId, 1, 1)
	var retained bool
	if err = e.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM purchase_receipts WHERE order_id=$1 AND gross_minor=9007199254740993 AND review_reason='provider_status_conflict' AND provider_data->>'status'='succeeded')", order.OrderId).Scan(&retained); err != nil || !retained {
		t.Fatal("first provider receipt was overwritten or not quarantined", err)
	}
	if err = s.fulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	got, err = s.purchaseOrder(ctx, account, order.OrderId)
	if err != nil || got.AccessOperationId != nil {
		t.Fatal("conflicting provider observations prepared new access", err)
	}
}

func TestYooKassaLateNonterminalObservation(t *testing.T) {
	for _, status := range []string{"pending", "waiting_for_capture"} {
		t.Run(status, func(t *testing.T) {
			s, e, account, order, f := kassaFixture(t)
			syncKassa(t, s, order.OrderId)
			settleKassa(e, f)
			if err := s.payments.ReceiveYooKassa(context.Background(), f.payment["id"].(string)); err != nil {
				t.Fatal(err)
			}
			if err := s.fulfillPurchase(context.Background(), order.OrderId); err != nil {
				t.Fatal(err)
			}
			got, err := s.purchaseOrder(context.Background(), account, order.OrderId)
			if err != nil || got.AccessOperationId == nil {
				t.Fatal("settled proof did not prepare access", err)
			}
			// Model an earlier GET response delivered after the settled response.
			f.payment["status"], f.payment["paid"] = status, status == "waiting_for_capture"
			delete(f.payment, "captured_at")
			if err = s.payments.ReceiveYooKassa(context.Background(), f.payment["id"].(string)); err != nil {
				t.Fatal(err)
			}
			if reason, err := s.payments.CheckPurchaseAccess(context.Background(), nil, order.OrderId, account, *got.AccessOperationId); err != nil || reason != "" {
				t.Fatal("late nonterminal response invalidated settled payment", reason, err)
			}
			manualCounts(t, s, order.OrderId, 1, 1)
		})
	}
}

func TestYooKassaLateInvalidAmount(t *testing.T) {
	for _, name := range []string{"zero-income", "income-currency", "refund-currency"} {
		t.Run(name, func(t *testing.T) {
			s, e, account, order, f := kassaFixture(t)
			syncKassa(t, s, order.OrderId)
			settleKassa(e, f)
			id := f.payment["id"].(string)
			if err := s.payments.ReceiveYooKassa(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			switch name {
			case "zero-income":
				f.payment["income_amount"] = map[string]any{"value": "0.00", "currency": "RUB"}
			case "income-currency":
				f.payment["income_amount"] = map[string]any{"value": "90071992547408.93", "currency": "USD"}
			case "refund-currency":
				f.payment["refunded_amount"] = map[string]any{"value": "0.00", "currency": "USD"}
			}
			if err := s.payments.ReceiveYooKassa(context.Background(), id); err != nil {
				t.Fatal(err)
			}
			got, err := s.purchaseOrder(context.Background(), account, order.OrderId)
			if err != nil || !got.ReviewRequired {
				t.Fatal("invalid later amount retained funding", err)
			}
			manualCounts(t, s, order.OrderId, 1, 1)
		})
	}
}

func TestYooKassaLaterIncomeConflictAndImmutability(t *testing.T) {
	s, e, account, order, f := kassaFixture(t)
	syncKassa(t, s, order.OrderId)
	settleKassa(e, f)
	id := f.payment["id"].(string)
	if err := s.payments.ReceiveYooKassa(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	f.payment["income_amount"] = map[string]any{"value": "90071992547408.93", "currency": "RUB"}
	if err := s.payments.ReceiveYooKassa(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	f.payment["income_amount"].(map[string]any)["value"] = "90071992547408.92"
	if err := s.payments.ReceiveYooKassa(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	got, err := s.purchaseOrder(context.Background(), account, order.OrderId)
	if err != nil || !got.ReviewRequired || got.AccessOperationId != nil {
		t.Fatal("two known income facts concealed a contradiction", err)
	}
	for _, query := range []string{
		"UPDATE yookassa_checkouts SET request='changed' WHERE order_id=$1",
		"UPDATE yookassa_checkouts SET shop_id='100002' WHERE order_id=$1",
		"UPDATE yookassa_checkouts SET test_mode=false WHERE order_id=$1",
		"UPDATE yookassa_checkouts SET first_attempt_at=first_attempt_at+interval '1 second' WHERE order_id=$1",
		"UPDATE yookassa_checkouts SET payment_id=gen_random_uuid() WHERE order_id=$1",
		"UPDATE yookassa_checkouts SET income_minor=1 WHERE order_id=$1",
		"UPDATE purchase_receipts SET net_minor=1 WHERE order_id=$1",
		"UPDATE purchase_receipts SET provider_data='{}' WHERE order_id=$1",
	} {
		if _, err = s.pool.Exec(context.Background(), query, order.OrderId); err == nil {
			t.Fatal("financial request/receipt proof mutable")
		}
	}
	source, err := os.ReadFile("../../db/migrations/00017_yookassa_payment.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(source), "-- +goose Down", 2)
	if len(parts) != 2 {
		t.Fatal("migration Down missing")
	}
	tx, err := s.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(context.Background(), parts[1]); err == nil || !strings.Contains(err.Error(), "provider downgrade blocked") {
		t.Fatal("downgrade erased provider history", err)
	}
	if err = tx.Rollback(context.Background()); err != nil {
		t.Fatal(err)
	}
	manualCounts(t, s, order.OrderId, 1, 1)
}
