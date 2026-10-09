package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAuditMirrorTransport(t *testing.T) {
	calls := 0
	cfg := AuditMirrorConfig{Enabled: true, Token: "123456:Owned_audit_transport_token_xxxxxxxxx", GroupID: -1001234567890}
	body := `{"ok":true,"result":{"message_id":73,"chat":{"id":-1001234567890,"type":"supergroup"}}}`
	client := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Host != "api.telegram.org" || r.URL.Path != "/bot"+cfg.Token+"/sendMessage" {
			t.Fatal("support mirror used another bot/API")
		}
		var in map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&in) != nil || string(in["chat_id"]) != "-1001234567890" || string(in["text"]) != `"action=owned.audit"` || len(in) != 3 || string(in["link_preview_options"]) != `{"is_disabled":true}` || in["message_thread_id"] != nil || in["parse_mode"] != nil || in["reply_markup"] != nil {
			t.Fatal("General/plain metadata wire contract")
		}
		if deadline, ok := r.Context().Deadline(); !ok || time.Until(deadline) > 10*time.Second {
			t.Fatal("unbounded support mirror")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	send, err := NewAuditMirror(cfg, client)
	if err != nil || send == nil {
		t.Fatal("enabled mirror unavailable", err)
	}
	if err = send(context.Background(), "action=owned.audit"); err != nil || calls != 1 {
		t.Fatal("General send", err)
	}
	for _, invalid := range []string{`{"ok":true,"result":{"message_id":0,"chat":{"id":-1001234567890,"type":"supergroup"}}}`, `{"ok":true,"result":{"message_id":73,"chat":{"id":-1009999999999,"type":"supergroup"}}}`, `{"ok":true,"result":{"message_id":73,"chat":{"id":-1001234567890,"type":"group"}}}`, `{"ok":true,"result":{"message_id":73,"message_thread_id":7,"chat":{"id":-1001234567890,"type":"supergroup"}}}`, `{"ok":false,"error_code":429,"description":"private-provider-body","parameters":{"retry_after":1}}`} {
		body = invalid
		before := calls
		if err = send(context.Background(), "action=owned.audit"); err == nil || calls != before+1 || strings.Contains(err.Error(), cfg.Token) || strings.Contains(err.Error(), "private-provider-body") {
			t.Fatal("unsafe ACK/error or repeated attempt", err)
		}
	}
	client.Transport = testTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("private-provider-body " + cfg.Token)
	})
	// Client copies the caller configuration; use a fresh adapter for this boundary.
	send, err = NewAuditMirror(cfg, client)
	before := calls
	if err != nil || send == nil {
		t.Fatal("new owned network transport", err)
	}
	if err = send(context.Background(), "action=owned.audit"); err == nil || err.Error() != "UNAVAILABLE" || calls != before+1 {
		t.Fatal("uncertain wire attempt retried/leaked", err)
	}
	for _, id := range []int64{0, 101, -1 << 52} {
		bad := cfg
		bad.GroupID = id
		if _, err := NewAuditMirror(bad, client); err == nil {
			t.Fatal("invalid General address accepted")
		}
	}
}

func TestAuditMirrorConfig(t *testing.T) {
	t.Setenv("AUDIT_MIRROR_ENABLED", "false")
	t.Setenv("SUPPORT_BOT_TOKEN_FILE", "/absent/private-file")
	t.Setenv("SUPPORT_BOT_TOKEN", "synthetic-private-token")
	if cfg, err := LoadAuditMirrorConfig(); err != nil || cfg.Enabled {
		t.Fatal("disabled mirror touched secret", err)
	}
	if send, err := NewAuditMirror(AuditMirrorConfig{}, nil); err != nil || send != nil {
		t.Fatal("disabled mirror created transport", err)
	}
	path := filepath.Join(t.TempDir(), "support-token")
	if os.WriteFile(path, []byte("123456:Owned_audit_config_token_xxxxxxxxx"), 0600) != nil {
		t.Fatal("owned token fixture")
	}
	t.Setenv("SUPPORT_BOT_TOKEN_FILE", path)
	t.Setenv("SUPPORT_BOT_TOKEN", "")
	t.Setenv("AUDIT_MIRROR_ENABLED", "true")
	t.Setenv("SUPPORT_GROUP_ID", "-1001234567890")
	t.Setenv("TELEGRAM_ENABLED", "false")
	if cfg, err := LoadAuditMirrorConfig(); err != nil || !cfg.Enabled || cfg.GroupID != -1001234567890 || cfg.Token != "123456:Owned_audit_config_token_xxxxxxxxx" {
		t.Fatal("independent support mirror config", err)
	}
	for _, id := range []string{"101", "0", "-4503599627370496", "-01001234567890", "-9223372036854775809", ""} {
		t.Setenv("SUPPORT_GROUP_ID", id)
		if _, err := LoadAuditMirrorConfig(); err == nil {
			t.Fatal("invalid group accepted")
		}
	}
	t.Setenv("SUPPORT_GROUP_ID", "-1001234567890")
	t.Setenv("SUPPORT_BOT_TOKEN", "synthetic-private-token")
	if _, err := LoadAuditMirrorConfig(); err == nil || strings.Contains(err.Error(), "synthetic-private-token") {
		t.Fatal("plaintext secret allowed/leaked")
	}
}
