package httpapi

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

type cryptoCase struct{ field, merchant, key, apiHost, payHost, source string }

func cryptoCaseFor(provider string) cryptoCase {
	if provider == "heleket" {
		return cryptoCase{"Heleket", "00000000-0000-4000-8000-000000000015", "test-only-heleket-key", "api.heleket.com", "new-pay.heleket.com", "31.133.220.8"}
	}
	return cryptoCase{"Cryptomus", "00000000-0000-4000-8000-000000000014", "test-only-crypto-key", "api.cryptomus.com", "pay.cryptomus.com", "91.227.144.54"}
}
func setCryptoTestEnabled(s *regressionFixture, provider string, enabled bool) {
	if provider == "heleket" {
		s.cfg.Payments.HeleketEnabled = enabled
	} else {
		s.cfg.Payments.CryptomusEnabled = enabled
	}
}
func setCryptoTestMerchant(s *regressionFixture, provider, merchant string) {
	if provider == "heleket" {
		s.cfg.Payments.HeleketMerchantID = merchant
	} else {
		s.cfg.Payments.CryptomusMerchantID = merchant
	}
}
func cryptoSync(s *regressionFixture, provider string) func(context.Context, uuid.UUID) error {
	if provider == "heleket" {
		return s.payments.SyncHeleket
	}
	return s.payments.SyncCryptomus
}
func cryptoCheckoutFor(p wire.PurchaseOrder, provider string) *wire.CryptomusCheckout {
	if provider == "heleket" {
		if p.HeleketCheckout == nil {
			return nil
		}
		return &wire.CryptomusCheckout{State: wire.CryptomusCheckoutState(p.HeleketCheckout.State), Url: p.HeleketCheckout.Url}
	}
	return p.CryptomusCheckout
}

func cryptoPurchaseFixture(t *testing.T, provider string) (*regressionFixture, *testkit.Env, uuid.UUID, uuid.UUID) {
	t.Helper()
	s, e, account, plan := purchaseFixture(t)
	p := cryptoCaseFor(provider)
	raw, err := json.Marshal(map[string]any{p.field + "Enabled": true, p.field + "MerchantID": p.merchant, p.field + "APIKey": p.key})
	if err != nil || json.Unmarshal(raw, &s.cfg.Payments) != nil {
		t.Fatal(err)
	}
	return s, e, account, plan
}

func signedCryptoBody(unsigned string, provider string) []byte {
	sign := fmt.Sprintf("%x", md5.Sum([]byte(base64.StdEncoding.EncodeToString([]byte(unsigned))+cryptoCaseFor(provider).key)))
	return []byte(strings.TrimSuffix(unsigned, "}") + `,"sign":"` + sign + `"}`)
}

// Signed callbacks are hints: their status/amount never bypass authenticated info.
func testCryptoHTTPAuthoritativeStatus(t *testing.T, provider string) {
	s, _, _, order, f := cryptoFixture(t, provider)
	syncCrypto(t, s, order.OrderId, provider)
	h := New(app.NewModules(s.pool, s.limiter, s.queue, s.cfg), s.pool, s.cfg.HTTP)
	request := func(body []byte, path string) int {
		r := httptest.NewRequest("POST", path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = cryptoCaseFor(provider).source + ":12345"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	unknown := `{"type":"payment","uuid":"00000000-0000-4000-8000-000000000090","order_id":"00000000-0000-4000-8000-000000000091","comments":"<タグ>\/a","discount_percent":1.5}`
	before := f.infos
	body := signedCryptoBody(unknown, provider)
	// Same values/field order, whitespace and Unicode escaping changed in transit.
	pretty := bytes.ReplaceAll(body, []byte("<タグ>"), []byte(`<\u30bf\u30b0>`))
	pretty = bytes.ReplaceAll(pretty, []byte(`,"`), []byte(",\n  \""))
	if code := request(pretty, "/webhooks/"+provider); code != 200 || f.infos != before {
		t.Fatal("valid ordered/PHP slash/unicode signature rejected or unknown order called API", code)
	}
	for _, raw := range []string{`{}`, `[]`, string(body) + string(body), `{"type":"payment","type":"payment"}`, `{"type":"payment","object":{"x":1,"x":2}}`, strings.Repeat("a", 16385)} {
		if code := request([]byte(raw), "/webhooks/"+provider); code != 400 {
			t.Fatal("malformed/duplicate notice accepted", code)
		}
	}
	if code := request(body, "/webhooks/"+provider+"?unexpected=1"); code != 400 {
		t.Fatal("query accepted", code)
	}
	tampered := bytes.Replace(body, []byte(`"discount_percent":1.5`), []byte(`"discount_percent":2.5`), 1)
	if code := request(tampered, "/webhooks/"+provider); code != 403 || f.infos != before {
		t.Fatal("signature tampering accepted", code)
	}
	forged := fmt.Sprintf(`{"type":"payment","uuid":"%s","order_id":"%s","status":"paid","is_final":true,"amount":"1.00"}`, f.payment["uuid"], order.OrderId)
	if code := request(signedCryptoBody(forged, provider), "/webhooks/"+provider); code != 200 || f.infos != before+1 {
		t.Fatal("known signed hint failed authenticated info", code)
	}
	manualCounts(t, s, order.OrderId, 0, 0)
}

func testCryptoExpiryAndDrift(t *testing.T, provider string) {
	for _, name := range []string{"unattempted-expired", "unknown-expired", "merchant-drift", "disabled-before-attempt"} {
		t.Run(name, func(t *testing.T) {
			s, e, account, order, f := cryptoFixture(t, provider)
			switch name {
			case "unattempted-expired":
				e.Advance(31 * time.Minute)
			case "unknown-expired":
				f.failFirst = true
				if err := cryptoSync(s, provider)(context.Background(), order.OrderId); err == nil {
					t.Fatal("ambiguous500 expected")
				}
				e.Advance(31 * time.Minute)
			case "merchant-drift":
				setCryptoTestMerchant(s, provider, uuid.NewString())
			case "disabled-before-attempt":
				setCryptoTestEnabled(s, provider, false)
			}
			syncCrypto(t, s, order.OrderId, provider)
			manualCounts(t, s, order.OrderId, 0, 0)
			if name == "unknown-expired" {
				if len(f.posts) != 1 || f.infos != 1 {
					t.Fatal("expired ambiguous attempt recreated invoice instead of info")
				}
			} else if len(f.posts) != 0 || f.infos != 0 {
				t.Fatal("ineligible attempt called provider")
			}
			got, err := s.purchaseOrder(context.Background(), account, order.OrderId)
			if err != nil || name == "merchant-drift" && !got.ReviewRequired {
				t.Fatal("configuration drift not quarantined", err)
			}
		})
	}
}

func testCryptoProviderHTTPFailures(t *testing.T, provider string) {
	for _, name := range []string{"redirect", "oversize", "malformed", "trailing", "state", "missing-state"} {
		t.Run(name, func(t *testing.T) {
			s, _, _, order, f := cryptoFixture(t, provider)
			switch name {
			case "redirect":
				f.responseStatus = 302
			case "oversize":
				f.raw = strings.Repeat("a", 65537)
			case "malformed":
				f.raw = `{"state":`
			case "trailing":
				f.raw = `{"state":0,"result":{}}{}`
			case "state":
				f.raw = `{"state":1,"result":{}}`
			case "missing-state":
				f.raw = `{"result":{}}`
			}
			if err := cryptoSync(s, provider)(context.Background(), order.OrderId); err == nil || len(f.posts) != 1 {
				t.Fatal("unsafe API response accepted or real request not exercised")
			}
			manualCounts(t, s, order.OrderId, 0, 0)
		})
	}
}

type cryptoStub struct {
	mu             sync.Mutex
	payment        map[string]any
	posts          [][]byte
	infos          int
	failFirst      bool
	responseStatus int
	raw            string
}

func cryptoFixture(t *testing.T, provider string, setup ...func(*regressionFixture, *testkit.Env, uuid.UUID, uuid.UUID)) (*regressionFixture, *testkit.Env, uuid.UUID, wire.PurchaseOrder, *cryptoStub) {
	t.Helper()
	input := func(_ *regressionFixture, _ *testkit.Env, _ uuid.UUID, plan uuid.UUID) wire.PurchaseOrderInput {
		return purchaseInput(plan)
	}
	return cryptoOrderFixture(t, provider, input, setup...)
}
func cryptoOrderFixture(t *testing.T, provider string, input func(*regressionFixture, *testkit.Env, uuid.UUID, uuid.UUID) wire.PurchaseOrderInput, setup ...func(*regressionFixture, *testkit.Env, uuid.UUID, uuid.UUID)) (*regressionFixture, *testkit.Env, uuid.UUID, wire.PurchaseOrder, *cryptoStub) {
	t.Helper()
	s, e, account, plan := cryptoPurchaseFixture(t, provider)
	for _, prepare := range setup {
		prepare(s, e, account, plan)
	}
	in := input(s, e, account, plan)
	in.PaymentMethod, in.PaymentType = wire.PurchaseOrderInputPaymentMethod(provider), wire.PurchaseOrderInputPaymentType(strings.ToUpper(provider))
	order, err := s.createPurchaseOrder(context.Background(), account, uuid.New(), in)
	if err != nil {
		t.Fatal(err)
	}
	f := &cryptoStub{payment: map[string]any{
		"uuid": uuid.NewString(), "order_id": order.OrderId.String(), "amount": "123.45000000", "currency": "USD",
		"payment_amount": "0.00000000", "payer_amount": "0.00123450", "payer_currency": "BTC", "merchant_amount": "0.00120981",
		"status": "check", "payment_status": "check", "is_final": false,
		"created_at": e.Clock().Format(time.RFC3339Nano), "updated_at": e.Clock().Format(time.RFC3339Nano),
		"address": "test-only-wallet-not-for-proof", "txid": "test-only-txid-not-for-proof",
	}}
	f.payment["url"] = "https://" + cryptoCaseFor(provider).payHost + "/pay/" + f.payment["uuid"].(string)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		data, err := io.ReadAll(r.Body)
		want := fmt.Sprintf("%x", md5.Sum([]byte(base64.StdEncoding.EncodeToString(data)+cryptoCaseFor(provider).key)))
		if err != nil || r.Method != "POST" || r.Header.Get("merchant") != cryptoCaseFor(provider).merchant || r.Header.Get("sign") != want {
			w.WriteHeader(401)
			return
		}
		var input map[string]any
		if json.Unmarshal(data, &input) != nil || input["order_id"] != order.OrderId.String() {
			w.WriteHeader(422)
			return
		}
		switch r.URL.Path {
		case "/v1/payment":
			f.posts = append(f.posts, append([]byte{}, data...))
			if f.failFirst && len(f.posts) == 1 {
				w.WriteHeader(500)
				return
			}
		case "/v1/payment/info":
			if len(input) != 1 {
				w.WriteHeader(422)
				return
			}
			f.infos++
		default:
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
		json.NewEncoder(w).Encode(map[string]any{"state": 0, "result": f.payment})
	}))
	t.Cleanup(server.Close)
	target, _ := url.Parse(server.URL)
	prior := http.DefaultTransport
	http.DefaultTransport = kassaTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != cryptoCaseFor(provider).apiHost {
			return nil, errors.New("test forbids external provider endpoints")
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

func syncCrypto(t *testing.T, s *regressionFixture, id uuid.UUID, provider string) {
	t.Helper()
	err := cryptoSync(s, provider)(context.Background(), id)
	var waiting *river.JobSnoozeError
	if err != nil && !errors.As(err, &waiting) {
		t.Fatal("crypto synchronization failed", err)
	}
}
func settleCrypto(e *testkit.Env, f *cryptoStub) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payment["status"], f.payment["payment_status"], f.payment["is_final"] = "paid", "paid", true
	f.payment["payment_amount"] = "0.00123450"
	f.payment["updated_at"] = e.Clock().Format(time.RFC3339Nano)
}

// Changing bytes/order/merchant on retry or treating crypto net as USD breaks this.
func testCryptoRequestAndRecovery(t *testing.T, provider string) {
	s, e, account, order, f := cryptoFixture(t, provider)
	f.failFirst = true
	if err := cryptoSync(s, provider)(context.Background(), order.OrderId); err == nil {
		t.Fatal("ambiguous500 accepted")
	}
	manualCounts(t, s, order.OrderId, 0, 0)
	syncCrypto(t, s, order.OrderId, provider)
	if len(f.posts) != 2 || !bytes.Equal(f.posts[0], f.posts[1]) {
		t.Fatal("retry changed frozen request or invoice count")
	}
	var req map[string]any
	if json.Unmarshal(f.posts[0], &req) != nil || req["amount"] != "123.45" || req["currency"] != "USD" || req["order_id"] != order.OrderId.String() || req["lifetime"] != float64(1800) || req["is_payment_multiple"] != false || req["is_refresh"] != nil || req["url_callback"] != s.cfg.HTTP.CabinetOrigin+"/webhooks/"+provider || req["url_return"] != s.cfg.HTTP.CabinetOrigin+"/orders/"+order.OrderId.String() || req["url_success"] != req["url_return"] {
		t.Fatal("request changed retained invoice parameters or USD price")
	}
	ready, err := s.purchaseOrder(context.Background(), account, order.OrderId)
	if err != nil || !ready.CanPay || cryptoCheckoutFor(ready, provider) == nil || cryptoCheckoutFor(ready, provider).State != "ready" || cryptoCheckoutFor(ready, provider).Url == nil {
		t.Fatal("known invoice has no safe checkout", err)
	}
	setCryptoTestEnabled(s, provider, false)
	settleCrypto(e, f)
	firstTime := e.Clock()
	syncCrypto(t, s, order.OrderId, provider)
	e.Advance(time.Minute)
	f.payment["updated_at"] = e.Clock().Format(time.RFC3339Nano)
	syncCrypto(t, s, order.OrderId, provider)
	manualCounts(t, s, order.OrderId, 1, 1)
	var gross int64
	var net *int64
	var currency string
	var occurred time.Time
	var proof []byte
	if err = s.pool.QueryRow(context.Background(), "SELECT gross_minor,net_minor,currency,occurred_at,provider_data FROM purchase_receipts WHERE order_id=$1", order.OrderId).Scan(&gross, &net, &currency, &occurred, &proof); err != nil || gross != 12345 || net != nil || currency != "USD" || !occurred.Equal(firstTime) {
		t.Fatal("principal, unknown net or first time changed", err)
	}
	if bytes.Contains(proof, []byte("test-only")) || !bytes.Contains(proof, []byte("BTC")) || !bytes.Contains(proof, []byte("0.00120981")) {
		t.Fatal("proof leaked wallet facts or lost crypto denomination")
	}
	for range 2 {
		if err = s.fulfillPurchase(context.Background(), order.OrderId); err != nil {
			t.Fatal(err)
		}
	}
	paid, err := s.purchaseOrder(context.Background(), account, order.OrderId)
	if err != nil || paid.PaymentStatus != "paid" || paid.ReviewRequired || paid.AccessOperationId == nil {
		t.Fatal("valid proof failed shared preparation", err)
	}
	var operations int
	if err = s.pool.QueryRow(context.Background(), "SELECT count(*) FROM access_operations WHERE purchase_order_id=$1", order.OrderId).Scan(&operations); err != nil || operations != 1 {
		t.Fatal("replay duplicated access", err)
	}
}

func testCryptoPendingNullableDates(t *testing.T, provider string) {
	for _, both := range []bool{false, true} {
		t.Run(fmt.Sprintf("both-%t", both), func(t *testing.T) {
			s, e, account, order, f := cryptoFixture(t, provider)
			created := f.payment["created_at"]
			f.payment["updated_at"] = nil
			if both {
				f.payment["created_at"] = nil
			}
			syncCrypto(t, s, order.OrderId, provider)
			manualCounts(t, s, order.OrderId, 0, 0)
			pending, err := s.purchaseOrder(context.Background(), account, order.OrderId)
			if err != nil || pending.ReviewRequired || !pending.CanPay || cryptoCheckoutFor(pending, provider) == nil || cryptoCheckoutFor(pending, provider).State != "ready" {
				t.Fatal("nullable pending dates blocked checkout", err)
			}
			f.payment["created_at"] = created
			settleCrypto(e, f)
			for range 2 {
				syncCrypto(t, s, order.OrderId, provider)
				if err = s.fulfillPurchase(context.Background(), order.OrderId); err != nil {
					t.Fatal(err)
				}
			}
			manualCounts(t, s, order.OrderId, 1, 1)
			paid, err := s.purchaseOrder(context.Background(), account, order.OrderId)
			if err != nil || paid.PaymentStatus != "paid" || paid.ReviewRequired || paid.AccessOperationId == nil {
				t.Fatal("valid final proof did not recover pending invoice", err)
			}
		})
	}
}

func testCryptoFundingBoundary(t *testing.T, provider string) {
	for _, tc := range []struct {
		name, field    string
		value          any
		receipts, jobs int
		review         bool
	}{
		{"paid", "", nil, 1, 1, false}, {"paid-over", "status", "paid_over", 1, 1, false},
		{"invoice-mismatch", "amount", "123.46", 1, 0, true}, {"fractional-cent", "amount", "123.45000001", 0, 0, true},
		{"other-currency", "currency", "EUR", 0, 0, true}, {"underpayment", "payment_amount", "0.00123449", 0, 0, true},
		{"missing-payment", "payment_amount", nil, 0, 0, true}, {"zero-payment", "payment_amount", "0", 0, 0, true},
		{"missing-payer", "payer_amount", nil, 0, 0, true}, {"zero-payer", "payer_amount", "0", 0, 0, true},
		{"exponent", "payment_amount", "1e-3", 0, 0, true}, {"fraction", "payment_amount", "1/2", 0, 0, true},
		{"negative", "payment_amount", "-1", 0, 0, true}, {"nan", "payment_amount", "NaN", 0, 0, true},
		{"ticker", "payer_currency", "btc/usd", 0, 0, true}, {"missing-merchant", "merchant_amount", nil, 0, 0, true},
		{"negative-merchant", "merchant_amount", "-1", 0, 0, true}, {"not-final", "is_final", false, 0, 0, true},
		{"status-mismatch", "payment_status", "check", 0, 0, true}, {"missing-final", "is_final", nil, 0, 0, true},
		{"future", "updated_at", "2026-10-02T00:00:00Z", 0, 0, true}, {"before-created", "updated_at", "2026-09-30T23:59:59Z", 0, 0, true},
		{"missing-created", "created_at", nil, 0, 0, true}, {"missing-updated", "updated_at", nil, 0, 0, true},
		{"late", "late", nil, 1, 0, true}, {"local-canceled", "local-canceled", nil, 1, 0, true},
		{"locked", "status", "locked", 0, 0, true}, {"refund", "status", "refund_paid", 0, 0, true},
		{"wrong-amount", "status", "wrong_amount", 0, 0, true}, {"missing-uuid", "uuid", nil, 0, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, e, account, order, f := cryptoFixture(t, provider)
			syncCrypto(t, s, order.OrderId, provider)
			settleCrypto(e, f)
			switch tc.field {
			case "late":
				e.Advance(31 * time.Minute)
				f.payment["updated_at"] = e.Clock().Format(time.RFC3339Nano)
			case "local-canceled":
				if _, err := s.pool.Exec(context.Background(), "UPDATE purchase_orders SET payment_status='canceled',active=false WHERE id=$1", order.OrderId); err != nil {
					t.Fatal(err)
				}
			case "":
			default:
				f.payment[tc.field] = tc.value
				if tc.field == "status" {
					f.payment["payment_status"] = tc.value
				}
				if tc.name == "paid-over" {
					f.payment["payment_amount"] = "0.00133450"
				}
			}
			syncCrypto(t, s, order.OrderId, provider)
			manualCounts(t, s, order.OrderId, tc.receipts, tc.jobs)
			got, err := s.purchaseOrder(context.Background(), account, order.OrderId)
			if err != nil || got.ReviewRequired != tc.review || got.AccessOperationId != nil {
				t.Fatal("wrong money boundary", err)
			}
			if tc.review && (got.CanPay || got.FulfillmentStatus != "needs_review") {
				t.Fatal("unverified money allows payment or access")
			}
			if tc.jobs == 1 {
				if err = s.fulfillPurchase(context.Background(), order.OrderId); err != nil {
					t.Fatal(err)
				}
				got, err = s.purchaseOrder(context.Background(), account, order.OrderId)
				if err != nil || got.AccessOperationId == nil {
					t.Fatal("valid proof rejected by shared guard", err)
				}
			}
		})
	}
}

// Equivalent decimal spelling is a replay; a changed amount or foreign receipt is not.
func testCryptoReceiptConflictAndForeignCollision(t *testing.T, provider string) {
	s, e, account, order, f := cryptoFixture(t, provider)
	syncCrypto(t, s, order.OrderId, provider)
	settleCrypto(e, f)
	syncCrypto(t, s, order.OrderId, provider)
	firstTime := e.Clock()
	e.Advance(time.Minute)
	f.payment["payment_amount"] = "0.001234500000"
	f.payment["payer_amount"] = "0.0012345000"
	f.payment["merchant_amount"] = "0.001209810000"
	f.payment["updated_at"] = e.Clock().Format(time.RFC3339Nano)
	syncCrypto(t, s, order.OrderId, provider)
	got, err := s.purchaseOrder(context.Background(), account, order.OrderId)
	if err != nil || got.ReviewRequired {
		t.Fatal("equal decimal facts fabricated a conflict", err)
	}
	for _, query := range []string{
		"UPDATE " + provider + "_checkouts SET request='changed' WHERE order_id=$1",
		"UPDATE " + provider + "_checkouts SET merchant_id='00000000-0000-4000-8000-000000000099' WHERE order_id=$1",
		"UPDATE " + provider + "_checkouts SET first_attempt_at=first_attempt_at+interval '1 second' WHERE order_id=$1",
		"UPDATE " + provider + "_checkouts SET invoice_id=gen_random_uuid() WHERE order_id=$1",
		"UPDATE " + provider + "_checkouts SET checkout_url='https://pay.cryptomus.com/changed' WHERE order_id=$1",
		"UPDATE purchase_receipts SET provider_data=provider_data||'{\"status\":\"paid_over\"}' WHERE order_id=$1",
		"UPDATE purchase_receipts SET gross_minor=1 WHERE order_id=$1",
		"UPDATE purchase_receipts SET net_minor=1 WHERE order_id=$1",
	} {
		if _, err = s.pool.Exec(context.Background(), query, order.OrderId); err == nil {
			t.Fatal("immutable payment facts changed")
		}
	}
	fields := purchaseNotice(s, order.OrderId, provider+":"+f.payment["uuid"].(string), "123.40", "123.45")
	if err = s.receiveYooMoney(context.Background(), fields); err != nil {
		t.Fatal("foreign-method nullable-net collision failed", err)
	}
	manualCounts(t, s, order.OrderId, 1, 1)
	var gross int64
	var net *int64
	var occurred time.Time
	if err = s.pool.QueryRow(context.Background(), "SELECT gross_minor,net_minor,occurred_at FROM purchase_receipts WHERE order_id=$1", order.OrderId).Scan(&gross, &net, &occurred); err != nil || gross != 12345 || net != nil || !occurred.Equal(firstTime) {
		t.Fatal("first receipt overwritten", err)
	}
	if err = s.fulfillPurchase(context.Background(), order.OrderId); err != nil {
		t.Fatal(err)
	}
	got, err = s.purchaseOrder(context.Background(), account, order.OrderId)
	if err != nil || !got.ReviewRequired || got.AccessOperationId != nil {
		t.Fatal("foreign conflicting facts authorized access", err)
	}
}

func testCryptoChangedFactsBlockPreparedAccess(t *testing.T, provider string) {
	for _, name := range []string{"cancel", "payment", "payer", "merchant", "final"} {
		t.Run(name, func(t *testing.T) {
			s, e, account, order, f := cryptoFixture(t, provider)
			syncCrypto(t, s, order.OrderId, provider)
			settleCrypto(e, f)
			syncCrypto(t, s, order.OrderId, provider)
			if err := s.fulfillPurchase(context.Background(), order.OrderId); err != nil {
				t.Fatal(err)
			}
			got, err := s.purchaseOrder(context.Background(), account, order.OrderId)
			if err != nil || got.AccessOperationId == nil {
				t.Fatal("valid proof did not prepare access", err)
			}
			if reason, err := s.payments.CheckPurchaseAccess(context.Background(), nil, order.OrderId, account, *got.AccessOperationId); err != nil || reason != "" {
				t.Fatal("valid proof rejected", reason, err)
			}
			switch name {
			case "cancel":
				f.payment["status"], f.payment["payment_status"] = "cancel", "cancel"
			case "payment":
				f.payment["payment_amount"] = "0.00133450"
			case "payer":
				f.payment["payer_amount"] = "0.00123449"
			case "merchant":
				f.payment["merchant_amount"] = "0.00120980"
			case "final":
				f.payment["is_final"] = false
			}
			syncCrypto(t, s, order.OrderId, provider)
			if reason, err := s.payments.CheckPurchaseAccess(context.Background(), nil, order.OrderId, account, *got.AccessOperationId); err != nil || reason != "purchase_funding_invalid" {
				t.Fatal("changed facts still authorize prepared write", reason, err)
			}
			manualCounts(t, s, order.OrderId, 1, 1)
		})
	}
}

func testCryptoObservationRace(t *testing.T, provider string) {
	s, e, account, order, f := cryptoFixture(t, provider)
	syncCrypto(t, s, order.OrderId, provider)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	gate, err := e.Pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Release()
	if _, err = gate.Exec(ctx, "SELECT pg_advisory_lock(742014)"); err != nil {
		t.Fatal(err)
	}
	defer gate.Exec(context.Background(), "SELECT pg_advisory_unlock(742014)")
	if _, err = e.Pool.Exec(ctx, `CREATE FUNCTION hold_crypto_settlement() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN IF NEW.payment_status='paid' AND OLD.payment_status<>'paid' THEN PERFORM pg_advisory_xact_lock(742014); END IF; RETURN NEW; END $$;
CREATE TRIGGER hold_crypto_settlement AFTER UPDATE ON purchase_orders FOR EACH ROW EXECUTE FUNCTION hold_crypto_settlement();`); err != nil {
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
		t.Fatal("observations did not reach SQL barrier")
	}
	settleCrypto(e, f)
	settled, canceled := make(chan error, 1), make(chan error, 1)
	go func() { settled <- cryptoSync(s, provider)(ctx, order.OrderId) }()
	waitBlocked(1)
	f.mu.Lock()
	f.payment["status"], f.payment["payment_status"] = "cancel", "cancel"
	f.mu.Unlock()
	go func() { canceled <- cryptoSync(s, provider)(ctx, order.OrderId) }()
	waitBlocked(2)
	if _, err = gate.Exec(ctx, "SELECT pg_advisory_unlock(742014)"); err != nil {
		t.Fatal(err)
	}
	for _, result := range []chan error{settled, canceled} {
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.purchaseOrder(ctx, account, order.OrderId)
	if err != nil || !got.ReviewRequired || got.PaymentStatus != "paid" {
		t.Fatal("concurrent canceled observation lost settled conflict", err)
	}
	manualCounts(t, s, order.OrderId, 1, 1)
	var retained bool
	if err = e.Pool.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM purchase_receipts WHERE order_id=$1 AND gross_minor=12345 AND net_minor IS NULL AND review_reason='provider_status_conflict' AND provider_data->>'status'='paid')", order.OrderId).Scan(&retained); err != nil || !retained {
		t.Fatal("first receipt lost or not quarantined", err)
	}
	if err = s.fulfillPurchase(ctx, order.OrderId); err != nil {
		t.Fatal(err)
	}
	got, err = s.purchaseOrder(ctx, account, order.OrderId)
	if err != nil || got.AccessOperationId != nil {
		t.Fatal("conflicting observations prepared access", err)
	}
}

func testCryptoOrderGuards(t *testing.T, provider string) {
	for _, name := range []string{"disabled", "unverified", "restricted", "payment-type"} {
		t.Run(name, func(t *testing.T) {
			s, _, account, plan := cryptoPurchaseFixture(t, provider)
			in := purchaseInput(plan)
			in.PaymentMethod, in.PaymentType = wire.PurchaseOrderInputPaymentMethod(provider), wire.PurchaseOrderInputPaymentType(strings.ToUpper(provider))
			want := ""
			switch name {
			case "disabled":
				setCryptoTestEnabled(s, provider, false)
				want = "PAYMENT_METHOD_UNAVAILABLE"
			case "unverified":
				if _, err := s.register(context.Background(), signup("crypto-pending@example.test")); err != nil {
					t.Fatal(err)
				}
				// Pending registration has no account; a supplied ID cannot place an order.
				account = uuid.New()
				want = "INVALID_CREDENTIALS"
			case "restricted":
				if _, err := s.pool.Exec(context.Background(), "UPDATE accounts SET restricted=true WHERE id=$1", account); err != nil {
					t.Fatal(err)
				}
				want = "ACCOUNT_RESTRICTED"
			case "payment-type":
				in.PaymentType = "AC"
				want = "INVALID_INPUT"
			}
			if _, err := s.createPurchaseOrder(context.Background(), account, uuid.New(), in); !catalogueCode(err, want) {
				t.Fatal("order authority bypass", err)
			}
			var rows int
			if err := s.pool.QueryRow(context.Background(), "SELECT count(*) FROM purchase_orders WHERE account_id=$1", account).Scan(&rows); err != nil || rows != 0 {
				t.Fatal("rejected order left data", err)
			}
		})
	}
}

func testCryptoReviewCannotReconcile(t *testing.T, provider string) {
	s, e, account, order, f := cryptoFixture(t, provider)
	syncCrypto(t, s, order.OrderId, provider)
	settleCrypto(e, f)
	syncCrypto(t, s, order.OrderId, provider)
	actor := verified(t, s, e, "crypto-operator@example.test")
	if _, err := s.pool.Exec(context.Background(), "INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)", actor, e.Clock()); err != nil {
		t.Fatal(err)
	}
	f.payment["status"], f.payment["payment_status"] = "cancel", "cancel"
	syncCrypto(t, s, order.OrderId, provider)
	if _, err := s.reconcilePurchaseOrder(context.Background(), actor, account, order.OrderId, uuid.New(), wire.PurchaseReconcileInput{Reason: "test-only reconciliation"}); !catalogueCode(err, "PURCHASE_ORDER_CONFLICT") {
		t.Fatal("operator reconcile bypassed provider proof", err)
	}
	manualCounts(t, s, order.OrderId, 1, 1)
}

// A forwarded vendor address cannot authorize an unrelated network sender.
func testCryptoHTTPBoundary(t *testing.T, provider string) {
	h, _, _ := httpFixture(t)
	req := httptest.NewRequest("POST", "/webhooks/"+provider, strings.NewReader(`{"type":"payment","uuid":cryptoCaseFor(provider).merchant,"order_id":"00000000-0000-4000-8000-000000000015","sign":"00000000000000000000000000000000"}`))
	req.RemoteAddr = "203.0.113.5:12345"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", cryptoCaseFor(provider).source)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != 403 {
		t.Fatal("public callback did not enforce effective source IP", r.Code)
	}
}

// Selecting RUB, skipping the frozen request, or enqueueing twice breaks this.
func testCryptoOrderAtomic(t *testing.T, provider string) {
	s, _, account, plan := cryptoPurchaseFixture(t, provider)
	in := purchaseInput(plan)
	in.PaymentMethod, in.PaymentType = wire.PurchaseOrderInputPaymentMethod(provider), wire.PurchaseOrderInputPaymentType(strings.ToUpper(provider))
	key := uuid.New()
	out, err := s.createPurchaseOrder(context.Background(), account, key, in)
	if err != nil {
		t.Fatal("enabled Cryptomus did not create a server-priced order", err)
	}
	if out.Quote.AmountMinor != "12345" || out.Quote.Currency != "USD" || out.CanPay || out.Checkout != nil || string(out.PaymentMethod) != provider {
		t.Fatal("provider preparation selected RUB or became a YooMoney form")
	}
	again, err := s.createPurchaseOrder(context.Background(), account, key, in)
	if err != nil || again.OrderId != out.OrderId {
		t.Fatal("lost response created another order", err)
	}
	var checkouts, jobs int
	if err = s.pool.QueryRow(context.Background(), "SELECT count(*) FROM "+provider+"_checkouts WHERE order_id=$1", out.OrderId).Scan(&checkouts); err != nil {
		t.Fatal(err)
	}
	if err = s.pool.QueryRow(context.Background(), "SELECT count(*) FROM river_job WHERE kind='"+provider+"_payment' AND args->>'order_id'=$1", out.OrderId.String()).Scan(&jobs); err != nil || checkouts != 1 || jobs != 1 {
		t.Fatal("order/replay did not atomically retain one provider request/job", err)
	}
	s.cfg.Payments.ManualEnabled = true
	s.cfg.Payments.ManualCardDetails = "test-only payment instructions"
	s.cfg.Payments.YooKassaEnabled = true
	methods, err := s.paymentMethods(context.Background(), account)
	if err != nil || len(methods.Methods) != 4 {
		t.Fatal("enabled method list lost old providers", err)
	}
	for _, method := range methods.Methods {
		currency := "RUB"
		if string(method.Id) == provider {
			currency = "USD"
		}
		if string(method.Currency) != currency {
			t.Fatal("provider selected wrong denomination")
		}
	}
}

func TestCryptomusHTTPAuthoritativeStatus(t *testing.T) {
	testCryptoHTTPAuthoritativeStatus(t, "cryptomus")
}
func TestCryptomusExpiryAndDrift(t *testing.T)       { testCryptoExpiryAndDrift(t, "cryptomus") }
func TestCryptomusProviderHTTPFailures(t *testing.T) { testCryptoProviderHTTPFailures(t, "cryptomus") }
func TestCryptomusRequestAndRecovery(t *testing.T)   { testCryptoRequestAndRecovery(t, "cryptomus") }
func TestCryptomusPendingNullableDates(t *testing.T) { testCryptoPendingNullableDates(t, "cryptomus") }
func TestCryptomusFundingBoundary(t *testing.T)      { testCryptoFundingBoundary(t, "cryptomus") }
func TestCryptomusReceiptConflictAndForeignCollision(t *testing.T) {
	testCryptoReceiptConflictAndForeignCollision(t, "cryptomus")
}
func TestCryptomusChangedFactsBlockPreparedAccess(t *testing.T) {
	testCryptoChangedFactsBlockPreparedAccess(t, "cryptomus")
}
func TestCryptomusObservationRace(t *testing.T) { testCryptoObservationRace(t, "cryptomus") }
func TestCryptomusOrderGuards(t *testing.T)     { testCryptoOrderGuards(t, "cryptomus") }
func TestCryptomusReviewCannotReconcile(t *testing.T) {
	testCryptoReviewCannotReconcile(t, "cryptomus")
}
func TestCryptomusHTTPBoundary(t *testing.T) { testCryptoHTTPBoundary(t, "cryptomus") }
func TestCryptomusOrderAtomic(t *testing.T)  { testCryptoOrderAtomic(t, "cryptomus") }
