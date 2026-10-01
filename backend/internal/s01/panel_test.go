package s01

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
func TestPanelConfig(t *testing.T) {
	cfg := Config{CabinetOrigin: "https://cabinet.example.test", TermsVersion: "1", PrivacyVersion: "1", MailKey: make([]byte, 32), CodeKey: make([]byte, 32), TrialEnabled: true, PanelID: "test", Operators: []int64{101}, PanelURL: "https://panel.example.test/path", PanelToken: "fixture-token", SubscriptionBaseURL: "https://subs.example.test/sub/"}
	if cfg.Validate() != nil {
		t.Fatal("valid panel config rejected")
	}
	for _, u := range []string{"http://panel.example.test", "https://username@panel.example.test", "https://panel.example.test?token=bad", "https://panel.example.test#secret", ""} {
		bad := cfg
		bad.PanelURL = u
		if bad.Validate() == nil {
			t.Fatal("unsafe panel URL accepted")
		}
	}
	bad := cfg
	bad.PanelToken = ""
	if bad.Validate() == nil {
		t.Fatal("missing credential accepted")
	}
	bad = cfg
	bad.SubscriptionBaseURL = "http://subs.example.test/"
	if bad.Validate() == nil {
		t.Fatal("unsafe key base URL accepted")
	}
}
