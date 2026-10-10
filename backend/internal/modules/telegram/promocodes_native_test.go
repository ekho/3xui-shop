package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"example.com/cabinet/backend/internal/testkit"
	"github.com/google/uuid"
)

func TestPromocodeTelegramNativeBoundaryAndReplay(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	actor := uuid.New()
	const telegramID = int64(701)
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,telegram_id)
		VALUES($1,'promo-operator@example.test','ru','fixture',$2,$3,'aaaaaaaaaaaaaaaa',$4,'1','1',$5)`, actor, e.Clock(), uuid.New(), "acct_"+actor.String(), telegramID); err != nil {
		t.Fatal(err)
	}
	authority := accounts.New(e.Pool, e.Redis, nil, accounts.Config{TermsVersion: "1", PrivacyVersion: "1", RateNamespace: uuid.NewString(), Now: e.Clock})
	if err := authority.ChangeOperatorRole(ctx, actor, true); err != nil {
		t.Fatal(err)
	}
	owner := bonuses.New(e.Pool, authority, e.Clock)
	lostResponse := true
	sent := 0
	h := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/sendMessage") {
			t.Fatalf("unexpected external Bot API call: %s", req.URL.Path)
		}
		var input struct {
			Chat  int64  `json:"chat_id"`
			Text  string `json:"text"`
			Parse string `json:"parse_mode"`
		}
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.Chat != telegramID || input.Parse != "HTML" || input.Text == "" {
			t.Fatalf("unexpected Bot API response: %+v", input)
		}
		sent++
		if lostResponse {
			lostResponse = false
			return nil, errors.New("synthetic lost response")
		}
		return jsonReply(botapi.Message{ID: int64(sent), Chat: botapi.Chat{ID: input.Chat}}), nil
	})}
	newRuntime := func() *Runtime {
		r := &Runtime{api: botapi.New(testToken, h), token: testToken}
		if err := r.ConfigurePromocodes(authority, owner, "https://cabinet.example.test"); err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := newRuntime()
	create := clientMessage(telegramID, "/promo_create 7 operator request")
	if err := r.handle(ctx, create); err != nil {
		t.Fatal(err)
	}
	assertState := func(wantRows, wantEvents, wantAudit int64, wantDays int, wantRevision int64, wantDeleted bool) uuid.UUID {
		t.Helper()
		var rows, events, audit int64
		if err := e.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM promocodes),
			(SELECT count(*) FROM promocode_events WHERE actor_account_id=$1),
			(SELECT count(*) FROM audit_events WHERE action LIKE 'promocode.%' AND account_id=$1 AND operator_account_id=$1)`, actor).Scan(&rows, &events, &audit); err != nil {
			t.Fatal(err)
		}
		if rows != wantRows || events != wantEvents || audit != wantAudit {
			t.Fatalf("unexpected effects: rows=%d events=%d audit=%d", rows, events, audit)
		}
		var id uuid.UUID
		var days int
		var revision int64
		var deleted bool
		if err := e.Pool.QueryRow(ctx, `SELECT id,duration_days,revision,deleted_at IS NOT NULL FROM promocodes LIMIT 1`).Scan(&id, &days, &revision, &deleted); err != nil {
			t.Fatal(err)
		}
		if days != wantDays || revision != wantRevision || deleted != wantDeleted {
			t.Fatalf("unexpected promocode state: days=%d revision=%d deleted=%t", days, revision, deleted)
		}
		return id
	}
	id := assertState(1, 1, 1, 7, 1, false)
	// A new bridge receives the same update after a lost reply. Its stable key
	// must return the committed result without a second domain effect.
	r = newRuntime()
	if err := r.handle(ctx, create); err != nil {
		t.Fatal(err)
	}
	assertState(1, 1, 1, 7, 1, false)
	edit := clientMessage(telegramID, "/promo_edit "+id.String()+" 1 9 operator correction")
	edit.ID, edit.Message.ID = 12, 8
	if err := r.handle(ctx, edit); err != nil {
		t.Fatal(err)
	}
	if err := r.handle(ctx, edit); err != nil {
		t.Fatal(err)
	}
	assertState(1, 2, 2, 9, 2, false)
	deleteUpdate := clientMessage(telegramID, "/promo_delete "+id.String()+" 2 confirm operator request")
	deleteUpdate.ID, deleteUpdate.Message.ID = 13, 9
	if err := r.handle(ctx, deleteUpdate); err != nil {
		t.Fatal(err)
	}
	if err := r.handle(ctx, deleteUpdate); err != nil {
		t.Fatal(err)
	}
	assertState(1, 3, 3, 9, 3, true)
	if err := authority.ChangeOperatorRole(ctx, actor, false); err != nil {
		t.Fatal(err)
	}
	if err := r.handle(ctx, create); err != nil {
		t.Fatal(err)
	}
	assertState(1, 3, 3, 9, 3, true)
	if sent != 7 {
		t.Fatalf("unexpected Bot API responses: %d", sent)
	}
}
