package notifications

import (
	"html"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

var noticeEntity = regexp.MustCompile(`&(?:#[^\s<>&;]{1,32}|[A-Za-z][A-Za-z0-9]{0,31});?`)
var noticeEndTag = regexp.MustCompile(`^</[A-Za-z][A-Za-z0-9-]*[ \t\n]*>$`)
var noticeLanguage = regexp.MustCompile(`^language-[A-Za-z0-9_+.-]{1,32}$`)
var noticeDecimal = regexp.MustCompile(`^[1-9][0-9]{0,19}$`)

func noticeControls(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' || r == '\ufffe' || r == '\uffff' {
			return true
		}
	}
	return false
}
func noticeLink(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	decoded, err := url.PathUnescape(s)
	if err != nil || !utf8.ValidString(decoded) || noticeControls(decoded) || strings.ContainsAny(decoded, "\n\t") {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.User != nil {
		return false
	}
	if u.Scheme == "https" {
		return u.Hostname() != "" && u.Opaque == ""
	}
	if u.Scheme != "tg" || u.Host != "user" || u.Path != "" || u.Fragment != "" || !strings.HasPrefix(u.RawQuery, "id=") {
		return false
	}
	n := strings.TrimPrefix(u.RawQuery, "id=")
	v, err := strconv.ParseInt(n, 10, 64)
	return err == nil && v > 0 && v <= 1<<52-1 && strconv.FormatInt(v, 10) == n
}

// Normalize once for preview, Telegram and safe web nodes; plaintext keeps link destinations.
func normalizeNoticeBody(raw string) (string, string, error) {
	invalid := func() (string, string, error) { return "", "", failure(400, "INVALID_INPUT") }
	if !utf8.ValidString(raw) || utf8.RuneCountInString(raw) < 1 || utf8.RuneCountInString(raw) > 4096 || noticeControls(raw) {
		return invalid()
	}
	for _, entity := range noticeEntity.FindAllString(raw, -1) {
		if !strings.HasSuffix(entity, ";") {
			if strings.HasPrefix(entity, "&#") || html.UnescapeString(entity) != entity {
				return invalid()
			}
			continue
		}
		if strings.HasPrefix(entity, "&#") {
			n := entity[2 : len(entity)-1]
			base := 10
			if strings.HasPrefix(n, "x") || strings.HasPrefix(n, "X") {
				base = 16
				n = n[1:]
			}
			value, err := strconv.ParseInt(n, base, 32)
			if strings.HasPrefix(n, "+") || err != nil || value <= 0 || value > utf8.MaxRune || value >= 0xd800 && value <= 0xdfff || noticeControls(string(rune(value))) {
				return invalid()
			}
		} else if entity != "&lt;" && entity != "&gt;" && entity != "&amp;" && entity != "&quot;" {
			return invalid()
		}
	}
	type frame struct {
		source, tag, href string
		plainStart        int
	}
	var stack []frame
	var formatted, plain strings.Builder
	z := xhtml.NewTokenizer(strings.NewReader(raw))
	for {
		kind := z.Next()
		if kind == xhtml.ErrorToken {
			if z.Err() != io.EOF || len(z.Raw()) != 0 || len(stack) != 0 || strings.TrimSpace(plain.String()) == "" {
				return invalid()
			}
			return formatted.String(), plain.String(), nil
		}
		rawToken := string(z.Raw())
		token := z.Token()
		switch kind {
		case xhtml.TextToken:
			if noticeControls(token.Data) {
				return invalid()
			}
			formatted.WriteString(html.EscapeString(token.Data))
			plain.WriteString(token.Data)
		case xhtml.StartTagToken:
			tag := token.Data
			switch tag {
			case "strong":
				tag = "b"
			case "em":
				tag = "i"
			case "ins":
				tag = "u"
			case "strike", "del":
				tag = "s"
			case "span":
				tag = "tg-spoiler"
			case "b", "i", "u", "s", "tg-spoiler", "code", "pre", "blockquote", "a", "tg-emoji":
			default:
				return invalid()
			}
			for _, parent := range stack {
				if parent.tag == "code" || parent.tag == "tg-emoji" || parent.tag == "pre" && tag != "code" || tag == "a" && parent.tag == "a" || tag == "blockquote" && parent.tag == "blockquote" {
					return invalid()
				}
				if (tag == "pre" || tag == "code") && parent.tag != "pre" && parent.tag != "blockquote" {
					return invalid()
				}
			}
			attrs := ""
			href := ""
			seen := map[string]bool{}
			for _, a := range token.Attr {
				if a.Namespace != "" || seen[a.Key] {
					return invalid()
				}
				seen[a.Key] = true
				switch {
				case token.Data == "span" && a.Key == "class" && a.Val == "tg-spoiler":
				case tag == "a" && a.Key == "href" && noticeLink(a.Val):
					href = a.Val
					attrs = ` href="` + html.EscapeString(href) + `"`
				case tag == "code" && a.Key == "class" && noticeLanguage.MatchString(a.Val) && len(stack) > 0 && stack[len(stack)-1].tag == "pre":
					attrs = ` class="` + a.Val + `"`
				case tag == "blockquote" && a.Key == "expandable" && a.Val == "":
					attrs = " expandable"
				case tag == "tg-emoji" && a.Key == "emoji-id" && noticeDecimal.MatchString(a.Val):
					if _, err := strconv.ParseUint(a.Val, 10, 64); err != nil {
						return invalid()
					}
				default:
					return invalid()
				}
			}
			if token.Data == "span" && !seen["class"] || tag == "a" && href == "" || tag == "tg-emoji" && !seen["emoji-id"] {
				return invalid()
			}
			stack = append(stack, frame{source: token.Data, tag: tag, href: href, plainStart: plain.Len()})
			if tag != "tg-emoji" {
				formatted.WriteString("<" + tag + attrs + ">")
			}
		case xhtml.EndTagToken:
			if !noticeEndTag.MatchString(rawToken) || len(stack) == 0 || stack[len(stack)-1].source != token.Data || len(token.Attr) > 0 {
				return invalid()
			}
			last := stack[len(stack)-1]
			if last.tag == "tg-emoji" && strings.TrimSpace(plain.String()[last.plainStart:]) == "" {
				return invalid()
			}
			stack = stack[:len(stack)-1]
			if last.tag != "tg-emoji" {
				formatted.WriteString("</" + last.tag + ">")
			}
			if last.href != "" {
				plain.WriteString(" (" + last.href + ")")
			}
		default:
			return invalid()
		}
	}
}
