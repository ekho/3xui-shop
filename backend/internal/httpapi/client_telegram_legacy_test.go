package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/notifications"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
)

func clientLegacyFact(t *testing.T, e *testkit.Env, account uuid.UUID, id, tg int64, packed string) {
	t.Helper()
	payment := "owned-legacy-" + uuid.NewString()
	hash := sha256.Sum256([]byte(payment))
	if _, err := e.Pool.Exec(context.Background(), `INSERT INTO legacy_payment_transactions(source_id,account_id,source_legacy_user_id,source_tg_id,source_payment_id,payment_id_hash,subscription,status,created_at,updated_at,imported_at)
 VALUES($1,$2,$3,$3,$4,$5,$6,'completed',$7,$7,$7)`, id, account, tg, payment, hash[:], packed, e.Clock()); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyTelegramReference(t *testing.T) {
	_, s, e, _ := bridgeFixture(t)
	ctx := context.Background()
	a, _, err := s.Accounts.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 701, DisplayName: "Owned", Locale: "ru"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := s.Accounts.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 702, DisplayName: "Other", Locale: "ru"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	const packed = "subscription:pay_yoomoney:0:0:701:1:30:15:100.00"
	clientLegacyFact(t, e, b.Account.ID, 12, 702, packed)
	if ref, err := s.Payments.LegacyTelegramReference(ctx, 701, packed); err != nil || ref != nil {
		t.Fatal("foreign invoice exposed", err)
	}
	clientLegacyFact(t, e, a.Account.ID, 11, 701, packed)
	if ref, err := s.Payments.LegacyTelegramReference(ctx, 701, packed); err != nil || ref == nil || *ref != "11" {
		t.Fatal("owned exact invoice lost", err)
	}
	if ref, err := s.Payments.LegacyTelegramReference(ctx, 701, strings.Replace(packed, "100.00", "100", 1)); err != nil || ref != nil {
		t.Fatal("immutable snapshot normalized into false match", err)
	}
	if ref, err := s.Payments.LegacyTelegramReference(ctx, 701, strings.Replace(packed, "100.00", "0", 1)); err != nil || ref != nil {
		t.Fatal("zero price invented invoice identity", err)
	}
	if _, err := s.Payments.LegacyTelegramReference(ctx, 702, packed); err == nil {
		t.Fatal("foreign target accepted")
	}
	clientLegacyFact(t, e, a.Account.ID, 13, 701, packed)
	if ref, err := s.Payments.LegacyTelegramReference(ctx, 701, packed); err != nil || ref != nil {
		t.Fatal("ambiguous invoice selected one payment", err)
	}
	var retained string
	if err = e.Pool.QueryRow(ctx, "SELECT subscription FROM legacy_payment_transactions WHERE source_id=11").Scan(&retained); err != nil || retained != packed {
		t.Fatal("legacy reference mutated archive", err)
	}
}

func TestClientTelegramHistoryFilter(t *testing.T) {
	h, e, cfg, key := miniAppHTTPFixture(t)
	web := supportLogin(t, h, e, cfg, "filtered@example.test")
	other := supportLogin(t, h, e, cfg, "foreign-filter@example.test")
	mini := identityMiniLogin(t, h, e, cfg, key, `{"id":701,"first_name":"Filter owner","language_code":"ru"}`)
	clientLegacyFact(t, e, web.id, 11, 601, "unparsed immutable web fact")
	clientLegacyFact(t, e, other.id, 12, 602, "unparsed immutable foreign fact")
	clientLegacyFact(t, e, mini.Account.AccountId, 13, 701, "subscription:pay_yoomoney:0:0:701:1:30:15:100.00")
	read := func(in string, isMini bool, want int) wire.PaymentHistoryPage {
		t.Helper()
		var rr *httptest.ResponseRecorder
		if isMini {
			rr = miniAppRequest(h, "POST", "/api/v1/payment-history", in, cfg.HTTP.CabinetOrigin, mini.SessionToken, mini.CsrfToken, "")
		} else {
			rr = supportRequest(h, &web, "POST", "/api/v1/payment-history", "application/json", []byte(in), cfg.HTTP.CabinetOrigin, uuid.Nil)
		}
		if rr.Code != want || rr.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("filtered history: want%d got%d", want, rr.Code)
		}
		var out wire.PaymentHistoryPage
		if want == 200 && json.Unmarshal(rr.Body.Bytes(), &out) != nil {
			t.Fatal("invalid filter response")
		}
		if strings.Contains(rr.Body.String(), "subscription:") || strings.Contains(rr.Body.String(), "immutable foreign") {
			t.Fatal("private archive input exposed")
		}
		return out
	}
	for _, isMini := range []bool{false, true} {
		own := "11"
		if isMini {
			own = "13"
		}
		if out := read(`{"kind":"legacy","legacy_source_id":"`+own+`"}`, isMini, 200); len(out.LegacyTransactions) != 1 || out.LegacyTransactions[0].SourceId != own || out.HasMore {
			t.Fatal("own exact filtered page incorrect")
		}
		for _, foreign := range []string{"12", "999"} {
			if out := read(`{"kind":"legacy","legacy_source_id":"`+foreign+`"}`, isMini, 200); len(out.LegacyTransactions) != 0 || out.HasMore {
				t.Fatal("filter bypassed account ownership")
			}
		}
		for _, bad := range []string{`{"kind":"orders","legacy_source_id":"11"}`, `{"kind":"legacy","legacy_source_id":"0"}`, `{"kind":"legacy","legacy_source_id":"01"}`, `{"kind":"legacy","legacy_source_id":"9223372036854775808"}`, `{"kind":"legacy","legacy_source_id":"11","before_id":"10","before_created_at":"2026-10-01T00:00:00Z"}`} {
			read(bad, isMini, 400)
		}
	}
	noCSRF := web
	noCSRF.csrf = ""
	if rr := supportRequest(h, &noCSRF, "POST", "/api/v1/payment-history", "application/json", []byte(`{"kind":"legacy","legacy_source_id":"11"}`), cfg.HTTP.CabinetOrigin, uuid.Nil); rr.Code != 403 {
		t.Fatal("filter bypassed CSRF")
	}
	if rr := miniAppRequest(h, "POST", "/api/v1/payment-history", `{"kind":"legacy","legacy_source_id":"13"}`, "https://foreign.example.test", mini.SessionToken, mini.CsrfToken, ""); rr.Code != 403 {
		t.Fatal("filter bypassed Origin")
	}
}

func TestClientTelegramCloseOwnership(t *testing.T) {
	for _, mode := range []string{"current", "foreign", "message", "pending", "version", "quarantine", "restriction"} {
		t.Run(mode, func(t *testing.T) {
			_, e, cfg := httpFixture(t)
			cfg.Accounts.Now = e.Clock
			s := app.NewModules(e.Pool, e.Redis, nil, &cfg)
			ctx := context.Background()
			a, _, err := s.Accounts.StartTelegramSession(ctx, accounts.TelegramSessionInput{TelegramInput: accounts.TelegramInput{TelegramID: 701, DisplayName: "Close owner", Locale: "ru"}, AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
			if err != nil {
				t.Fatal(err)
			}
			tx, err := e.Pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			if err = s.Notifications.EnqueueClientTx(ctx, tx, notifications.ClientNotice{AccountID: a.Account.ID, TelegramID: 701, CredentialVersion: a.Account.CredentialVersion, Locale: "ru", EventKey: "fixture:close", Route: "cabinet"}, e.Clock()); err != nil {
				t.Fatal(err)
			}
			if err = tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			j, err := s.Notifications.ClaimClient(ctx)
			if err != nil || j == nil {
				t.Fatal("close claim", err)
			}
			if mode != "pending" {
				if err = s.Notifications.DeliverClient(ctx, *j, func() (notifications.ClientOutcome, error) {
					return notifications.ClientOutcome{State: "sent", MessageID: 42}, nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			tg, message := int64(701), int64(42)
			switch mode {
			case "foreign":
				tg = 702
			case "message":
				message = 43
			case "version":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET credential_version=credential_version+1 WHERE id=$1", a.Account.ID)
			case "quarantine":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET telegram_login_disabled=true WHERE id=$1", a.Account.ID)
			case "restriction":
				_, err = e.Pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1", a.Account.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			closes := 0
			valid, err := s.Notifications.CloseClient(ctx, j.ID, tg, message, func() error { closes++; return nil })
			if err != nil || valid != (mode == "current") || closes != map[bool]int{true: 1, false: 0}[mode == "current"] {
				t.Fatal("close ownership bypass", mode, valid, closes, err)
			}
		})
	}
}
