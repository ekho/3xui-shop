package telegram

import (
	"github.com/google/uuid"
	"testing"
)

func TestSupportCommandParsing(t *testing.T) {
	id := uuid.NewString()
	for _, tc := range []struct {
		text, action string
		valid        bool
	}{
		{"/comp 2 owned reason", "comp", true},
		{"/comp 0", "", false}, {"/comp 366", "", false}, {"/comp 1.5", "", false},
		{"/comp@fixture_bot 2", "comp", true}, {"/comp@other_bot 2", "", false},
		{"/reset", "reset", true}, {"/reset_traffic owned reason", "reset", true}, {"/reset owned reason", "reset", true},
		{"/close", "close", true}, {"/reopen", "reopen", true},
		{"/ban owned reason", "ban", true}, {"/unban owned reason", "unban", true},
		{"/approve " + id, "approve", true}, {"/approve", "", false},
		{"/reject " + id + " owned reason", "reject", true},
		{"/reconsider " + id + " owned reason", "reconsider", true},
		{"/pending", "pending", true}, {"/info", "info", true},
		{"/info " + id, "info", true}, {"/info malformed", "", false}, {"/info " + id + " extra", "", false}, {"/open", "reopen", true},
		{"/retry " + id + " owned reason", "retry", true},
		{"/retry " + id, "retry", true},
		{"/bind " + id + " 888 owned reason", "bind", true},
		{"/bind " + id + " 1", "", false}, {"/bind " + id + " 4503599627370496", "", false},
		{"/clsoe", "", false}, {"/comp 2\x00", "", false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			in, topic, ok := parseSupportCommand(tc.text, "fixture_bot")
			if ok != tc.valid || ok && in.Action != tc.action {
				t.Fatal("strict command parsing", ok, in.Action)
			}
			if ok && (in.Action == "bind" || tc.text == "/info "+id) && topic.String() != id {
				t.Fatal("bind target lost")
			}
		})
	}
}
