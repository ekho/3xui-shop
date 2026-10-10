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
	"github.com/google/uuid"
)

type promoProofKey struct{}

type promoTestAuthority struct {
	resolved, checked int
	roleErr           error
}

func (a *promoTestAuthority) ResolveTelegramContext(ctx context.Context, _ int64) (context.Context, *accounts.Snapshot, error) {
	a.resolved++
	return context.WithValue(ctx, promoProofKey{}, true), &accounts.Snapshot{ID: uuid.MustParse("5fa2607d-dcb9-4a53-8cc5-7281a5e766fe"), Locale: "en"}, nil
}

func (a *promoTestAuthority) RequireOperator(ctx context.Context, _ uuid.UUID) error {
	a.checked++
	if ctx.Value(promoProofKey{}) != true {
		return errors.New("missing Telegram proof")
	}
	return a.roleErr
}

type promoTestOwner struct {
	actor, key, id uuid.UUID
	keys           []uuid.UUID
	commands       []string
	reason         string
	days           int
	revision       int64
	err            error
}

func (o *promoTestOwner) record(ctx context.Context, command string, actor, id, key uuid.UUID) error {
	if ctx.Value(promoProofKey{}) != true {
		return errors.New("owner received no Telegram proof")
	}
	o.actor, o.id, o.key = actor, id, key
	o.keys = append(o.keys, key)
	o.commands = append(o.commands, command)
	return o.err
}

func (o *promoTestOwner) item() bonuses.Promocode {
	return bonuses.Promocode{PromocodeID: uuid.MustParse("118667f2-4f1d-49ca-89d2-b2b0ab688312"), Code: "<code>&", DurationDays: 7, Revision: 2, State: "available"}
}

func (o *promoTestOwner) ListPromocodes(ctx context.Context, actor uuid.UUID, page, perPage int) (bonuses.PromocodeList, error) {
	if page != 1 || perPage != 10 {
		return bonuses.PromocodeList{}, errors.New("wrong pagination")
	}
	return bonuses.PromocodeList{Promocodes: []bonuses.Promocode{o.item()}, Page: 1, PerPage: 10, Total: 1}, o.record(ctx, "list", actor, uuid.Nil, uuid.Nil)
}

func (o *promoTestOwner) GetPromocode(ctx context.Context, actor, id uuid.UUID) (bonuses.PromocodeDetail, error) {
	return bonuses.PromocodeDetail{Promocode: o.item()}, o.record(ctx, "card", actor, id, uuid.Nil)
}

func (o *promoTestOwner) CreatePromocode(ctx context.Context, actor, key uuid.UUID, in bonuses.CreatePromocodeInput) (bonuses.Promocode, error) {
	o.days, o.reason = in.DurationDays, in.Reason
	return o.item(), o.record(ctx, "create", actor, uuid.Nil, key)
}

func (o *promoTestOwner) EditPromocode(ctx context.Context, actor, id, key uuid.UUID, in bonuses.EditPromocodeInput) (bonuses.Promocode, error) {
	o.days, o.reason, o.revision = in.DurationDays, in.Reason, in.ExpectedRevision
	return o.item(), o.record(ctx, "edit", actor, id, key)
}

func (o *promoTestOwner) DeletePromocode(ctx context.Context, actor, id, key uuid.UUID, in bonuses.DeletePromocodeInput) (bonuses.Promocode, error) {
	o.reason, o.revision = in.Reason, in.ExpectedRevision
	return o.item(), o.record(ctx, "delete", actor, id, key)
}

func promoRuntime(t *testing.T, authority *promoTestAuthority, owner *promoTestOwner, sent *[]string) *Runtime {
	t.Helper()
	h := &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		if !strings.HasSuffix(req.URL.Path, "sendMessage") {
			t.Fatalf("unexpected Bot API call: %s", req.URL.Path)
		}
		var input struct {
			Text   string                `json:"text"`
			Chat   int64                 `json:"chat_id"`
			Markup botapi.InlineKeyboard `json:"reply_markup"`
		}
		if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		*sent = append(*sent, input.Text)
		if len(input.Markup.Rows) != 0 {
			button := input.Markup.Rows[0][0]
			if button.URL != "https://cabinet.example.test/admin/promocodes?lang=en" || button.WebApp != nil {
				t.Fatalf("unsafe operator button: %+v", button)
			}
		}
		return jsonReply(botapi.Message{ID: 70, Chat: botapi.Chat{ID: input.Chat}}), nil
	})}
	r := &Runtime{api: botapi.New(testToken, h), token: testToken}
	if err := r.ConfigurePromocodes(authority, owner, "https://cabinet.example.test"); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestParsePromocodeCommand(t *testing.T) {
	id := "118667f2-4f1d-49ca-89d2-b2b0ab688312"
	for _, tc := range []struct {
		text, action string
		valid        bool
	}{
		{"/promocodes", "list", true},
		{"/promo " + id, "card", true},
		{"/promo_create 10 campaign reason", "create", true},
		{"/promo_edit " + id + " 2 15 correction reason", "edit", true},
		{"/promo_delete " + id + " 2 confirm requested removal", "delete", true},
		{"/promo_delete " + id + " 2 removal", "delete", false},
		{"/promo_create 0 reason", "create", false},
		{"/promo_create 366 reason", "create", false},
		{"/promo_edit " + id + " 0 1 reason", "edit", false},
		{"/promo not-a-uuid", "card", false},
	} {
		cmd, handled := parsePromocodeCommand(tc.text, "fixture_bot")
		if !handled || cmd.action != tc.action || cmd.valid != tc.valid {
			t.Fatalf("%q: %+v handled=%v", tc.text, cmd, handled)
		}
	}
	for _, text := range []string{"/promo@other_bot " + id, "/unknown", "/promocodes\x00", "hello"} {
		if _, handled := parsePromocodeCommand(text, "fixture_bot"); handled {
			t.Fatalf("unexpected command %q", text)
		}
	}
}

func TestPromocodeCommandsProofAndReplay(t *testing.T) {
	authority := &promoTestAuthority{}
	owner := &promoTestOwner{}
	var sent []string
	r := promoRuntime(t, authority, owner, &sent)
	id := "118667f2-4f1d-49ca-89d2-b2b0ab688312"
	for i, command := range []string{"/promocodes", "/promo " + id, "/promo_create 7 launch reason", "/promo_edit " + id + " 2 8 campaign fix", "/promo_delete " + id + " 3 confirm campaign ended"} {
		u := clientMessage(701, command)
		u.ID += int64(i)
		u.Message.ID += int64(i)
		if err := r.handle(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Join(owner.commands, ",") != "list,card,create,edit,delete" || owner.days != 8 || owner.revision != 3 || owner.reason != "campaign ended" || authority.checked != 5 || len(sent) != 5 {
		t.Fatalf("wrong command routing: %+v %+v %q", owner, authority, sent)
	}
	if !strings.Contains(sent[0], "&lt;code&gt;&amp;") || strings.Contains(sent[0], "<code><code>") || !strings.Contains(sent[1], "History") {
		t.Fatalf("unsafe output or missing history: %q", sent)
	}
	u := clientMessage(701, "/promo_create 7 launch reason")
	u.ID, u.Message.ID = 99, 99
	for range 2 {
		if err := r.handle(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	if owner.keys[5] == uuid.Nil || owner.keys[5] != owner.keys[6] || owner.keys[5] == owner.keys[2] || authority.checked != 7 {
		t.Fatalf("unstable key or stale role check: %+v %+v", owner.keys, authority)
	}
}

func TestPromocodeUnsafeMessagesAndRevokedRole(t *testing.T) {
	for _, kind := range []string{"group", "forwarded", "via-bot", "foreign-mention", "revoked"} {
		t.Run(kind, func(t *testing.T) {
			authority := &promoTestAuthority{}
			owner := &promoTestOwner{}
			var sent []string
			r := promoRuntime(t, authority, owner, &sent)
			u := clientMessage(701, "/promo_create 7 reason")
			switch kind {
			case "group":
				u.Message.Chat.Type = "group"
			case "forwarded":
				u.Message.Raw = json.RawMessage(`{"forward_origin":{"type":"user"}}`)
			case "via-bot":
				u.Message.Raw = json.RawMessage(`{"via_bot":{"id":999}}`)
			case "foreign-mention":
				u.Message.Text = "/promo_create@other_bot 7 reason"
			case "revoked":
				authority.roleErr = &accounts.Error{Status: 403, Code: "FORBIDDEN"}
			}
			_, err := r.promocodes.handle(context.Background(), u, "fixture_bot")
			if err != nil {
				t.Fatal(err)
			}
			if len(owner.commands) != 0 || (kind != "revoked" && authority.resolved != 0) {
				t.Fatalf("unsafe update reached owner: %+v %+v", owner, authority)
			}
		})
	}
}

func TestPromocodeHistoryAndListStaySafe(t *testing.T) {
	owner := &promoTestOwner{}
	reason := "<unsafe>&"
	detail := bonuses.PromocodeDetail{Promocode: owner.item(), Events: []bonuses.PromocodeEvent{{Action: "<edit>", Reason: &reason}, {Action: "legacy", Reason: nil}}, EventsHasMore: true}
	text := promocodeDetailText(detail, "ru")
	if strings.Contains(text, reason) || !strings.Contains(text, "&lt;unsafe&gt;&amp;") || !strings.Contains(text, "История") || !strings.Contains(text, "Остальная история") {
		t.Fatalf("unsafe or incomplete detail: %q", text)
	}
	rows := make([]bonuses.Promocode, 30)
	for i := range rows {
		rows[i] = owner.item()
		rows[i].Code = strings.Repeat("<", 100)
	}
	text = promocodeListText(bonuses.PromocodeList{Promocodes: rows}, "en")
	if len(text) > 4096 || strings.Contains(text, strings.Repeat("<", 5)) {
		t.Fatalf("unsafe or unbounded list: %d bytes", len(text))
	}
}
