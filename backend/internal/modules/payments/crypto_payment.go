package payments

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/netip"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// This private choice covers two separately checked wire contracts. Network and
// SQL identifiers are fixed here; callers cannot configure arbitrary providers.
type cryptoProvider string

const (
	cryptomusProvider cryptoProvider = "cryptomus"
	heleketProvider   cryptoProvider = "heleket"
)

func (p cryptoProvider) config(c Config) (string, string, bool) {
	if p == heleketProvider {
		return c.HeleketMerchantID, c.HeleketAPIKey, c.HeleketEnabled
	}
	return c.CryptomusMerchantID, c.CryptomusAPIKey, c.CryptomusEnabled
}
func (p cryptoProvider) table() string {
	if p == heleketProvider {
		return "heleket_checkouts"
	}
	return "cryptomus_checkouts"
}
func (p cryptoProvider) apiHost() string {
	if p == heleketProvider {
		return "api.heleket.com"
	}
	return "api.cryptomus.com"
}
func (p cryptoProvider) hostAllowed(host string) bool {
	if p == heleketProvider {
		return strings.EqualFold(host, "new-pay.heleket.com") || strings.EqualFold(host, "pay.heleket.com")
	}
	return strings.EqualFold(host, "pay.cryptomus.com")
}
func (p cryptoProvider) sourceAllowed(raw string) bool {
	source := "91.227.144.54"
	if p == heleketProvider {
		source = "31.133.220.8"
	}
	ip, err := netip.ParseAddr(raw)
	return err == nil && ip.Unmap() == netip.MustParseAddr(source)
}

func (s *Service) queueCryptoTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, quote PurchaseQuote, provider cryptoProvider) error {
	c := s.config()
	merchantID, key, _ := provider.config(c)
	amount, err := strconv.ParseInt(quote.AmountMinor, 10, 64)
	merchant, merchantErr := uuid.Parse(merchantID)
	if err != nil || amount <= 0 || quote.Currency != "USD" || merchantErr != nil || merchant == uuid.Nil || merchant.String() != merchantID || key == "" {
		return unavailable()
	}
	returnURL := strings.TrimRight(c.CabinetOrigin, "/") + "/orders/" + id.String()
	callback := strings.TrimRight(c.CabinetOrigin, "/") + "/webhooks/" + string(provider)
	if len(returnURL) > 255 || len(callback) > 255 {
		return unavailable()
	}
	body, err := json.Marshal(map[string]any{
		"amount": fmt.Sprintf("%d.%02d", amount/100, amount%100), "currency": "USD", "order_id": id.String(),
		"url_return": returnURL, "url_success": returnURL, "url_callback": callback,
		"lifetime": 1800, "is_payment_multiple": false,
	})
	if err != nil {
		return unavailable()
	}
	if _, err = tx.Exec(ctx, "INSERT INTO "+provider.table()+"(order_id,merchant_id,request) VALUES($1,$2,$3)", id, merchantID, body); err != nil {
		return unavailable()
	}
	if s.queue == nil || s.queue() == nil {
		return unavailable()
	}
	var args river.JobArgs = CryptomusArgs{OrderID: id}
	if provider == heleketProvider {
		args = HeleketArgs{OrderID: id}
	}
	_, err = s.queue().InsertTx(ctx, tx, args, &river.InsertOpts{Queue: "payments", MaxAttempts: 1000000})
	if err != nil {
		return unavailable()
	}
	return nil
}

type cryptoRow struct {
	provider cryptoProvider
	order    uuid.UUID
	merchant string
	request  []byte
	first    *time.Time
	id       *uuid.UUID
}
type cryptoPayment struct {
	ID             string `json:"uuid"`
	Order          string `json:"order_id"`
	Amount         string `json:"amount"`
	Currency       string `json:"currency"`
	PaymentAmount  string `json:"payment_amount"`
	PayerAmount    string `json:"payer_amount"`
	PayerCurrency  string `json:"payer_currency"`
	MerchantAmount string `json:"merchant_amount"`
	Status         string `json:"status"`
	PaymentStatus  string `json:"payment_status"`
	Final          *bool  `json:"is_final"`
	Created        string `json:"created_at"`
	Updated        string `json:"updated_at"`
	URL            string `json:"url"`
}

func (s *Service) cryptoCheckout(ctx context.Context, order uuid.UUID, provider cryptoProvider) (cryptoRow, error) {
	c := cryptoRow{order: order, provider: provider}
	err := s.pool.QueryRow(ctx, "SELECT merchant_id,request,first_attempt_at,invoice_id FROM "+provider.table()+" WHERE order_id=$1", order).Scan(&c.merchant, &c.request, &c.first, &c.id)
	return c, err
}
func validCryptoURL(raw string, provider cryptoProvider) bool {
	u, err := url.Parse(raw)
	return err == nil && len(raw) <= 2000 && u.Scheme == "https" && u.User == nil && provider.hostAllowed(u.Hostname()) && (u.Port() == "" || u.Port() == "443") && !strings.ContainsAny(raw, "\r\n\x00")
}
func cryptoSign(body []byte, key string) []byte {
	sum := md5.Sum([]byte(base64.StdEncoding.EncodeToString(body) + key)) // Provider wire protocol, not a password hash.
	return sum[:]
}

// Re-encode ordered JSON like the provider's JSON_UNESCAPED_UNICODE example;
// retain numeric lexemes, reject duplicate keys at every depth, escape slashes.
func cryptoSignedJSON(raw []byte) ([]byte, string, error) {
	if len(raw) == 0 || !utf8.Valid(raw) {
		return nil, "", failure(400, "INVALID_INPUT")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var out bytes.Buffer
	sign := ""
	writeString := func(value string) {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(value)
		out.Write(bytes.ReplaceAll(bytes.TrimSuffix(b.Bytes(), []byte("\n")), []byte("/"), []byte(`\/`)))
	}
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return failure(400, "INVALID_INPUT")
		}
		tok, err := d.Token()
		if err != nil {
			return failure(400, "INVALID_INPUT")
		}
		switch v := tok.(type) {
		case json.Delim:
			if v != '{' && v != '[' {
				return failure(400, "INVALID_INPUT")
			}
			out.WriteByte(byte(v))
			first := true
			seen := map[string]bool{}
			for d.More() {
				if v == '{' {
					keyTok, err := d.Token()
					key, ok := keyTok.(string)
					if err != nil || !ok || seen[key] {
						return failure(400, "INVALID_INPUT")
					}
					seen[key] = true
					if depth == 0 && key == "sign" {
						t, err := d.Token()
						var ok bool
						sign, ok = t.(string)
						if err != nil || !ok {
							return failure(400, "INVALID_INPUT")
						}
						continue
					}
					if !first {
						out.WriteByte(',')
					}
					writeString(key)
					out.WriteByte(':')
				} else if !first {
					out.WriteByte(',')
				}
				first = false
				if err := value(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			want := json.Delim('}')
			if v == '[' {
				want = ']'
			}
			if err != nil || end != want {
				return failure(400, "INVALID_INPUT")
			}
			out.WriteByte(byte(want))
		case string:
			writeString(v)
		case json.Number:
			out.WriteString(v.String())
		case bool:
			out.WriteString(strconv.FormatBool(v))
		case nil:
			out.WriteString("null")
		default:
			return failure(400, "INVALID_INPUT")
		}
		return nil
	}
	if err := value(0); err != nil {
		return nil, "", err
	}
	if _, err := d.Token(); err != io.EOF || out.Len() == 0 || out.Bytes()[0] != '{' {
		return nil, "", failure(400, "INVALID_INPUT")
	}
	return out.Bytes(), sign, nil
}

func (s *Service) cryptoRequest(ctx context.Context, c cryptoRow, path string, body []byte) (cryptoPayment, int, error) {
	var out cryptoPayment
	req, err := http.NewRequestWithContext(ctx, "POST", "https://"+c.provider.apiHost()+"/v1/payment"+path, bytes.NewReader(body))
	if err != nil {
		return out, 0, unavailable()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("merchant", c.merchant)
	_, key, _ := c.provider.config(s.config())
	req.Header.Set("sign", hex.EncodeToString(cryptoSign(body, key)))
	r, err := s.http.Do(req)
	if err != nil {
		return out, 0, unavailable()
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return out, r.StatusCode, unavailable()
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil || len(data) > 65536 {
		return out, r.StatusCode, unavailable()
	}
	if _, _, err = cryptoSignedJSON(data); err != nil {
		return out, r.StatusCode, unavailable()
	}
	var envelope struct {
		State  *int          `json:"state"`
		Result cryptoPayment `json:"result"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.State == nil || *envelope.State != 0 {
		return out, r.StatusCode, unavailable()
	}
	return envelope.Result, r.StatusCode, nil
}

// Every status/receipt observation and first-attempt decision shares these locks.
func (s *Service) cryptoLockedOrder(ctx context.Context, order uuid.UUID) (pgx.Tx, purchaseRow, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, purchaseRow{}, unavailable()
	}
	var account uuid.UUID
	if err = tx.QueryRow(ctx, "SELECT account_id FROM purchase_orders WHERE id=$1", order).Scan(&account); err == nil {
		_, err = s.lockAccount(ctx, tx, account)
	}
	var p purchaseRow
	if err == nil {
		p, err = scanPurchase(tx.QueryRow(ctx, "SELECT "+purchaseColumns+" FROM purchase_orders WHERE id=$1 FOR UPDATE", order))
	}
	if err != nil {
		tx.Rollback(ctx)
		return nil, purchaseRow{}, unavailable()
	}
	return tx, p, nil
}
func (s *Service) cryptoReviewTx(ctx context.Context, tx pgx.Tx, order uuid.UUID, reason string, observation []byte, provider cryptoProvider) error {
	if _, err := tx.Exec(ctx, "UPDATE purchase_receipts SET review_reason=$2 WHERE order_id=$1", order, reason); err != nil {
		return unavailable()
	}
	if _, err := tx.Exec(ctx, "UPDATE purchase_orders SET active=false,review_required=true,review_reason=$2,fulfillment_status=CASE WHEN access_operation_id IS NULL THEN 'needs_review' ELSE fulfillment_status END WHERE id=$1", order, reason); err != nil {
		return unavailable()
	}
	if _, err := tx.Exec(ctx, "UPDATE "+provider.table()+" SET state='unavailable',observation=COALESCE($2,observation) WHERE order_id=$1", order, observation); err != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) cryptoReview(ctx context.Context, order uuid.UUID, reason string, provider cryptoProvider) error {
	tx, _, err := s.cryptoLockedOrder(ctx, order)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = s.cryptoReviewTx(ctx, tx, order, reason, nil, provider); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) syncCrypto(ctx context.Context, order uuid.UUID, provider cryptoProvider) error {
	c, err := s.cryptoCheckout(ctx, order, provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return unavailable()
	}
	merchantID, key, enabled := provider.config(s.config())
	if merchantID != c.merchant {
		return s.cryptoReview(ctx, order, "provider_config_changed", provider)
	}
	if key == "" {
		return unavailable()
	}
	if c.id == nil {
		tx, p, err := s.cryptoLockedOrder(ctx, order)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		if err = tx.QueryRow(ctx, "SELECT first_attempt_at,invoice_id FROM "+provider.table()+" WHERE order_id=$1 FOR UPDATE", order).Scan(&c.first, &c.id); err != nil {
			return unavailable()
		}
		if p.review {
			return tx.Commit(ctx)
		}
		if c.id == nil && c.first == nil {
			if !p.active || p.paymentStatus != "pending" || !s.now().Before(p.expires) {
				_, err = tx.Exec(ctx, "UPDATE "+provider.table()+" SET state='unavailable' WHERE order_id=$1", order)
				if err != nil {
					return unavailable()
				}
				return tx.Commit(ctx)
			}
			if !enabled {
				return river.JobSnooze(10 * time.Second)
			}
			if err = tx.QueryRow(ctx, "UPDATE "+provider.table()+" SET first_attempt_at=$2 WHERE order_id=$1 RETURNING first_attempt_at", order, s.now().UTC().Truncate(time.Microsecond)).Scan(&c.first); err != nil {
				return unavailable()
			}
		}
		if err = tx.Commit(ctx); err != nil {
			return unavailable()
		}
		if c.id == nil && s.now().Before(p.expires) {
			payment, status, err := s.cryptoRequest(ctx, c, "", c.request)
			if err != nil {
				if status == 400 || status == 422 {
					return s.cryptoReview(ctx, order, "provider_request_rejected", provider)
				}
				return err
			}
			id, err := uuid.Parse(payment.ID)
			if err != nil || id == uuid.Nil || id.String() != payment.ID || payment.Order != order.String() {
				return s.cryptoReview(ctx, order, "provider_identity_mismatch", provider)
			}
			result, err := s.pool.Exec(ctx, "UPDATE "+provider.table()+" SET invoice_id=$2 WHERE order_id=$1 AND (invoice_id IS NULL OR invoice_id=$2)", order, id)
			if err != nil || result.RowsAffected() != 1 {
				return s.cryptoReview(ctx, order, "provider_identity_conflict", provider)
			}
			c.id = &id
		}
	}
	waiting, err := s.refreshCrypto(ctx, c)
	if err != nil {
		return err
	}
	if waiting {
		return river.JobSnooze(10 * time.Second)
	}
	return nil
}

func (s *Service) receiveCrypto(ctx context.Context, raw []byte, provider cryptoProvider) error {
	if len(raw) > 16384 {
		return failure(400, "INVALID_INPUT")
	}
	canonical, sign, err := cryptoSignedJSON(raw)
	if err != nil {
		return err
	}
	var notice struct {
		Type  string `json:"type"`
		ID    string `json:"uuid"`
		Order string `json:"order_id"`
	}
	if json.Unmarshal(raw, &notice) != nil || notice.Type != "payment" {
		return failure(400, "INVALID_INPUT")
	}
	order, e1 := uuid.Parse(notice.Order)
	invoice, e2 := uuid.Parse(notice.ID)
	if e1 != nil || e2 != nil || order == uuid.Nil || invoice == uuid.Nil || order.String() != notice.Order || invoice.String() != notice.ID {
		return failure(400, "INVALID_INPUT")
	}
	got, err := hex.DecodeString(sign)
	merchantID, key, _ := provider.config(s.config())
	if err != nil || len(got) != md5.Size || hex.EncodeToString(got) != sign || key == "" || subtle.ConstantTimeCompare(got, cryptoSign(canonical, key)) != 1 {
		return failure(403, "INVALID_CREDENTIALS")
	}
	c, err := s.cryptoCheckout(ctx, order, provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return unavailable()
	}
	if c.merchant != merchantID {
		return s.cryptoReview(ctx, order, "provider_config_changed", provider)
	}
	_, err = s.refreshCrypto(ctx, c)
	return err
}

func (s *Service) refreshCrypto(ctx context.Context, c cryptoRow) (bool, error) {
	body, _ := json.Marshal(map[string]string{"order_id": c.order.String()})
	p, _, err := s.cryptoRequest(ctx, c, "/info", body)
	if err != nil {
		return false, err
	}
	return s.recordCrypto(ctx, c, p)
}

var cryptoDecimal = regexp.MustCompile(`^[0-9]{1,40}(\.[0-9]{1,40})?$`)
var cryptoTicker = regexp.MustCompile(`^[A-Z0-9]{1,16}$`)

func cryptoNumber(value string) (*big.Rat, bool) {
	if !cryptoDecimal.MatchString(value) {
		return nil, false
	}
	v, ok := new(big.Rat).SetString(value)
	return v, ok
}
func cryptoUSD(value string) (int64, error) {
	if !cryptoDecimal.MatchString(value) {
		return 0, failure(400, "INVALID_INPUT")
	}
	parts := strings.SplitN(value, ".", 2)
	if len(parts) == 2 && len(parts[1]) > 2 {
		if strings.Trim(parts[1][2:], "0") != "" {
			return 0, failure(400, "INVALID_INPUT")
		}
		value = parts[0] + "." + parts[1][:2]
	}
	return minorUnits(value)
}

func (s *Service) recordCrypto(ctx context.Context, c cryptoRow, payment cryptoPayment) (bool, error) {
	provider := c.provider
	tx, p, err := s.cryptoLockedOrder(ctx, c.order)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, "SELECT invoice_id FROM "+provider.table()+" WHERE order_id=$1 FOR UPDATE", c.order).Scan(&c.id); err != nil {
		return false, unavailable()
	}
	id, idErr := uuid.Parse(payment.ID)
	gross, amountErr := cryptoUSD(payment.Amount)
	status := payment.Status
	waiting := status == "check" || status == "process" || status == "confirm_check"
	terminalEmpty := status == "cancel" || status == "fail" || status == "system_fail"
	paid := status == "paid" || status == "paid_over"
	observation, _ := json.Marshal(map[string]any{"provider": string(provider), "valid_status": waiting || terminalEmpty || paid, "order_matches": payment.Order == c.order.String(), "is_final": payment.Final})
	review := func(reason string) (bool, error) {
		if err := s.cryptoReviewTx(ctx, tx, c.order, reason, observation, provider); err != nil {
			return false, err
		}
		return false, tx.Commit(ctx)
	}
	if idErr != nil || id == uuid.Nil || id.String() != payment.ID || c.id != nil && id != *c.id || payment.Order != c.order.String() {
		return review("provider_identity_mismatch")
	}
	if amountErr != nil || gross <= 0 || payment.Currency != "USD" || payment.Final == nil || payment.PaymentStatus != status {
		return review("provider_observation_invalid")
	}
	if c.id == nil {
		if _, err = tx.Exec(ctx, "UPDATE "+provider.table()+" SET invoice_id=$2 WHERE order_id=$1", c.order, id); err != nil {
			return false, unavailable()
		}
		c.id = &id
	}
	operation := string(provider) + ":" + id.String()
	var originalOrder uuid.UUID
	var originalGross int64
	var originalCurrency, originalType string
	var originalProof []byte
	err = tx.QueryRow(ctx, "SELECT order_id,gross_minor,currency,notification_type,provider_data FROM purchase_receipts WHERE operation_id=$1 FOR UPDATE", operation).Scan(&originalOrder, &originalGross, &originalCurrency, &originalType, &originalProof)
	hadReceipt := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, unavailable()
	}
	if !paid {
		if hadReceipt && waiting {
			return false, tx.Commit(ctx)
		}
		if hadReceipt {
			return review("provider_status_conflict")
		}
		if !waiting && !terminalEmpty {
			return review("provider_status_review")
		}
		if gross != p.amount || *payment.Final == waiting {
			return review("provider_observation_invalid")
		}
		if terminalEmpty {
			if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET payment_status='canceled',active=false WHERE id=$1 AND payment_status='pending'", c.order); err != nil {
				return false, unavailable()
			}
			if _, err = tx.Exec(ctx, "UPDATE "+provider.table()+" SET state='unavailable',observation=$2 WHERE order_id=$1", c.order, observation); err != nil {
				return false, unavailable()
			}
			return false, tx.Commit(ctx)
		}
		state := "preparing"
		var link *string
		if validCryptoURL(payment.URL, provider) {
			state = "ready"
			link = &payment.URL
		}
		if _, err = tx.Exec(ctx, "UPDATE "+provider.table()+" SET checkout_url=COALESCE(checkout_url,$2),state=$3,observation=$4 WHERE order_id=$1", c.order, link, state, observation); err != nil {
			return false, unavailable()
		}
		return true, tx.Commit(ctx)
	}
	created, createdErr := time.Parse(time.RFC3339Nano, payment.Created)
	updated, updatedErr := time.Parse(time.RFC3339Nano, payment.Updated)
	if createdErr != nil || updatedErr != nil || updated.Before(created) || updated.After(s.now().Add(5*time.Minute)) || created.Before(p.created.Add(-5*time.Minute)) {
		return review("provider_observation_invalid")
	}
	actual, actualOK := cryptoNumber(payment.PaymentAmount)
	payer, payerOK := cryptoNumber(payment.PayerAmount)
	merchant, merchantOK := cryptoNumber(payment.MerchantAmount)
	if !*payment.Final || !actualOK || !payerOK || !merchantOK || actual.Sign() <= 0 || payer.Sign() <= 0 || merchant.Sign() < 0 || actual.Cmp(payer) < 0 || !cryptoTicker.MatchString(payment.PayerCurrency) {
		return review("provider_payment_invalid")
	}
	created = created.UTC().Truncate(time.Microsecond)
	updated = updated.UTC().Truncate(time.Microsecond)
	proof, _ := json.Marshal(map[string]any{"provider": string(provider), "invoice_id": id.String(), "merchant_id": c.merchant, "order_id": c.order.String(), "status": status, "payment_status": payment.PaymentStatus, "is_final": true, "amount_minor": strconv.FormatInt(gross, 10), "currency": "USD", "payment_amount": payment.PaymentAmount, "payer_amount": payment.PayerAmount, "merchant_amount": payment.MerchantAmount, "payer_currency": payment.PayerCurrency, "created_at": created.Format(time.RFC3339Nano)})
	if hadReceipt {
		if originalOrder != c.order || originalGross != gross || originalCurrency != "USD" || originalType != string(provider)+"."+status || !equalCryptoProof(originalProof, proof) {
			if _, err = tx.Exec(ctx, "UPDATE purchase_receipts SET review_reason='conflicting_operation_id' WHERE operation_id=$1", operation); err != nil {
				return false, unavailable()
			}
			if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET review_required=true,review_reason='conflicting_operation_id',active=false,fulfillment_status=CASE WHEN access_operation_id IS NULL THEN 'needs_review' ELSE fulfillment_status END WHERE id=$1", originalOrder); err != nil {
				return false, unavailable()
			}
			return review("conflicting_operation_id")
		}
		return false, tx.Commit(ctx) // updated_at alone never rewrites the first financial time.
	}
	var otherPaid bool
	if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM purchase_orders WHERE account_id=$1 AND id<>$2 AND payment_status='paid')", p.account, c.order).Scan(&otherPaid); err != nil {
		return false, unavailable()
	}
	reason := ""
	switch {
	case p.method != string(provider):
		reason = "payment_method_mismatch"
	case gross != p.amount:
		reason = "payment_mismatch"
	case p.paymentStatus == "canceled" || updated.After(p.expires):
		reason = "late_or_canceled"
	case p.review:
		reason = "order_requires_review"
	case p.paymentStatus == "paid" || otherPaid:
		reason = "another_first_payment"
	}
	result, err := tx.Exec(ctx, "INSERT INTO purchase_receipts(operation_id,order_id,occurred_at,gross_minor,net_minor,currency,notification_type,codepro,unaccepted,review_reason,created_at,provider_data) VALUES($1,$2,$3,$4,NULL,'USD',$5,false,false,NULLIF($6,''),$7,$8) ON CONFLICT DO NOTHING", operation, c.order, updated, gross, string(provider)+"."+status, reason, s.now(), proof)
	if err != nil || result.RowsAffected() != 1 {
		return false, unavailable()
	}
	if _, err = tx.Exec(ctx, "UPDATE "+provider.table()+" SET state='unavailable',observation=$2 WHERE order_id=$1", c.order, proof); err != nil {
		return false, unavailable()
	}
	if reason != "" {
		if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET payment_status='paid',paid_at=COALESCE(paid_at,$2),active=false,review_required=true,review_reason=$3,fulfillment_status=CASE WHEN access_operation_id IS NULL THEN 'needs_review' ELSE fulfillment_status END WHERE id=$1", c.order, updated, reason); err != nil {
			return false, unavailable()
		}
	} else {
		if s.queue == nil || s.queue() == nil {
			return false, unavailable()
		}
		if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET payment_status='paid',paid_at=$2,active=false,fulfillment_status='queued',funding_operation_id=$3 WHERE id=$1", c.order, updated, operation); err != nil {
			return false, unavailable()
		}
		if _, err = tx.Exec(ctx, "UPDATE purchase_orders SET active=false WHERE account_id=$1 AND id<>$2", p.account, c.order); err != nil {
			return false, unavailable()
		}
		if _, err = s.queue().InsertTx(ctx, tx, PurchaseArgs{OrderID: c.order}, &river.InsertOpts{Queue: "provision", MaxAttempts: 1000000}); err != nil {
			return false, unavailable()
		}
	}
	return false, tx.Commit(ctx)
}

func equalCryptoProof(a, b []byte) bool {
	var x, y map[string]any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	for _, proof := range []map[string]any{x, y} {
		for _, field := range []string{"payment_amount", "payer_amount", "merchant_amount"} {
			text, ok := proof[field].(string)
			if !ok {
				return false
			}
			amount, ok := cryptoNumber(text)
			if !ok {
				return false
			}
			proof[field] = amount.RatString()
		}
	}
	return reflect.DeepEqual(x, y)
}
