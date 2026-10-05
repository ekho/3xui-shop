package vpn

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
)

// A lost UUID/CA or rebuilt payload would change a real 3.7.0 update.
func TestPanelUpdateNativePayload(t *testing.T) {
	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	updates := make(chan map[string]json.RawMessage, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fixture/panel/api/clients/get/acct_fixture":
			json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": map[string]any{
				"client":     map[string]any{"id": 7, "uuid": id, "email": "acct_fixture", "subId": "fixture", "expiryTime": 1800000000000, "limitIp": 2, "totalGB": 1024, "enable": true, "allowedIPs": "127.0.0.1, ::1", "customField": "preserved"},
				"inboundIds": []int{1}, "usedTraffic": 0,
			}})
		case "/fixture/panel/api/clients/update/acct_fixture":
			var body map[string]json.RawMessage
			if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fixture" || json.NewDecoder(r.Body).Decode(&body) != nil {
				w.WriteHeader(400)
				return
			}
			updates <- body
			json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": nil})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	client := NewPanelClient(Config{PanelURL: server.URL + "/fixture", PanelToken: "fixture", PanelRootCAs: roots})
	defer client.Close()
	view, err := client.GetClient(context.Background(), "acct_fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = client.UpdateAccess(context.Background(), view, AccessTarget{PanelKey: "acct_fixture", VPNID: id, SubID: "fixture", DeviceCount: 2, ExpiryTimeMS: 1800000000000, TrafficLimitBytes: 2048, Banned: true}); err != nil {
		t.Fatal(err)
	}
	body := <-updates
	for field, literal := range map[string]string{
		"id": `"11111111-1111-4111-8111-111111111111"`, "limitIp": "3", "expiryTime": "1800000000000", "totalGB": "2048", "enable": "false", "customField": `"preserved"`,
	} {
		if string(body[field]) != literal {
			t.Errorf("native update changed %s", field)
		}
	}
	var ips []string
	if json.Unmarshal(body["allowedIPs"], &ips) != nil || !reflect.DeepEqual(ips, []string{"127.0.0.1", "::1"}) {
		t.Fatal("ClientRecord CSV not translated to native update array")
	}
}

// Unsafe origins and cancelled work must never reach the panel.
func TestPanelCancelledAndUnsafeOrigin(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": []any{}})
	}))
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	for _, tc := range []struct {
		name, origin string
		cancelled    bool
	}{
		{"cancelled", server.URL, true},
		{"plaintext", "http://" + server.Listener.Addr().String(), false},
		{"userinfo", "https://user@" + server.Listener.Addr().String(), false},
		{"query", server.URL + "?token=fixture", false},
		{"fragment", server.URL + "#fixture", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelled {
				cancel()
			}
			client := NewPanelClient(Config{PanelURL: tc.origin, PanelToken: "fixture", PanelRootCAs: roots})
			defer client.Close()
			if _, err := client.RegularInboundIDs(ctx); !errors.Is(err, ErrPanel) {
				t.Fatal("unsafe or cancelled request did not fail closed")
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("unsafe/cancelled request reached panel")
	}
}

func TestPanelClientRecordNative(t *testing.T) {
	id := uuid.New()
	client := map[string]any{"id": 7, "uuid": id.String(), "email": "acct_fixture", "subId": "fixture", "expiryTime": int64(1800000000000), "limitIp": 2, "totalGB": 1024, "enable": true}
	var language string
	response := map[string]any{"success": true, "obj": map[string]any{"client": client, "inboundIds": []int{1}, "usedTraffic": 0}}
	h := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		language = r.Header.Get("Accept-Language")
		json.NewEncoder(w).Encode(response)
	}))
	defer h.Close()
	roots := x509.NewCertPool()
	roots.AddCert(h.Certificate())
	p := NewPanelClient(Config{PanelURL: h.URL, PanelToken: "fixture", PanelRootCAs: roots})
	defer p.Close()
	v, err := p.GetClient(context.Background(), "acct_fixture")
	if err != nil || v == nil || v.VPNID != id {
		t.Fatal("native numeric id / UUID readback", err)
	}
	delete(client, "uuid")
	if _, err := p.GetClient(context.Background(), "acct_fixture"); err == nil {
		t.Fatal("database id treated as VPN identity")
	}
	for _, msg := range []string{"record not found", " (record not found)", "Obtain (record not found)", "unavailable (record not found)", ""} {
		response = map[string]any{"success": false, "msg": msg, "obj": nil}
		v, err = p.GetClient(context.Background(), "acct_fixture")
		known := msg == "record not found" || msg == " (record not found)" || msg == "Obtain (record not found)"
		if known && (err != nil || v != nil) || !known && err == nil {
			t.Fatal("absence must match an explicit supported response")
		}
	}
	if language != "en-US" {
		t.Fatal("native error locale not fixed")
	}
}

func TestPanelBoundary(t *testing.T) {
	var loginCalls, addCalls int
	h := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		reply := func(v any) { json.NewEncoder(w).Encode(v) }
		switch r.URL.Path {
		case "/fixture/csrf-token":
			http.SetCookie(w, &http.Cookie{Name: "csrf", Value: "fixture", Path: "/fixture", Secure: true})
			reply(map[string]any{"success": true, "obj": "fixture-csrf"})
		case "/fixture/login":
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			_, err := r.Cookie("csrf")
			if err != nil || r.Header.Get("X-CSRF-Token") != "fixture-csrf" || body["username"] != "fixture" || body["password"] != "fixture password" {
				w.WriteHeader(403)
				return
			}
			loginCalls++
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "fixture", Path: "/fixture", Secure: true})
			reply(map[string]any{"success": true, "obj": nil})
		case "/fixture/panel/api/inbounds/list":
			_, err := r.Cookie("session")
			if err != nil || r.Header.Get("X-CSRF-Token") != "fixture-csrf" {
				w.WriteHeader(401)
				return
			}
			reply(map[string]any{"success": true, "obj": []map[string]any{{"id": 1, "enable": true, "tag": "region-regular-tcp"}, {"id": 2, "enable": false, "tag": "regular"}, {"id": 3, "enable": true, "tag": "nonregular"}}})
		case "/fixture/panel/api/clients/add":
			addCalls++
			w.WriteHeader(503)
		default:
			w.WriteHeader(404)
		}
	}))
	defer h.Close()
	roots := x509.NewCertPool()
	roots.AddCert(h.Certificate())
	cfg := Config{PanelURL: h.URL + "/fixture", PanelUsername: "fixture", PanelPassword: "fixture password", PanelRootCAs: roots}
	p := NewPanelClient(cfg)
	ids, err := p.RegularInboundIDs(context.Background())
	if err != nil || len(ids) != 1 || ids[0] != 1 || loginCalls != 1 {
		t.Fatal("session login / enabled regular tags", err)
	}
	_ = p.AddClient(context.Background(), ProvisionTarget{})
	if addCalls != 1 || loginCalls != 1 {
		t.Fatal("transport replayed POST or login")
	}
	cfg.PanelRootCAs = nil
	if _, err = NewPanelClient(cfg).RegularInboundIDs(context.Background()); err == nil {
		t.Fatal("untrusted TLS accepted")
	}
}
