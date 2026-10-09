package botapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

func TestSupportFileWire(t *testing.T) {
	ctx := context.Background()
	data := []byte{0, 1, 2, 255}
	calls := 0
	h := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Scheme != "https" || r.URL.Host != "api.telegram.org" || r.URL.RawQuery != "" {
			t.Fatal("file escaped fixed provider")
		}
		body := ""
		switch r.URL.Path {
		case "/botowned-fixture/getFile":
			var p map[string]string
			if json.NewDecoder(r.Body).Decode(&p) != nil || p["file_id"] != "owned-file" {
				t.Fatal("wrong getFile payload")
			}
			body = `{"ok":true,"result":{"file_id":"owned-file","file_path":"documents/owned.bin","file_size":4}}`
		case "/file/botowned-fixture/documents/owned.bin":
			if r.Method != http.MethodGet {
				t.Fatal("file download method")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data))}, nil
		case "/botowned-fixture/sendDocument":
			kind, p, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || kind != "multipart/form-data" {
				t.Fatal("file is not multipart")
			}
			fields := map[string]string{}
			mr := multipart.NewReader(r.Body, p["boundary"])
			for {
				part, err := mr.NextPart()
				if errors.Is(err, io.EOF) {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				b, err := io.ReadAll(part)
				if err != nil {
					t.Fatal(err)
				}
				if part.FormName() == "document" {
					if part.FileName() != `quote".bin` || !bytes.Equal(b, data) {
						t.Fatal("filename or bytes changed")
					}
				} else {
					fields[part.FormName()] = string(b)
				}
			}
			if fields["chat_id"] != "-10074001" || fields["message_thread_id"] != "888" || fields["caption"] != "<b>plain caption</b>" || fields["parse_mode"] != "" {
				t.Fatal("document destination/caption changed")
			}
			body = `{"ok":true,"result":{"message_id":14,"message_thread_id":888,"chat":{"id":-10074001,"type":"supergroup"}}}`
		default:
			t.Fatal("unexpected file request")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	c := New("owned-fixture", h)
	f, err := c.GetFile(ctx, "owned-file")
	if err != nil || f.Path != "documents/owned.bin" {
		t.Fatal("file metadata lost", err)
	}
	b, err := c.DownloadFile(ctx, f.Path)
	if err != nil || !bytes.Equal(b, data) {
		t.Fatal("download bytes lost", err)
	}
	id, err := c.SendSupportDocument(ctx, -10074001, 888, `quote".bin`, b, "<b>plain caption</b>")
	if err != nil || id != 14 || calls != 3 {
		t.Fatal("document ACK not verified", err, calls)
	}
}

func TestSupportFileBounds(t *testing.T) {
	for _, path := range []string{"", "../owned", "documents/../owned", "/owned", "https://evil.test/file", "owned?token=x", "owned#x", "owned%2fother", `owned\other`} {
		t.Run(path, func(t *testing.T) {
			c := New("owned-fixture", &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { t.Fatal("invalid path reached wire"); return nil, nil })})
			if _, err := c.DownloadFile(context.Background(), path); err == nil || err.Error() != "INVALID_INPUT" {
				t.Fatal("unsafe path accepted", err)
			}
		})
	}
	for _, tc := range []struct {
		name       string
		status     int
		body, code string
	}{
		{"actual-cap", 200, strings.Repeat("x", (10<<20)+1), "FILE_TOO_LARGE"},
		{"redirect", 302, "", "INVALID_RESPONSE"},
		{"failure", 503, "private-body", "UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := New("owned-fixture", &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Location": {"https://evil.test/"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})})
			_, err := c.DownloadFile(context.Background(), "documents/owned.bin")
			if err == nil || err.Error() != tc.code || calls != 1 {
				t.Fatal("unsafe/bypassed file limit", err, calls)
			}
		})
	}
}

func TestSupportCanonicalMedia(t *testing.T) {
	var a, b, c Message
	if json.Unmarshal([]byte(`{"message_id":7,"chat":{"id":731,"type":"private"},"from":{"id":731,"first_name":"Old"},"date":1,"future_media":{"object_id":9007199254740993,"nested":{"b":2,"a":1}}}`), &a) != nil ||
		json.Unmarshal([]byte(`{"future_media":{"nested":{"a":1,"b":2},"object_id":9007199254740993},"date":2,"from":{"id":731,"first_name":"New"},"chat":{"type":"private","id":731},"message_id":7}`), &b) != nil ||
		json.Unmarshal([]byte(`{"message_id":7,"chat":{"id":731,"type":"private"},"from":{"id":731},"future_media":{"object_id":9007199254740992,"nested":{"b":2,"a":1}}}`), &c) != nil {
		t.Fatal("message fixture")
	}
	da, err := a.SupportDigest()
	if err != nil {
		t.Fatal(err)
	}
	db, err := b.SupportDigest()
	if err != nil || da != db {
		t.Fatal("digest depends on JSON order/name/date", err)
	}
	dc, err := c.SupportDigest()
	if err != nil || da == dc {
		t.Fatal("unknown media integer rounded or lost", err)
	}
}
