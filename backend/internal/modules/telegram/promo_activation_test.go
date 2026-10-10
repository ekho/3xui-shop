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

type activationAuthority struct {
	resolved int
	actor    *accounts.Snapshot
}

func (a *activationAuthority) ResolveTelegramContext(ctx context.Context, _ int64) (context.Context, *accounts.Snapshot, error) {
	a.resolved++
	return context.WithValue(ctx, promoProofKey{}, true), a.actor, nil
}

type activationOwner struct {
	keys                 []uuid.UUID
	code                 string
	gotActor             uuid.UUID
	gotID                uuid.UUID
	statusCalls          int
	result               bonuses.PromocodeActivation
	err                  error
	requireTelegramProof bool
}

func (o *activationOwner) hasProof(ctx context.Context, actor uuid.UUID) bool {
	if ctx.Value(promoProofKey{}) == true {
		return true
	}
	proof, ok := accounts.TelegramActor(ctx)
	return o.requireTelegramProof && ok && proof.ID == actor
}

func (o *activationOwner) ActivatePromocode(ctx context.Context, actor, key uuid.UUID, in bonuses.ActivatePromocodeInput) (bonuses.PromocodeActivation, error) {
	if !o.hasProof(ctx, actor) {
		return bonuses.PromocodeActivation{}, errors.New("missing actor proof")
	}
	o.keys = append(o.keys, key)
	o.code, o.gotActor = in.Code, actor
	return o.result, o.err
}

func TestClientPromocodeRealTelegramPrincipal(t *testing.T) {
	e := testkit.Open(t)
	ctx := context.Background()
	actorID := uuid.New()
	const telegramID = int64(701)
	if _, err := e.Pool.Exec(ctx, `INSERT INTO accounts(id,email_key,locale,password_hash,verified_at,vpn_id,sub_id,panel_key,terms_version,privacy_version,telegram_id)
		VALUES($1,'promo-client@example.test','ru','fixture',$2,$3,'bbbbbbbbbbbbbbbb',$4,'1','1',$5)`, actorID, e.Clock(), uuid.New(), "acct_"+actorID.String(), telegramID); err != nil {
		t.Fatal(err)
	}
	authority := accounts.New(e.Pool, e.Redis, nil, accounts.Config{TermsVersion: "1", PrivacyVersion: "1", RateNamespace: uuid.NewString(), Now: e.Clock})
	owner := &activationOwner{requireTelegramProof: true, result: bonuses.PromocodeActivation{PromocodeID: uuid.New(), DurationDays: 7, OperationID: uuid.New(), Status: "pending"}}
	h := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/sendMessage") {
			t.Fatal("unexpected Bot API request")
		}
		var in struct {
			Chat int64 `json:"chat_id"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			t.Fatal(err)
		}
		return jsonReply(botapi.Message{ID: 42, Chat: botapi.Chat{ID: in.Chat}}), nil
	})}
	r := &Runtime{api: botapi.New(testToken, h), token: testToken}
	if err := r.ConfigureClientPromocode(authority, owner, "https://cabinet.example.test"); err != nil {
		t.Fatal(err)
	}
	u := clientMessage(telegramID, "/promocode CODE")
	if err := r.handle(ctx, u); err != nil || len(owner.keys) != 1 || owner.gotActor != actorID {
		t.Fatal("linked account was not accepted with Telegram proof", err)
	}
	status := clientMessage(telegramID, "/promocode_status "+owner.result.OperationID.String())
	status.ID, status.Message.ID = 12, 8
	if err := r.handle(ctx, status); err != nil || owner.statusCalls != 1 || owner.gotID != owner.result.OperationID {
		t.Fatal("status lookup did not use Telegram proof", err)
	}
	if _, err := e.Pool.Exec(ctx, "UPDATE accounts SET restricted=true WHERE id=$1", actorID); err != nil {
		t.Fatal(err)
	}
	if err := r.handle(ctx, status); err != nil || owner.statusCalls != 1 {
		t.Fatal("restricted identity replay reached owner", err)
	}
}

func (o *activationOwner) GetPromocodeActivation(ctx context.Context, actor, id uuid.UUID) (bonuses.PromocodeActivation, error) {
	if !o.hasProof(ctx, actor) {
		return bonuses.PromocodeActivation{}, errors.New("missing actor proof")
	}
	o.statusCalls++
	o.gotActor, o.gotID = actor, id
	return o.result, o.err
}

func TestParseClientPromocodeCommands(t *testing.T) {
	id := uuid.New()
	for _, tc := range []struct {
		text, action, value string
		handled, valid      bool
	}{
		{"/promocode Mixed_01", "activate", "Mixed_01", true, true},
		{"/promocode_status " + id.String(), "status", id.String(), true, true},
		{"/promocode", "activate", "", true, false},
		{"/promocode one two", "activate", "", true, false},
		{"/promocode_status bad", "status", "", true, false},
		{"/promocode@other_bot A", "", "", false, false},
		{"/promo A", "", "", false, false},
		{"/promocode\x00 A", "", "", false, false},
	} {
		got, handled := parseClientPromocodeCommand(tc.text, "fixture_bot")
		if handled != tc.handled || got.action != tc.action || got.value != tc.value || got.valid != tc.valid {
			t.Fatalf("parse command shape mismatch: %q => %+v handled=%t", tc.text, got, handled)
		}
	}
}

func TestClientPromocodeTelegramAdapter(t *testing.T) {
	const code = "SensitiveCode"
	actorID := uuid.New()
	opID := uuid.New()
	authority := &activationAuthority{actor: &accounts.Snapshot{ID: actorID, Locale: "en"}}
	owner := &activationOwner{result: bonuses.PromocodeActivation{PromocodeID: uuid.New(), DurationDays: 7, OperationID: opID, Status: "pending"}}
	var messages []struct {
		Text   string                 `json:"text"`
		Markup *botapi.InlineKeyboard `json:"reply_markup"`
	}
	h := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/sendMessage") {
			t.Fatal("unexpected bot API call")
		}
		var in struct {
			Chat   int64                  `json:"chat_id"`
			Text   string                 `json:"text"`
			Markup *botapi.InlineKeyboard `json:"reply_markup"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, struct {
			Text   string                 `json:"text"`
			Markup *botapi.InlineKeyboard `json:"reply_markup"`
		}{in.Text, in.Markup})
		return jsonReply(botapi.Message{ID: int64(len(messages)), Chat: botapi.Chat{ID: in.Chat}}), nil
	})}
	r := &Runtime{api: botapi.New(testToken, h), token: testToken}
	if err := r.ConfigureClientPromocode(authority, owner, "https://cabinet.example.test"); err != nil {
		t.Fatal(err)
	}
	u := clientMessage(701, "/promocode "+code)
	if err := r.handle(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if err := r.handle(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if len(owner.keys) != 2 || owner.keys[0] == uuid.Nil || owner.keys[0] != owner.keys[1] || owner.code != code || owner.gotActor != actorID {
		t.Fatal("activation did not use verified account and stable update key")
	}
	if len(messages) != 2 || strings.Contains(messages[0].Text, code) || !strings.Contains(messages[0].Text, opID.String()) || messages[0].Markup.Rows[0][0].URL != "https://cabinet.example.test/cabinet?lang=en" {
		t.Fatal("activation reply leaked code or omitted safe status/cabinet route")
	}
	status := clientMessage(701, "/promocode_status "+opID.String())
	status.ID, status.Message.ID = 12, 8
	if err := r.handle(context.Background(), status); err != nil || owner.gotID != opID || len(messages) != 3 {
		t.Fatal("status lookup did not use verified actor and operation ID", err)
	}
}

func TestClientPromocodeRejectsUnverifiedMessages(t *testing.T) {
	actorID := uuid.New()
	authority := &activationAuthority{actor: &accounts.Snapshot{ID: actorID, Locale: "ru"}}
	owner := &activationOwner{}
	h := &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("unsafe message reached Bot API")
		return nil, nil
	})}
	for _, change := range []func(*botapi.Update){
		func(u *botapi.Update) { u.Message.Chat.Type = "group" },
		func(u *botapi.Update) { u.Message.Chat.ID = 702 },
		func(u *botapi.Update) { u.Message.Raw = json.RawMessage(`{"forward_origin":{"type":"user"}}`) },
		func(u *botapi.Update) { u.Message.From.IsBot = true },
		func(u *botapi.Update) { u.ID = -1 },
	} {
		r := &Runtime{api: botapi.New(testToken, h), token: testToken}
		if err := r.ConfigureClientPromocode(authority, owner, "https://cabinet.example.test"); err != nil {
			t.Fatal(err)
		}
		u := clientMessage(701, "/promocode SECRET")
		change(&u)
		if err := r.handle(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	if authority.resolved != 0 || len(owner.keys) != 0 {
		t.Fatal("unsafe message reached account or owner")
	}
}

func TestClientPromocodeLostBotReplyReplaysSameKey(t *testing.T) {
	authority := &activationAuthority{actor: &accounts.Snapshot{ID: uuid.New(), Locale: "ru"}}
	owner := &activationOwner{result: bonuses.PromocodeActivation{PromocodeID: uuid.New(), DurationDays: 3, OperationID: uuid.New(), Status: "pending"}}
	sends := 0
	h := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "/sendMessage") {
			t.Fatal("unexpected Bot API request")
		}
		sends++
		if sends == 1 {
			return nil, errors.New("synthetic lost reply")
		}
		return jsonReply(botapi.Message{ID: 42, Chat: botapi.Chat{ID: 701}}), nil
	})}
	newRuntime := func() *Runtime {
		r := &Runtime{api: botapi.New(testToken, h), token: testToken}
		if err := r.ConfigureClientPromocode(authority, owner, "https://cabinet.example.test"); err != nil {
			t.Fatal(err)
		}
		return r
	}
	u := clientMessage(701, "/promocode SECRET")
	if err := newRuntime().handle(context.Background(), u); err == nil {
		t.Fatal("lost Bot API response acknowledged update")
	}
	if err := newRuntime().handle(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if sends != 2 || len(owner.keys) != 2 || owner.keys[0] != owner.keys[1] {
		t.Fatal("restarted Telegram adapter did not replay the same owner request")
	}
}
