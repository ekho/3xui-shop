package testkit

import (
	"crypto/ed25519"
	"encoding/base64"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// SignedMiniAppData uses owned test keys, never a deployable Telegram token.
// Canonical data comes from Telegram's published third-party validation format.
func SignedMiniAppData(key ed25519.PrivateKey, botID int64, at time.Time, user, start string) string {
	v := url.Values{"auth_date": {strconv.FormatInt(at.Unix(), 10)}, "user": {user}, "hash": {strings.Repeat("0", 64)}}
	if start != "" {
		v.Set("start_param", start)
	}
	keys := []string{"auth_date", "user"}
	if start != "" {
		keys = append(keys, "start_param")
	}
	sort.Strings(keys)
	lines := []string{strconv.FormatInt(botID, 10) + ":WebAppData"}
	for _, k := range keys {
		lines = append(lines, k+"="+v.Get(k))
	}
	v.Set("signature", base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(strings.Join(lines, "\n")))))
	return v.Encode()
}
