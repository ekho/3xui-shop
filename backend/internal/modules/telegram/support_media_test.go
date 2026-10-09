package telegram

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
)

func TestSupportMediaInput(t *testing.T) {
	for _, tc := range []struct {
		name, content, kind, text, file string
		only                            bool
		calls                           int
	}{
		{"photo", `"caption":"caption","photo":[{"file_id":"small","file_size":2},{"file_id":"large","file_size":4},{"file_id":"oversize","file_size":10485761}]`, "photo", "caption", "large", false, 2},
		{"document", `"document":{"file_id":"large","file_size":4,"file_name":"../quote\".bin"}`, "document", "", "large", false, 2},
		{"oversize", `"document":{"file_id":"oversize","file_size":10485761}`, "document", "", "", true, 0},
		{"download-error", `"caption":"unchanged","voice":{"file_id":"unavailable","file_size":4}`, "voice", "unchanged", "unavailable", true, 1},
		{"unknown", `"future_media":{"object_id":9007199254740993}`, "unknown", "", "", true, 0},
		{"contact", `"contact":{"phone_number":"+123","first_name":"Owned"}`, "", "Contact: Owned\nPhone: +123", "", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h := &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Scheme != "https" || r.URL.Host != "api.telegram.org" {
					t.Fatal("provider escaped")
				}
				if strings.HasSuffix(r.URL.Path, "getFile") {
					var p map[string]string
					if json.NewDecoder(r.Body).Decode(&p) != nil || p["file_id"] != tc.file {
						t.Fatal("wrong file selected")
					}
					if tc.name == "download-error" {
						return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("owned error"))}, nil
					}
					return jsonReply(map[string]any{"file_id": tc.file, "file_path": "documents/owned.bin", "file_size": 4}), nil
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("data"))}, nil
			})}
			b := &supportBridge{cfg: SupportConfig{GroupID: -10074001}, botID: 973, api: botapi.New("owned-fixture", h)}
			var m botapi.Message
			if json.Unmarshal([]byte(`{"message_id":7,"from":{"id":731},"chat":{"id":731,"type":"private"},`+tc.content+`}`), &m) != nil {
				t.Fatal("media fixture")
			}
			in, err := b.messageInput(context.Background(), m, 1, true)
			if err != nil || in.MediaKind != tc.kind || in.Text != tc.text || in.TelegramOnly != tc.only || calls != tc.calls {
				t.Fatal("content selection/fallback lost", err, in.MediaKind, in.TelegramOnly, calls)
			}
			if in.Source.ActorID != 731 || in.Source.ChatID != 731 || in.Source.BotID != 973 || in.Source.Digest == [32]byte{} {
				t.Fatal("media source proof lost")
			}
			if tc.calls == 2 && (string(in.Bytes) != "data" || in.Name == "" || strings.ContainsAny(in.Name, "/\\")) {
				t.Fatal("bytes/normalized name lost")
			}
		})
	}
}
