package botapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSupportForumWire(t *testing.T) {
	h := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Scheme != "https" || r.URL.Host != "api.telegram.org" {
			t.Fatal("wrong provider host")
		}
		var p map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			t.Fatal(err)
		}
		var body string
		switch {
		case strings.HasSuffix(r.URL.Path, "getChat"):
			if string(p["chat_id"]) != "-10074001" {
				t.Fatal("wrong forum lookup")
			}
			body = `{"id":-10074001,"type":"supergroup","is_forum":true}`
		case strings.HasSuffix(r.URL.Path, "getChatMember"):
			if string(p["chat_id"]) != "-10074001" || string(p["user_id"]) != "973" {
				t.Fatal("wrong administrator lookup")
			}
			body = `{"user":{"id":973,"is_bot":true},"status":"administrator","can_manage_topics":true}`
		case strings.HasSuffix(r.URL.Path, "createForumTopic"):
			if string(p["chat_id"]) != "-10074001" || string(p["name"]) != `"owned topic"` {
				t.Fatal("wrong topic creation")
			}
			body = `{"message_thread_id":888,"name":"owned topic","icon_color":0}`
		case strings.HasSuffix(r.URL.Path, "sendMessage"):
			var text string
			if json.Unmarshal(p["text"], &text) != nil || string(p["chat_id"]) != "-10074001" || string(p["message_thread_id"]) != "888" || text != "<b>plain</b>" || p["parse_mode"] != nil {
				t.Fatal("wrong plain topic message")
			}
			body = `{"message_id":11,"message_thread_id":888,"chat":{"id":-10074001,"type":"supergroup"}}`
		case strings.HasSuffix(r.URL.Path, "copyMessage"):
			if string(p["chat_id"]) != "731" || p["message_thread_id"] != nil || string(p["from_chat_id"]) != "-10074001" || string(p["message_id"]) != "12" || p["caption"] != nil {
				t.Fatal("copy recipient/content changed")
			}
			body = `{"message_id":13}`
		default:
			t.Fatal("unexpected forum method")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":` + body + `}`)), Header: http.Header{}}, nil
	})}
	c := New("owned-fixture", h)
	ctx := context.Background()
	chat, err := c.GetChat(ctx, -10074001)
	if err != nil || !chat.IsForum {
		t.Fatal("forum proof lost", err)
	}
	member, err := c.GetChatMember(ctx, -10074001, 973)
	if err != nil || member.User.ID != 973 || !member.CanManageTopics {
		t.Fatal("administrator proof lost", err)
	}
	topic, err := c.CreateForumTopic(ctx, -10074001, "owned topic")
	if err != nil || topic != 888 {
		t.Fatal("topic proof lost", err)
	}
	msg, err := c.SendSupportText(ctx, -10074001, 888, "<b>plain</b>")
	if err != nil || msg != 11 {
		t.Fatal("text proof lost", err)
	}
	copy, err := c.CopySupportMessage(ctx, 731, 0, -10074001, 12)
	if err != nil || copy != 13 {
		t.Fatal("copy proof lost", err)
	}
	if _, err = c.SendMessage(ctx, -10074001, "still private", nil); err == nil {
		t.Fatal("private API guard weakened")
	}
}

func TestSupportForumControlWire(t *testing.T) {
	h := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		var p map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&p) != nil || string(p["chat_id"]) != "-10074001" {
			t.Fatal("control group escaped")
		}
		result := `true`
		if strings.HasSuffix(r.URL.Path, "sendMessage") {
			if p["message_thread_id"] != nil || p["parse_mode"] != nil || p["reply_markup"] == nil {
				t.Fatal("General prompt destination/markup")
			}
			result = `{"message_id":91,"chat":{"id":-10074001,"type":"supergroup"}}`
		} else if string(p["message_thread_id"]) != "888" || !strings.HasSuffix(r.URL.Path, "closeForumTopic") && !strings.HasSuffix(r.URL.Path, "reopenForumTopic") {
			t.Fatal("topic action boundary")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":` + result + `}`))}, nil
	})}
	c := New("owned-fixture", h)
	for _, closed := range []bool{true, false} {
		ok, err := c.SetForumTopicClosed(context.Background(), -10074001, 888, closed)
		if err != nil || !ok {
			t.Fatal("bool topic ACK lost", err)
		}
	}
	id, err := c.SendSupportCard(context.Background(), -10074001, 0, "Confirm", &InlineKeyboard{Rows: [][]Button{{{Text: "Confirm", Data: "sp1:c:owned"}}}})
	if err != nil || id != 91 {
		t.Fatal("General prompt ACK", err)
	}
}

func TestSupportForumErrorProof(t *testing.T) {
	for _, tc := range []struct{ description, code string }{{"Bad Request: message thread not found", "THREAD_NOT_FOUND"}, {"Bad Request: something else", "BAD_REQUEST"}} {
		t.Run(tc.code, func(t *testing.T) {
			h := &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"ok":false,"error_code":400,"description":"` + tc.description + `"}`)), Header: http.Header{}}, nil
			})}
			_, err := New("owned-fixture", h).SendSupportText(context.Background(), -10074001, 888, "text")
			if err == nil || err.Error() != tc.code {
				t.Fatal("error was not bounded proof", err)
			}
		})
	}
}
