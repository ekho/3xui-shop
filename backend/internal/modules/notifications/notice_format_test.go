package notifications

import (
	"strings"
	"testing"
)

func TestNoticeFormatMalformedInput(t *testing.T) {
	for _, raw := range []string{"Hi &copy", "Hi &amp", "Hi &#0", "Hi &#+65;", "<b>Hi</b x>", "Hi <tg-emoji emoji-id=\"123\"></tg-emoji>"} {
		t.Run(raw, func(t *testing.T) {
			if _, _, err := normalizeNoticeBody(raw); err == nil {
				t.Fatal("malformed entity/tag/fallback accepted")
			}
		})
	}
	if formatted, plain, err := normalizeNoticeBody(strings.Repeat("ж", 4096)); err != nil || formatted != strings.Repeat("ж", 4096) || plain != formatted {
		t.Fatal("Unicode rune limit treated as byte limit", err)
	}
}
