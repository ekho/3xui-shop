package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
)

func TestParseServerCommand(t *testing.T) {
	for _, tc := range []struct {
		text, action, id, name, host string
		maxClients                   int64
		valid                        bool
	}{
		{"/servers", "list", "", "", "", 0, true},
		{"/server legacy.main", "card", "legacy.main", "", "", 0, true},
		{"/server_add Node A | https://node.example.test | 0", "add", "", "Node A", "https://node.example.test", 0, true},
		{"/server_add Node A | https://node.example.test | 2147483647", "add", "", "Node A", "https://node.example.test", 2147483647, true},
		{"/server_ping legacy.main", "ping", "legacy.main", "", "", 0, true},
		{"/servers_sync", "sync", "", "", "", 0, true},
		{"/server_delete legacy.main confirm", "delete", "legacy.main", "", "", 0, true},
		{"/server_delete legacy.main", "delete", "legacy.main", "", "", 0, false},
		{"/server_add Node | https://node.example.test | -1", "add", "", "", "", 0, false},
		{"/server_add Node | https://node.example.test | 2147483648", "add", "", "", "", 0, false},
		{"/servers extra", "list", "", "", "", 0, false},
		{"/server_ping", "ping", "", "", "", 0, false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			got, handled := parseServerCommand(tc.text, "fixture_bot")
			if !handled || got.action != tc.action || got.valid != tc.valid {
				t.Fatalf("got %+v handled=%t", got, handled)
			}
			if tc.valid && (got.id != tc.id || got.name != tc.name || got.host != tc.host || got.maxClients != tc.maxClients) {
				t.Fatalf("parsed %+v", got)
			}
		})
	}
	for _, text := range []string{"/servers@other_bot", "/unknown", "hello", "/servers\x00"} {
		if _, handled := parseServerCommand(text, "fixture_bot"); handled {
			t.Fatalf("unexpected command: %q", text)
		}
	}
}

type serverProofKey struct{}
type serverTestAuthority struct {
	resolved, checked   int
	locale              string
	resolveErr, roleErr error
}

func (a *serverTestAuthority) ResolveTelegramContext(ctx context.Context, _ int64) (context.Context, *accounts.Snapshot, error) {
	a.resolved++
	if a.resolveErr != nil {
		return ctx, nil, a.resolveErr
	}
	return context.WithValue(ctx, serverProofKey{}, true), &accounts.Snapshot{ID: uuid.MustParse("5fa2607d-dcb9-4a53-8cc5-7281a5e766fe"), Locale: a.locale}, nil
}
func (a *serverTestAuthority) RequireInfrastructure(ctx context.Context, _ uuid.UUID) error {
	a.checked++
	if ctx.Value(serverProofKey{}) != true {
		return errors.New("missing actor proof")
	}
	return a.roleErr
}

type serverTestOwner struct {
	keys    []uuid.UUID
	created []vpn.ServerInput
	reads   int
	deletes int
	err     error
}

func (o *serverTestOwner) proof(ctx context.Context) error {
	if ctx.Value(serverProofKey{}) != true {
		return errors.New("missing actor proof")
	}
	return o.err
}
func (o *serverTestOwner) ListManagedServers(ctx context.Context, _ uuid.UUID) ([]vpn.Server, error) {
	o.reads++
	if err := o.proof(ctx); err != nil {
		return nil, err
	}
	return []vpn.Server{{ID: "s1", Name: "<node>", Host: "https://node.example.test", SubscriptionBaseURL: "secret-subscription-url"}}, nil
}
func (o *serverTestOwner) GetManagedServer(ctx context.Context, _ uuid.UUID, id string) (vpn.Server, error) {
	o.reads++
	return vpn.Server{ID: id, Name: "<node>"}, o.proof(ctx)
}
func (o *serverTestOwner) CreateManagedServer(ctx context.Context, _ uuid.UUID, key uuid.UUID, in vpn.ServerInput, channel string) (vpn.Server, bool, error) {
	if channel != "telegram" {
		return vpn.Server{}, false, errors.New("wrong audit channel")
	}
	o.keys, o.created = append(o.keys, key), append(o.created, in)
	return vpn.Server{ID: "new", Name: in.Name, Host: in.Host}, false, o.proof(ctx)
}
func (o *serverTestOwner) PingManagedServer(ctx context.Context, _ uuid.UUID, id string, key uuid.UUID, channel string) (vpn.Server, error) {
	o.keys = append(o.keys, key)
	return vpn.Server{ID: id}, o.proof(ctx)
}
func (o *serverTestOwner) SyncManagedServers(ctx context.Context, _ uuid.UUID, key uuid.UUID, channel string) ([]vpn.Server, error) {
	o.keys = append(o.keys, key)
	return nil, o.proof(ctx)
}
func (o *serverTestOwner) DeleteManagedServer(ctx context.Context, _ uuid.UUID, id string, key uuid.UUID, channel string) (vpn.Server, error) {
	o.deletes++
	o.keys = append(o.keys, key)
	return vpn.Server{ID: id}, o.proof(ctx)
}

func serverRuntime(t *testing.T, authority *serverTestAuthority, owner *serverTestOwner, sent *[]string) *Runtime {
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
		if len(input.Markup.Rows) > 0 && input.Markup.Rows[0][0].URL != "https://cabinet.example.test/admin/servers?lang="+authority.locale {
			t.Fatalf("unprotected cabinet link: %+v", input.Markup)
		}
		return jsonReply(botapi.Message{ID: 70, Chat: botapi.Chat{ID: input.Chat}}), nil
	})}
	r := &Runtime{api: botapi.New(testToken, h), token: testToken}
	if err := r.ConfigureServerManagement(authority, owner, "https://cabinet.example.test"); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestServerManagementPrivateActorAndRole(t *testing.T) {
	for _, kind := range []string{"group", "other-chat", "bot", "forwarded", "via-bot", "sender-chat", "stale-proof", "revoked-role"} {
		t.Run(kind, func(t *testing.T) {
			authority := &serverTestAuthority{locale: "ru"}
			owner := &serverTestOwner{}
			var sent []string
			r := serverRuntime(t, authority, owner, &sent)
			u := clientMessage(701, "/servers")
			switch kind {
			case "group":
				u.Message.Chat.Type = "group"
			case "other-chat":
				u.Message.Chat.ID++
			case "bot":
				u.Message.From.IsBot = true
			case "forwarded":
				u.Message.Raw = json.RawMessage(`{"forward_origin":{"type":"user"}}`)
			case "via-bot":
				u.Message.Raw = json.RawMessage(`{"via_bot":{"id":999}}`)
			case "sender-chat":
				u.Message.Raw = json.RawMessage(`{"sender_chat":{"id":999}}`)
			case "stale-proof":
				authority.resolveErr = &accounts.Error{Status: 403, Code: "INVALID_CREDENTIALS"}
			case "revoked-role":
				authority.roleErr = &accounts.Error{Status: 403, Code: "FORBIDDEN"}
			}
			if err := r.handle(context.Background(), u); err != nil {
				t.Fatal(err)
			}
			if owner.reads != 0 {
				t.Fatal("unauthorized server read")
			}
			if kind != "stale-proof" && kind != "revoked-role" && authority.resolved != 0 {
				t.Fatal("unsafe update reached account lookup")
			}
		})
	}
}

func TestServerManagementCommandsAndReplay(t *testing.T) {
	authority := &serverTestAuthority{locale: "en"}
	owner := &serverTestOwner{}
	var sent []string
	r := serverRuntime(t, authority, owner, &sent)
	for i, text := range []string{"/servers", "/server_add Node | https://node.example.test | 0", "/server_delete old"} {
		u := clientMessage(701, text)
		u.ID += int64(i)
		u.Message.ID += int64(i)
		if err := r.handle(context.Background(), u); err != nil {
			t.Fatal(err)
		}
	}
	if len(owner.created) != 1 || owner.created[0].ID != "" || owner.created[0].MaxClients != 0 || owner.deletes != 0 || len(sent) != 3 {
		t.Fatalf("commands did not respect owner and confirmation: %+v, %d, %d", owner.created, owner.deletes, len(sent))
	}
	if !strings.Contains(sent[0], "&lt;node&gt;") || strings.Contains(sent[0], "secret-subscription-url") || !strings.Contains(sent[2], "confirm") {
		t.Fatalf("unsafe or missing output: %q", sent)
	}
	u := clientMessage(701, "/server_delete old confirm")
	u.ID, u.Message.ID = 14, 10
	if err := r.handle(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if err := r.handle(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	if owner.deletes != 2 || len(owner.keys) != 3 || owner.keys[1] != owner.keys[2] || owner.keys[0] == owner.keys[1] {
		t.Fatalf("unstable idempotency identity: %+v", owner.keys)
	}
	if authority.resolved != 5 || authority.checked != 5 {
		t.Fatalf("proof was not refreshed: %+v", authority)
	}
}

func TestServerManagementUnavailableIsNotAcknowledged(t *testing.T) {
	authority := &serverTestAuthority{locale: "ru"}
	owner := &serverTestOwner{err: &vpn.Error{Status: 503, Code: "SERVICE_UNAVAILABLE"}}
	var sent []string
	r := serverRuntime(t, authority, owner, &sent)
	if err := r.handle(context.Background(), clientMessage(701, "/server_ping s1")); err == nil || len(sent) != 0 {
		t.Fatalf("transient domain failure acknowledged: %v, %q", err, sent)
	}
}

func TestServerManagementOutputBounded(t *testing.T) {
	rows := make([]vpn.Server, 30)
	for i := range rows {
		rows[i] = vpn.Server{ID: strings.Repeat("<", 128), Name: strings.Repeat("<", 100), Host: strings.Repeat("<", 200)}
	}
	if text := serverList(rows, "en"); len(text) > 4096 || strings.Contains(text, strings.Repeat("<", 5)) {
		t.Fatalf("unsafe Telegram output: %d bytes", len(text))
	}
}
