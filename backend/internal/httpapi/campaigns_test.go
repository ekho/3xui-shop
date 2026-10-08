package httpapi

import (
	"context"
	"encoding/json"
	"example.com/cabinet/backend/internal/app"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// An inactive/unknown source must not prevent signup or invent a campaign association.
func TestCampaignsInactiveSourcePreserved(t *testing.T) {
	h, e, cfg, _ := miniAppHTTPFixture(t)
	ctx := context.Background()
	actor := campaignOperator(t, h, e, cfg)
	paused := campaignCreate(t, h, actor, cfg, "Paused")
	for _, tc := range []struct {
		email, code string
		pause       bool
	}{
		{"paused@example.test", *paused.Code, true}, {"unknown@example.test", "unknown_source", false},
	} {
		body, _ := json.Marshal(map[string]string{"email": tc.email, "locale": "en", "accepted_terms_version": "1", "accepted_privacy_version": "1", "source_code": tc.code})
		r := request(h, "POST", "/api/v1/auth/register", string(body), cfg.HTTP.CabinetOrigin)
		var proof wire.RegistrationAccepted
		if r.Code != 202 || json.Unmarshal(r.Body.Bytes(), &proof) != nil {
			t.Fatal("source request", r.Code)
		}
		if tc.pause {
			state := []byte(`{"state":"paused","expected_revision":1,"reason":"disable before consent completes"}`)
			if r := supportRequest(h, &actor, "POST", "/api/v1/operator/campaigns/"+paused.ID.String()+"/state", "application/json", state, cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 200 {
				t.Fatal("pause", r.Code)
			}
		}
		_, token, _ := testkit.MailSecrets(t, e.Pool, cfg.Mail.MailKey, proof.ChallengeId)
		body, _ = json.Marshal(map[string]string{"token": token, "new_password": "long safe campaign password"})
		if r := request(h, "POST", "/api/v1/auth/verify-email", string(body), cfg.HTTP.CabinetOrigin); r.Code != 200 {
			t.Fatal("inactive source blocked signup", r.Code)
		}
		var source string
		var count int
		if err := e.Pool.QueryRow(ctx, "SELECT registration_source_code FROM accounts WHERE email_key=$1", tc.email).Scan(&source); err != nil || source != tc.code {
			t.Fatal("inactive raw source lost")
		}
		if err := e.Pool.QueryRow(ctx, "SELECT count(*) FROM campaign_acquisitions").Scan(&count); err != nil || count != 0 {
			t.Fatal("inactive cohort added", count)
		}
	}
}
func campaignWaitBlocked(t *testing.T, e *testkit.Env, ctx context.Context) {
	t.Helper()
	for {
		var count int
		if err := e.Pool.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND cardinality(pg_blocking_pids(pid))>0").Scan(&count); err != nil {
			t.Fatal("campaign lock barrier", err)
		}
		if count > 0 {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("campaign lock barrier timed out")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// A source SELECT without its row lock would assign the old active row while pause commits.
func TestCampaignsConcurrentPauseWinsRegistration(t *testing.T) {
	h, e, cfg, key := miniAppHTTPFixture(t)
	actor := campaignOperator(t, h, e, cfg)
	campaign := campaignCreate(t, h, actor, cfg, "Race pause")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "SELECT id FROM campaigns WHERE id=$1 FOR UPDATE", campaign.ID); err != nil {
		t.Fatal(err)
	}
	raw := testkit.SignedMiniAppData(key, 123, e.Clock(), `{"id":889,"first_name":"Blocked campaign","language_code":"en"}`, *campaign.Code)
	input, _ := json.Marshal(map[string]string{"init_data": raw, "accepted_terms_version": "1", "accepted_privacy_version": "1"})
	done := make(chan int, 1)
	go func() {
		done <- request(h, "POST", "/api/v1/telegram/mini-app/session", string(input), cfg.HTTP.CabinetOrigin).Code
	}()
	campaignWaitBlocked(t, e, ctx)
	if _, err = tx.Exec(ctx, "UPDATE campaigns SET state='paused',revision=2 WHERE id=$1", campaign.ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 200 {
			t.Fatal("signup blocked by pause", code)
		}
	case <-ctx.Done():
		t.Fatal("signup did not finish")
	}
	var count int
	if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM campaign_acquisitions WHERE campaign_id=$1", campaign.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("stale active source assigned", count, err)
	}
}

// Revocation after transport authentication must still win before the owner write/replay.
func TestCampaignsConcurrentRoleRevocation(t *testing.T) {
	h, e, cfg, _ := miniAppHTTPFixture(t)
	actor := campaignOperator(t, h, e, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "SELECT id FROM accounts WHERE id=$1 FOR UPDATE", actor.id); err != nil {
		t.Fatal(err)
	}
	done := make(chan int, 1)
	go func() {
		done <- supportRequest(h, &actor, "POST", "/api/v1/operator/campaigns", "application/json", []byte(`{"name":"Must not exist","reason":"race"}`), cfg.HTTP.CabinetOrigin, uuid.New()).Code
	}()
	campaignWaitBlocked(t, e, ctx)
	if _, err = tx.Exec(ctx, "DELETE FROM operator_accounts WHERE account_id=$1", actor.id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 403 {
			t.Fatal("revoked role wrote", code)
		}
	case <-ctx.Done():
		t.Fatal("revoked write did not finish")
	}
	var count int
	if err = e.Pool.QueryRow(ctx, "SELECT count(*) FROM campaigns").Scan(&count); err != nil || count != 0 {
		t.Fatal("unauthorized campaign added", count, err)
	}
}

type campaignHTTPRow struct {
	ID        uuid.UUID `json:"campaign_id"`
	Code      *string   `json:"code"`
	State     string    `json:"state"`
	Revision  int64     `json:"revision"`
	WebVisits int64     `json:"web_visits"`
}

func campaignOperator(t *testing.T, h http.Handler, e *testkit.Env, cfg app.Config) supportSession {
	t.Helper()
	actor := supportLogin(t, h, e, cfg, "campaign-operator@example.test")
	if _, err := e.Pool.Exec(context.Background(), "INSERT INTO operator_accounts(account_id,granted_at) VALUES($1,$2)", actor.id, e.Clock()); err != nil {
		t.Fatal(err)
	}
	return actor
}
func campaignCreate(t *testing.T, h http.Handler, actor supportSession, cfg app.Config, name string) campaignHTTPRow {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name, "reason": "owned campaign acceptance"})
	r := supportRequest(h, &actor, "POST", "/api/v1/operator/campaigns", "application/json", body, cfg.HTTP.CabinetOrigin, uuid.New())
	var row campaignHTTPRow
	if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &row) != nil || row.ID == uuid.Nil || row.Code == nil {
		t.Fatalf("campaign create want201/new link, got%d", r.Code)
	}
	return row
}

// Removing a role/CSRF guard, replay protection or atomic counter breaks real HTTP/DB behavior.
func TestCampaignsLifecycleHTTP(t *testing.T) {
	h, e, cfg, _ := miniAppHTTPFixture(t)
	ctx := context.Background()
	actor := campaignOperator(t, h, e, cfg)
	body := []byte(`{"name":"<Campaign>","reason":"new campaign"}`)
	key := uuid.New()
	r := supportRequest(h, &actor, "POST", "/api/v1/operator/campaigns", "application/json", body, cfg.HTTP.CabinetOrigin, key)
	var row campaignHTTPRow
	if r.Code != 201 || json.Unmarshal(r.Body.Bytes(), &row) != nil || row.Code == nil {
		t.Fatalf("new campaign want201 got%d", r.Code)
	}
	replay := supportRequest(h, &actor, "POST", "/api/v1/operator/campaigns", "application/json", body, cfg.HTTP.CabinetOrigin, key)
	if replay.Code != 201 || replay.Body.String() != r.Body.String() {
		t.Fatal("create replay changed snapshot")
	}
	changed := []byte(`{"name":"different","reason":"new campaign"}`)
	if r := supportRequest(h, &actor, "POST", "/api/v1/operator/campaigns", "application/json", changed, cfg.HTTP.CabinetOrigin, key); r.Code != 409 {
		t.Fatal("different body replay", r.Code)
	}
	noCSRF := actor
	noCSRF.csrf = ""
	for _, tc := range []struct {
		session *supportSession
		origin  string
		want    int
	}{
		{nil, cfg.HTTP.CabinetOrigin, 401}, {&noCSRF, cfg.HTTP.CabinetOrigin, 403}, {&actor, "https://foreign.example.test", 403},
	} {
		if r := supportRequest(h, tc.session, "POST", "/api/v1/operator/campaigns", "application/json", changed, tc.origin, uuid.New()); r.Code != tc.want {
			t.Fatal("write authority", r.Code, tc.want)
		}
	}
	if r := supportRequest(h, &actor, "POST", "/api/v1/operator/campaigns?actor=forged", "application/json", changed, cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 400 {
		t.Fatal("query injection", r.Code)
	}
	if r := supportRequest(h, &actor, "POST", "/api/v1/operator/campaigns", "application/json", body, cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != 409 {
		t.Fatal("name reuse", r.Code)
	}
	visit, _ := json.Marshal(map[string]string{"code": *row.Code})
	var wg sync.WaitGroup
	responses := make(chan int, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses <- supportRequest(h, nil, "POST", "/api/v1/campaign-visits", "application/json", visit, cfg.HTTP.CabinetOrigin, uuid.Nil).Code
		}()
	}
	wg.Wait()
	close(responses)
	for code := range responses {
		if code != 204 {
			t.Fatal("concurrent visit", code)
		}
	}
	var count int64
	if err := e.Pool.QueryRow(ctx, "SELECT web_visits FROM campaigns WHERE id=$1", row.ID).Scan(&count); err != nil || count != 20 {
		t.Fatal("lost visit", count, err)
	}
	for i := 0; i < 10; i++ {
		if r := supportRequest(h, nil, "POST", "/api/v1/campaign-visits", "application/json", visit, cfg.HTTP.CabinetOrigin, uuid.Nil); r.Code != 204 {
			t.Fatal("within visit limit", r.Code)
		}
	}
	if r := supportRequest(h, nil, "POST", "/api/v1/campaign-visits", "application/json", visit, cfg.HTTP.CabinetOrigin, uuid.Nil); r.Code != 429 || r.Header().Get("Retry-After") == "" {
		t.Fatal("visit limit", r.Code)
	}
	path := "/api/v1/operator/campaigns/" + row.ID.String() + "/state"
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"state":"paused","expected_revision":1,"reason":"pause"}`, 200},
		{`{"state":"active","expected_revision":1,"reason":"stale"}`, 409},
		{`{"state":"active","expected_revision":2,"reason":"resume"}`, 200},
		{`{"state":"deleted","expected_revision":3,"reason":"delete"}`, 200},
		{`{"state":"active","expected_revision":4,"reason":"undelete"}`, 409},
	} {
		if r := supportRequest(h, &actor, "POST", path, "application/json", []byte(tc.body), cfg.HTTP.CabinetOrigin, uuid.New()); r.Code != tc.want {
			t.Fatal("state", r.Code, tc.want)
		}
	}
	e.Advance(time.Minute)
	if r := supportRequest(h, nil, "POST", "/api/v1/campaign-visits", "application/json", visit, cfg.HTTP.CabinetOrigin, uuid.Nil); r.Code != 204 {
		t.Fatal("deleted visit response", r.Code)
	}
	if err := e.Pool.QueryRow(ctx, "SELECT web_visits FROM campaigns WHERE id=$1", row.ID).Scan(&count); err != nil || count != 30 {
		t.Fatal("deleted acquired visit", count, err)
	}
	if err := e.Pool.QueryRow(ctx, "SELECT count(*) FROM campaign_events WHERE campaign_id=$1", row.ID).Scan(&count); err != nil || count != 4 {
		t.Fatal("duplicate/missing audit", count, err)
	}
	if _, err := e.Pool.Exec(ctx, "DELETE FROM campaigns WHERE id=$1", row.ID); err == nil {
		t.Fatal("campaign hard delete permitted")
	}
	if _, err := e.Pool.Exec(ctx, "DELETE FROM campaign_events WHERE campaign_id=$1", row.ID); err == nil {
		t.Fatal("audit delete permitted")
	}
	if _, err := e.Pool.Exec(ctx, "DELETE FROM operator_accounts WHERE account_id=$1", actor.id); err != nil {
		t.Fatal(err)
	}
	if r := supportRequest(h, &actor, "POST", "/api/v1/operator/campaigns", "application/json", body, cfg.HTTP.CabinetOrigin, key); r.Code != 403 {
		t.Fatal("revoked role replay", r.Code)
	}
}

// Losing source at resend or applying late start/link would silently move the original cohort.
func TestCampaignsFirstRegistrationSource(t *testing.T) {
	h, e, cfg, key := miniAppHTTPFixture(t)
	ctx := context.Background()
	actor := campaignOperator(t, h, e, cfg)
	first := campaignCreate(t, h, actor, cfg, "First")
	other := campaignCreate(t, h, actor, cfg, "Other")
	body, _ := json.Marshal(map[string]string{"email": "campaign-user@example.test", "locale": "en", "accepted_terms_version": "1", "accepted_privacy_version": "1", "source_code": *first.Code})
	r := request(h, "POST", "/api/v1/auth/register", string(body), cfg.HTTP.CabinetOrigin)
	var challenge wire.RegistrationAccepted
	if r.Code != 202 || json.Unmarshal(r.Body.Bytes(), &challenge) != nil {
		t.Fatal("source registration", r.Code)
	}
	_, oldToken, _ := testkit.MailSecrets(t, e.Pool, cfg.Mail.MailKey, challenge.ChallengeId)
	e.Advance(61 * time.Second)
	if r := request(h, "POST", "/api/v1/auth/resend-verification", `{"email":"campaign-user@example.test"}`, cfg.HTTP.CabinetOrigin); r.Code != 202 {
		t.Fatal("resend", r.Code)
	}
	var latest uuid.UUID
	if err := e.Pool.QueryRow(ctx, "SELECT id FROM registration_challenges WHERE email_key=$1 AND NOT revoked", "campaign-user@example.test").Scan(&latest); err != nil {
		t.Fatal(err)
	}
	_, token, _ := testkit.MailSecrets(t, e.Pool, cfg.Mail.MailKey, latest)
	verify := func(token string) int {
		body, _ := json.Marshal(map[string]string{"token": token, "new_password": "long safe campaign password"})
		return request(h, "POST", "/api/v1/auth/verify-email", string(body), cfg.HTTP.CabinetOrigin).Code
	}
	oldStatus, newStatus, replayStatus := verify(oldToken), verify(token), verify(token)
	if oldStatus != 400 || newStatus != 200 || replayStatus != 400 {
		t.Fatalf("verification statuses old/new/replay: %d/%d/%d", oldStatus, newStatus, replayStatus)
	}
	var id, campaign uuid.UUID
	var source string
	if err := e.Pool.QueryRow(ctx, "SELECT id,registration_source_code FROM accounts WHERE email_key=$1", "campaign-user@example.test").Scan(&id, &source); err != nil || source != *first.Code {
		t.Fatal("first source lost", err)
	}
	if err := e.Pool.QueryRow(ctx, "SELECT campaign_id FROM campaign_acquisitions WHERE account_id=$1", id).Scan(&campaign); err != nil || campaign != first.ID {
		t.Fatal("cohort source", err)
	}
	if _, err := e.Pool.Exec(ctx, "UPDATE accounts SET registration_source_code=$2 WHERE id=$1", id, *other.Code); err == nil {
		t.Fatal("source mutable")
	}
	for _, tc := range []struct{ tg, source string }{{"881", *first.Code}, {"882", "123456789"}, {"883", strings.Repeat("a", 512)}} {
		raw := testkit.SignedMiniAppData(key, 123, e.Clock(), `{"id":`+tc.tg+`,"first_name":"Campaign client","language_code":"en"}`, tc.source)
		input, _ := json.Marshal(map[string]string{"init_data": raw, "accepted_terms_version": "1", "accepted_privacy_version": "1"})
		r := request(h, "POST", "/api/v1/telegram/mini-app/session", string(input), cfg.HTTP.CabinetOrigin)
		var auth wire.MiniAppSessionResult
		if r.Code != 200 || json.Unmarshal(r.Body.Bytes(), &auth) != nil {
			t.Fatal("signed source", r.Code)
		}
		again := testkit.SignedMiniAppData(key, 123, e.Clock(), `{"id":`+tc.tg+`,"first_name":"Campaign client","language_code":"en"}`, *other.Code)
		input, _ = json.Marshal(map[string]string{"init_data": again})
		late := request(h, "POST", "/api/v1/telegram/mini-app/session", string(input), cfg.HTTP.CabinetOrigin)
		var returned wire.MiniAppSessionResult
		if late.Code != 200 || json.Unmarshal(late.Body.Bytes(), &returned) != nil || returned.Account.AccountId != auth.Account.AccountId {
			t.Fatal("late login identity")
		}
		var count int
		if err := e.Pool.QueryRow(ctx, "SELECT registration_source_code FROM accounts WHERE id=$1", auth.Account.AccountId).Scan(&source); err != nil || source != tc.source {
			t.Fatal("late source overwritten")
		}
		if err := e.Pool.QueryRow(ctx, "SELECT count(*) FROM campaign_acquisitions WHERE account_id=$1", auth.Account.AccountId).Scan(&count); err != nil {
			t.Fatal(err)
		}
		want := 1
		if tc.tg != "881" {
			want = 0
		}
		if count != want {
			t.Fatal("late source reattribution", count, want)
		}
	}
}
