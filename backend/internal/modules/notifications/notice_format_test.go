package notifications

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNoticeFormatMalformedInput(t *testing.T) {
	for _, raw := range []string{"Hi &copy", "Hi &amp", "Hi &#0", "Hi &#+65;", "<b>Hi</b x>", "Hi <tg-emoji emoji-id=\"123\"></tg-emoji>", "Hello <b", "Hello </b", "Hello <a href=\"https://example.test", "Hello \ufffe", "Hello \uffff", "Hello &#65534;", `<a href="https://example.test/%E9">Help</a>`, `<a href="https://example.test/?q=%E9">Help</a>`, `<a href="https://example.test/%09">Help</a>`} {
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

// Feed the actual producer output into the actual browser renderer, not hand-written mock HTML.
func TestNoticeFormatBrowserCompatibility(t *testing.T) {
	if os.Getenv("RUN_BROWSER_TESTS") != "1" {
		t.Skip("requires RUN_BROWSER_TESTS=1")
	}
	var normalized []string
	for _, raw := range []string{"Hello 🙂 世界", "<strong>Bold &amp; safe</strong>", "<pre><code class=\"language-go\">fmt.Print(&quot;hi&quot;)</code></pre>", `<blockquote expandable>Quote</blockquote>`, `<tg-emoji emoji-id="123">🙂</tg-emoji>`, `<a href="https://example.test/%E2%82%AC?q=%E2%82%AC&amp;x=2#%E2%82%AC">Help</a>`, `<a href="tg://user?id=733">Client</a>`, "Tabs\tand\nlines", "Hello \U0001fffe"} {
		formatted, _, err := normalizeNoticeBody(raw)
		if err != nil {
			t.Fatal("valid browser input rejected", err)
		}
		normalized = append(normalized, formatted)
	}
	input, err := json.Marshal(normalized)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "tests/notice-format-browser.mjs")
	cmd.Dir = filepath.Join("..", "..", "..", "..", "web")
	cmd.Stdin = strings.NewReader(string(input))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("normalized producer/browser mismatch: %s (%v)", out, err)
	}
}
