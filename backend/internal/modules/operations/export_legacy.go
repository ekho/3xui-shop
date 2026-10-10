package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"

	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"example.com/cabinet/backend/internal/modules/accounts"
	auditreports "example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/bonuses"
	"example.com/cabinet/backend/internal/modules/campaigns"
	"example.com/cabinet/backend/internal/modules/catalogue"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/support"
	"example.com/cabinet/backend/internal/modules/vpn"
	"github.com/google/uuid"
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

const MaxLegacyExportPacket = 32 << 20

var errLegacyExport = errors.New("EXPORT_FAILED")
var exportSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
var exportStamp = regexp.MustCompile(`^\d{4}-\d\d-\d\d[ T]\d\d:\d\d:\d\d(?:\.\d{1,6})?(?:Z|[+-]\d\d:\d\d)?$`)
var exportPrice = regexp.MustCompile(`^(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)
var exportCode = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
var exportSubID = regexp.MustCompile(`^[0-9a-z]{16}$`)
var legacyTables = map[string]string{
	"servers":          "id name host max_clients location online subscription_url",
	"users":            "id tg_id vpn_id sub_id server_id first_name last_name username language_code created_at is_trial_used approval_status approval_requested_at approval_decided_at approval_decided_by stars_charge_id is_stars_auto_renew stars_expires_at inbound_groups source_invite_name",
	"transactions":     "id tg_id payment_id subscription status created_at updated_at",
	"plans":            "id devices traffic_gb prices inbound_groups hidden",
	"plan_durations":   "id days",
	"referrals":        "id referred_tg_id referrer_tg_id created_at referred_rewarded_at referred_bonus_days",
	"referrer_rewards": "id user_tg_id reward_type reward_level amount created_at rewarded_at payment_id",
	"promocodes":       "id code duration is_activated activated_by created_at",
	"invites":          "id name hash_code clicks created_at is_active",
	"support_tickets":  "id tg_id thread_id status created_at updated_at",
	"audit_log":        "id created_at actor_type actor_id actor_name action target_id source payload",
}

type legacyCell struct {
	kind, text string
	real       float64
}
type legacyRow map[string]legacyCell

func invalidExport()                         { panic(errLegacyExport) }
func (r legacyRow) cell(k string) legacyCell { return r[k] }
func (c legacyCell) integer(min int64, nullable bool) *int64 {
	if c.kind == "null" && nullable {
		return nil
	}
	if c.kind != "integer" {
		invalidExport()
	}
	n, e := strconv.ParseInt(c.text, 10, 64)
	if e != nil || n < min {
		invalidExport()
	}
	return &n
}
func (r legacyRow) num(k string, min int64) int64       { return *r.cell(k).integer(min, false) }
func (r legacyRow) maybeNum(k string, min int64) *int64 { return r.cell(k).integer(min, true) }
func (c legacyCell) textValue(limit int, nullable, nonempty bool) *string {
	if c.kind == "null" && nullable {
		return nil
	}
	if c.kind != "text" || !utf8.ValidString(c.text) || strings.ContainsRune(c.text, 0) || (nonempty && c.text == "") || (limit > 0 && len([]rune(c.text)) > limit) {
		invalidExport()
	}
	v := c.text
	return &v
}
func (r legacyRow) str(k string, limit int) string { return *r.cell(k).textValue(limit, false, true) }
func (r legacyRow) maybeStr(k string, limit int) *string {
	return r.cell(k).textValue(limit, true, false)
}
func (c legacyCell) boolean(nullable bool) *bool {
	if c.kind == "null" && nullable {
		return nil
	}
	if c.kind != "integer" || (c.text != "0" && c.text != "1") {
		invalidExport()
	}
	v := c.text == "1"
	return &v
}
func (r legacyRow) flag(k string) bool       { return *r.cell(k).boolean(false) }
func (r legacyRow) maybeFlag(k string) *bool { return r.cell(k).boolean(true) }
func (c legacyCell) timestamp(nullable bool) *time.Time {
	if c.kind == "null" && nullable {
		return nil
	}
	if c.kind != "text" || !exportStamp.MatchString(c.text) {
		invalidExport()
	}
	raw := strings.ReplaceAll(c.text, " ", "T")
	layouts := []string{"2006-01-02T15:04:05.999999Z07:00", "2006-01-02T15:04:05Z07:00", "2006-01-02T15:04:05.999999", "2006-01-02T15:04:05"}
	for _, layout := range layouts {
		var t time.Time
		var e error
		if strings.ContainsAny(raw, "Z+") || strings.LastIndex(raw, "-") > 9 {
			t, e = time.Parse(layout, raw)
		} else {
			t, e = time.ParseInLocation(layout, raw, time.UTC)
		}
		if e == nil && t.Year() >= 1 && t.Year() <= 9999 {
			t = t.UTC()
			return &t
		}
	}
	invalidExport()
	return nil
}
func (r legacyRow) at(k string) time.Time       { return *r.cell(k).timestamp(false) }
func (r legacyRow) maybeAt(k string) *time.Time { return r.cell(k).timestamp(true) }
func (r legacyRow) choice(k string, allowed ...string) string {
	v := r.str(k, 0)
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	invalidExport()
	return ""
}
func validExportUnicode(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}
func exportJSON(c legacyCell, nullable bool) any {
	if c.kind == "null" && nullable {
		return nil
	}
	if c.kind != "text" || !validExportUnicode([]byte(c.text)) || strings.ContainsRune(c.text, 0) {
		invalidExport()
	}
	d := json.NewDecoder(strings.NewReader(c.text))
	d.UseNumber()
	var parse func() any
	parse = func() any {
		t, e := d.Token()
		if e != nil {
			invalidExport()
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				out := map[string]any{}
				for d.More() {
					key, e := d.Token()
					k, ok := key.(string)
					if e != nil || !ok {
						invalidExport()
					}
					if _, exists := out[k]; exists {
						invalidExport()
					}
					out[k] = parse()
				}
				end, e := d.Token()
				if e != nil || end != json.Delim('}') {
					invalidExport()
				}
				return out
			case '[':
				out := []any{}
				for d.More() {
					out = append(out, parse())
				}
				end, e := d.Token()
				if e != nil || end != json.Delim(']') {
					invalidExport()
				}
				return out
			}
			invalidExport()
		}
		return t
	}
	v := parse()
	if _, e := d.Token(); e != io.EOF {
		invalidExport()
	}
	return v
}
func exportGroups(c legacyCell, nullable, empty bool) *[]string {
	if c.kind == "null" && nullable {
		return nil
	}
	v := exportJSON(c, nullable)
	if v == nil && nullable {
		return nil
	}
	items, ok := v.([]any)
	if !ok || (!empty && len(items) == 0) {
		invalidExport()
	}
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		s, ok := item.(string)
		if !ok || s == "" || len([]rune(s)) > 64 || strings.ContainsRune(s, 0) || seen[s] {
			invalidExport()
		}
		seen[s] = true
		out = append(out, s)
	}
	return &out
}
func exportURL(c legacyCell, limit int, nullable bool) *string {
	v := c.textValue(limit, nullable, true)
	if v == nil {
		return nil
	}
	u, e := url.Parse(*v)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(*v) != *v || strings.ContainsAny(*v, "\\\r\n\t#") {
		invalidExport()
	}
	for _, part := range strings.Split(u.EscapedPath(), "/") {
		if part == "." || part == ".." {
			invalidExport()
		}
	}
	if port := u.Port(); port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			invalidExport()
		}
	}
	return v
}
func exportAmount(c legacyCell, kind string) string {
	var rendered string
	switch c.kind {
	case "integer":
		rendered = c.text
	case "real":
		if math.IsInf(c.real, 0) || math.IsNaN(c.real) || c.real != 0 && math.Abs(c.real) < 1e-18 {
			invalidExport()
		}
		rendered = fmt.Sprintf("%.18f", c.real)
	default:
		invalidExport()
	}
	if strings.HasPrefix(rendered, "-") {
		invalidExport()
	}
	rat, ok := new(big.Rat).SetString(rendered)
	if !ok || rat.Cmp(new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(20), nil))) >= 0 {
		invalidExport()
	}
	parts := strings.Split(rendered, ".")
	if len(parts) > 1 && len(parts[1]) > 18 || len(strings.TrimLeft(parts[0], "0"))+func() int {
		if len(parts) > 1 {
			return len(parts[1])
		}
		return 0
	}() > 38 {
		invalidExport()
	}
	if kind == "DAYS" && !rat.IsInt() {
		invalidExport()
	}
	return rendered
}
func readLegacyTables(tx *sql.Tx) map[string][]legacyRow {
	var names []struct{ name, kind string }
	objects, e := tx.Query(`SELECT name,type FROM sqlite_master WHERE type IN ('table','view')`)
	if e != nil {
		invalidExport()
	}
	for objects.Next() {
		var n struct{ name, kind string }
		if objects.Scan(&n.name, &n.kind) != nil {
			invalidExport()
		}
		if !strings.HasPrefix(n.name, "sqlite_") {
			names = append(names, n)
		}
	}
	if objects.Err() != nil {
		invalidExport()
	}
	objects.Close()
	found := map[string]bool{}
	for _, n := range names {
		if n.kind != "table" || found[n.name] {
			invalidExport()
		}
		found[n.name] = true
		if n.name != "alembic_version" {
			if _, ok := legacyTables[n.name]; !ok {
				invalidExport()
			}
		}
	}
	if len(found) != len(legacyTables) && len(found) != len(legacyTables)+1 {
		invalidExport()
	}
	for table := range legacyTables {
		if !found[table] {
			invalidExport()
		}
	}
	if found["alembic_version"] {
		var col string
		rows, e := tx.Query("PRAGMA table_xinfo(alembic_version)")
		if e != nil {
			invalidExport()
		}
		count := 0
		for rows.Next() {
			var cid, kind, notnull, defaultValue, primary, hidden any
			if rows.Scan(&cid, &col, &kind, &notnull, &defaultValue, &primary, &hidden) != nil || col != "version_num" {
				invalidExport()
			}
			count++
		}
		if rows.Err() != nil || count != 1 {
			invalidExport()
		}
		rows.Close()
	}
	for _, check := range []string{"quick_check", "integrity_check"} {
		var result string
		if tx.QueryRow("PRAGMA "+check).Scan(&result) != nil || result != "ok" {
			invalidExport()
		}
	}
	var fk any
	e = tx.QueryRow("PRAGMA foreign_key_check").Scan(&fk, &fk, &fk, &fk)
	if e != sql.ErrNoRows {
		invalidExport()
	}
	tables := map[string][]legacyRow{}
	for table, spec := range legacyTables {
		columns := strings.Fields(spec)
		expected := map[string]bool{}
		for _, col := range columns {
			expected[col] = true
		}
		rows, e := tx.Query("PRAGMA table_xinfo(" + table + ")")
		if e != nil {
			invalidExport()
		}
		actual := map[string]bool{}
		for rows.Next() {
			var cid, typ, notnull, dflt, pk, hidden any
			var name string
			if rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk, &hidden) != nil {
				invalidExport()
			}
			actual[name] = true
		}
		if rows.Err() != nil || len(actual) != len(expected) {
			invalidExport()
		}
		rows.Close()
		for col := range expected {
			if !actual[col] {
				invalidExport()
			}
		}
		expr := make([]string, 0, len(columns)*2)
		for _, col := range columns {
			expr = append(expr, "typeof("+col+")")
			expr = append(expr, "CASE WHEN typeof("+col+")='text' THEN CAST("+col+" AS BLOB) ELSE "+col+" END")
		}
		data, e := tx.Query("SELECT " + strings.Join(expr, ",") + " FROM " + table + " ORDER BY id")
		if e != nil {
			invalidExport()
		}
		out := []legacyRow{}
		ids := map[int64]bool{}
		for data.Next() {
			vals := make([]any, 2*len(columns))
			ptrs := make([]any, len(vals))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if data.Scan(ptrs...) != nil {
				invalidExport()
			}
			row := legacyRow{}
			for i, col := range columns {
				kind, ok := vals[2*i].(string)
				if !ok {
					invalidExport()
				}
				cell := legacyCell{kind: kind}
				switch value := vals[2*i+1].(type) {
				case nil:
					if kind != "null" {
						invalidExport()
					}
				case int64:
					if kind != "integer" {
						invalidExport()
					}
					cell.text = strconv.FormatInt(value, 10)
				case float64:
					if kind != "real" {
						invalidExport()
					}
					cell.real = value
				case []byte:
					if kind != "text" && kind != "blob" {
						invalidExport()
					}
					cell.text = string(value)
				default:
					invalidExport()
				}
				row[col] = cell
			}
			id := row.num("id", 1)
			if ids[id] {
				invalidExport()
			}
			ids[id] = true
			out = append(out, row)
		}
		if data.Err() != nil {
			invalidExport()
		}
		data.Close()
		tables[table] = out
	}
	return tables
}
func plainJSONDecimal(s string) string {
	parts := strings.Split(strings.ToLower(s), "e")
	if len(parts) == 1 {
		return s
	}
	if len(parts) != 2 {
		invalidExport()
	}
	exponent, e := strconv.Atoi(parts[1])
	if e != nil || exponent > 30 || exponent < -30 {
		invalidExport()
	}
	mantissa := parts[0]
	if strings.HasPrefix(mantissa, "-") {
		invalidExport()
	}
	dot := strings.IndexByte(mantissa, '.')
	if dot < 0 {
		dot = len(mantissa)
	}
	digits := strings.ReplaceAll(mantissa, ".", "")
	point := dot + exponent
	if point <= 0 {
		return "0." + strings.Repeat("0", -point) + digits
	}
	var whole, fraction string
	if point >= len(digits) {
		whole = digits + strings.Repeat("0", point-len(digits))
	} else {
		whole, fraction = digits[:point], digits[point:]
	}
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	if fraction != "" {
		return whole + "." + fraction
	}
	return whole
}
func exportPrices(c legacyCell, durations map[string]bool) map[string]map[string]string {
	object, ok := exportJSON(c, false).(map[string]any)
	if !ok {
		invalidExport()
	}
	result := map[string]map[string]string{}
	for currency, raw := range object {
		if currency != "RUB" && currency != "USD" && currency != "XTR" {
			invalidExport()
		}
		days, ok := raw.(map[string]any)
		if !ok {
			invalidExport()
		}
		result[currency] = map[string]string{}
		scale := 2
		if currency == "XTR" {
			scale = 0
		}
		for day, n := range days {
			if !durations[day] {
				invalidExport()
			}
			number, ok := n.(json.Number)
			if !ok {
				invalidExport()
			}
			rendered := plainJSONDecimal(string(number))
			rat, ok := new(big.Rat).SetString(rendered)
			if !ok || rat.Sign() < 0 {
				invalidExport()
			}
			minor := new(big.Rat).Mul(rat, new(big.Rat).SetInt64(int64(math.Pow10(scale))))
			if !minor.IsInt() || minor.Num().BitLen() > 63 {
				invalidExport()
			}
			if !exportPrice.MatchString(rendered) {
				invalidExport()
			}
			result[currency][day] = rendered
		}
	}
	return result
}
func buildLegacyExport(rows map[string][]legacyRow, source string, botID, groupID int64) LegacyPackage {
	p := LegacyPackage{Version: 1, Source: source, Users: []accounts.LegacyUser{}, Servers: []vpn.LegacyServer{}, Stars: []payments.LegacyStarsUser{}, Catalogue: catalogue.LegacyCataloguePackage{Version: 1, Durations: []int64{}, Plans: []catalogue.LegacyCataloguePlan{}}, Approvals: accounts.LegacyApprovalPackage{Version: 1, Users: []accounts.LegacyApprovalUser{}, ApprovalEvents: []accounts.LegacyApprovalSourceEvent{}}, Payments: payments.LegacyPaymentPackage{Version: 1, Users: []payments.LegacyPaymentUser{}, Transactions: []payments.LegacyPaymentTransaction{}}, Bonuses: bonuses.LegacyPackage{Version: 1, Source: source, Promocodes: []bonuses.LegacyPromocode{}, Referrals: []bonuses.LegacyReferral{}, Rewards: []bonuses.LegacyReward{}}, Campaigns: campaigns.LegacyPackage{Version: 1, Source: source, Campaigns: []campaigns.LegacyCampaign{}, Users: []campaigns.LegacyUser{}}, Support: support.LegacySupportInput{Version: 1, BotID: botID, GroupID: groupID, Tickets: []support.LegacySupportRow{}}, Audit: auditreports.LegacyAuditPackage{Version: 1, Events: []auditreports.LegacyAuditInput{}}, CatalogueSource: LegacyCatalogueSource{Durations: []LegacyDuration{}, Plans: []LegacyPlanSource{}}}
	serverIDs, serverNames := map[int64]bool{}, map[string]bool{}
	for _, r := range rows["servers"] {
		id, name := r.num("id", 1), r.str("name", 255)
		if serverNames[name] || strings.TrimSpace(name) != name {
			invalidExport()
		}
		serverNames[name] = true
		serverIDs[id] = true
		p.Servers = append(p.Servers, vpn.LegacyServer{SourceID: id, Name: name, Host: *exportURL(r.cell("host"), 255, false), MaxClients: r.num("max_clients", 0), Location: r.maybeStr("location", 32), Online: r.cell("online").boolean(false), SubscriptionURL: exportURL(r.cell("subscription_url"), 512, true)})
	}
	tgIDs, vpnIDs, subIDs := map[int64]bool{}, map[string]bool{}, map[string]bool{}
	for _, r := range rows["users"] {
		id, tg := r.num("id", 1), r.num("tg_id", 1)
		vpnID, subID := r.str("vpn_id", 36), r.str("sub_id", 36)
		parsed, e := uuid.Parse(vpnID)
		if e != nil || parsed == uuid.Nil || parsed.String() != vpnID {
			invalidExport()
		}
		parsed, e = uuid.Parse(subID)
		if !exportSubID.MatchString(subID) && (e != nil || parsed.String() != subID) {
			invalidExport()
		}
		if tgIDs[tg] || vpnIDs[vpnID] || subIDs[subID] {
			invalidExport()
		}
		tgIDs[tg], vpnIDs[vpnID], subIDs[subID] = true, true, true
		sid := r.maybeNum("server_id", 1)
		if sid != nil && !serverIDs[*sid] {
			invalidExport()
		}
		inbound := exportGroups(r.cell("inbound_groups"), true, true)
		if inbound != nil && len(*inbound) > 0 {
			allowed := map[string]bool{"regular": true, "euru": true, "unlimited": true, "banned": true}
			normal := 0
			for _, g := range *inbound {
				if !allowed[g] {
					invalidExport()
				}
				if g != "banned" {
					normal++
				}
			}
			if normal != 1 && !(len(*inbound) == 1 && (*inbound)[0] == "banned") {
				invalidExport()
			}
		}
		invite := r.maybeStr("source_invite_name", 100)
		if invite != nil && strings.TrimSpace(*invite) == "" {
			invalidExport()
		}
		trial := r.maybeFlag("is_trial_used")
		p.Users = append(p.Users, accounts.LegacyUser{SourceID: id, TgID: tg, VPNID: vpnID, SubID: subID, ServerID: sid, FirstName: r.str("first_name", 32), LastName: r.maybeStr("last_name", 64), Username: r.maybeStr("username", 32), LanguageCode: r.str("language_code", 5), CreatedAt: r.at("created_at"), IsTrialUsed: trial, InboundGroups: inbound, SourceInviteName: invite})
		p.Approvals.Users = append(p.Approvals.Users, accounts.LegacyApprovalUser{SourceLegacyUserID: id, SourceTgID: tg, Status: r.choice("approval_status", "pending", "approved", "rejected"), RequestedAt: r.maybeAt("approval_requested_at"), DecidedAt: r.maybeAt("approval_decided_at"), DecidedBy: r.maybeNum("approval_decided_by", 1)})
		p.Payments.Users = append(p.Payments.Users, payments.LegacyPaymentUser{SourceLegacyUserID: id, SourceTgID: tg})
		p.Stars = append(p.Stars, payments.LegacyStarsUser{SourceLegacyUserID: id, SourceTgID: tg, StarsChargeID: r.maybeStr("stars_charge_id", 64), IsStarsAutoRenew: r.maybeFlag("is_stars_auto_renew"), StarsExpiresAt: r.maybeNum("stars_expires_at", 0)})
		if invite != nil {
			p.Campaigns.Users = append(p.Campaigns.Users, campaigns.LegacyUser{SourceLegacyUserID: id, SourceTgID: tg, SourceInviteName: *invite, IsTrialUsed: trial})
		}
	}
	days := map[string]bool{}
	for _, r := range rows["plan_durations"] {
		id, day := r.num("id", 1), r.num("days", 1)
		s := strconv.FormatInt(day, 10)
		if days[s] || day > 106751 {
			invalidExport()
		}
		days[s] = true
		p.Catalogue.Durations = append(p.Catalogue.Durations, day)
		p.CatalogueSource.Durations = append(p.CatalogueSource.Durations, LegacyDuration{SourceID: id, Days: day})
	}
	if len(days) > 100 {
		invalidExport()
	}
	devices := map[int64]bool{}
	for _, r := range rows["plans"] {
		groups := exportGroups(r.cell("inbound_groups"), false, false)
		if len(*groups) != 1 {
			invalidExport()
		}
		profile := (*groups)[0]
		if profile != "regular" && profile != "euru" && profile != "unlimited" {
			invalidExport()
		}
		hidden := r.flag("hidden")
		if profile == "unlimited" && !hidden {
			invalidExport()
		}
		prices := exportPrices(r.cell("prices"), days)
		if (!hidden || len(prices) > 0) && (len(days) == 0 || len(prices) != 3 || len(prices["RUB"]) != len(days) || len(prices["USD"]) != len(days) || len(prices["XTR"]) != len(days)) {
			invalidExport()
		}
		id, device, traffic := r.num("id", 1), r.num("devices", 1), r.num("traffic_gb", 0)
		if devices[device] || device > 10000 || traffic > 100000 {
			invalidExport()
		}
		devices[device] = true
		p.Catalogue.Plans = append(p.Catalogue.Plans, catalogue.LegacyCataloguePlan{LegacyPlanID: id, Devices: int(device), TrafficGB: int(traffic), Profile: profile, Hidden: hidden, Prices: prices})
		p.CatalogueSource.Plans = append(p.CatalogueSource.Plans, LegacyPlanSource{SourceID: id, InboundGroups: *groups, PricesJSON: r.str("prices", 0)})
	}
	paymentsSeen := map[string]bool{}
	for _, r := range rows["transactions"] {
		tg := r.num("tg_id", 1)
		if !tgIDs[tg] {
			invalidExport()
		}
		payment := r.str("payment_id", 64)
		if paymentsSeen[payment] {
			invalidExport()
		}
		paymentsSeen[payment] = true
		p.Payments.Transactions = append(p.Payments.Transactions, payments.LegacyPaymentTransaction{SourceID: r.num("id", 1), SourceTgID: tg, PaymentID: payment, Subscription: r.str("subscription", 255), Status: r.choice("status", "pending", "completed", "canceled", "refunded"), CreatedAt: r.at("created_at"), UpdatedAt: r.at("updated_at")})
	}
	promoCodes := map[string]bool{}
	for _, r := range rows["promocodes"] {
		actor, active := r.maybeNum("activated_by", 1), r.cell("is_activated").boolean(false)
		if actor != nil && (!tgIDs[*actor] || !*active) {
			invalidExport()
		}
		duration := r.num("duration", 1)
		if duration > 2147483647 {
			invalidExport()
		}
		code := r.str("code", 32)
		if promoCodes[code] {
			invalidExport()
		}
		promoCodes[code] = true
		p.Bonuses.Promocodes = append(p.Bonuses.Promocodes, bonuses.LegacyPromocode{SourceID: r.num("id", 1), Code: code, Duration: duration, IsActivated: active, ActivatedBy: actor, CreatedAt: r.at("created_at")})
	}
	parents := map[int64]int64{}
	for _, r := range rows["referrals"] {
		child, parent := r.num("referred_tg_id", 1), r.num("referrer_tg_id", 1)
		if !tgIDs[child] || !tgIDs[parent] || child == parent || parents[child] != 0 {
			invalidExport()
		}
		parents[child] = parent
		bonus := r.maybeNum("referred_bonus_days", 0)
		if bonus != nil && *bonus > 2147483647 {
			invalidExport()
		}
		p.Bonuses.Referrals = append(p.Bonuses.Referrals, bonuses.LegacyReferral{SourceID: r.num("id", 1), ReferredTgID: child, ReferrerTgID: parent, CreatedAt: r.at("created_at"), ReferredRewardedAt: r.maybeAt("referred_rewarded_at"), ReferredBonusDays: bonus})
	}
	for child := range parents {
		seen := map[int64]bool{}
		for parents[child] != 0 {
			if seen[child] {
				invalidExport()
			}
			seen[child] = true
			child = parents[child]
		}
	}
	rewardProof := map[string]bool{}
	for _, r := range rows["referrer_rewards"] {
		tg := r.num("user_tg_id", 1)
		if !tgIDs[tg] {
			invalidExport()
		}
		kind := r.choice("reward_type", "DAYS", "MONEY")
		var level *int64
		if s := r.maybeStr("reward_level", 0); s != nil {
			n := int64(0)
			if *s == "FIRST_LEVEL" {
				n = 1
			} else if *s == "SECOND_LEVEL" {
				n = 2
			} else {
				invalidExport()
			}
			level = &n
		}
		payment := r.str("payment_id", 64)
		key := strconv.FormatInt(tg, 10) + "/" + payment
		if rewardProof[key] {
			invalidExport()
		}
		rewardProof[key] = true
		p.Bonuses.Rewards = append(p.Bonuses.Rewards, bonuses.LegacyReward{SourceID: r.num("id", 1), UserTgID: tg, RewardType: kind, RewardLevel: level, Amount: exportAmount(r.cell("amount"), kind), PaymentID: payment, CreatedAt: r.at("created_at"), RewardedAt: r.maybeAt("rewarded_at")})
	}
	campaignNames, campaignCodes := map[string]bool{}, map[string]bool{}
	for _, r := range rows["invites"] {
		name, code := r.str("name", 100), r.str("hash_code", 64)
		numeric := true
		for _, ch := range code {
			if ch < '0' || ch > '9' {
				numeric = false
			}
		}
		if campaignNames[name] || campaignCodes[code] || strings.TrimSpace(name) == "" || !exportCode.MatchString(code) || numeric {
			invalidExport()
		}
		campaignNames[name], campaignCodes[code] = true, true
		p.Campaigns.Campaigns = append(p.Campaigns.Campaigns, campaigns.LegacyCampaign{SourceID: r.num("id", 1), Name: name, HashCode: code, Clicks: r.maybeNum("clicks", 0), IsActive: r.maybeFlag("is_active"), CreatedAt: r.maybeAt("created_at")})
		if r.cell("clicks").kind == "null" || r.cell("is_active").kind == "null" {
			invalidExport()
		}
	}
	ticketUsers, ticketThreads := map[int64]bool{}, map[int64]bool{}
	for _, r := range rows["support_tickets"] {
		tg, thread := r.num("tg_id", 1), r.maybeNum("thread_id", 1)
		created, updated := r.at("created_at"), r.at("updated_at")
		if ticketUsers[tg] || thread != nil && ticketThreads[*thread] || updated.Before(created) {
			invalidExport()
		}
		ticketUsers[tg] = true
		if thread != nil {
			ticketThreads[*thread] = true
		}
		p.Support.Tickets = append(p.Support.Tickets, support.LegacySupportRow{SourceID: r.num("id", 1), TelegramID: tg, ThreadID: thread, Status: r.choice("status", "open", "closed", "banned"), CreatedAt: created.Format("2006-01-02T15:04:05.000000Z"), UpdatedAt: updated.Format("2006-01-02T15:04:05.000000Z")})
	}
	for _, r := range rows["audit_log"] {
		payload := r.maybeStr("payload", 0)
		if payload != nil {
			if len([]byte(*payload)) > 64<<10 {
				invalidExport()
			}
			if _, ok := exportJSON(r.cell("payload"), true).(map[string]any); !ok {
				invalidExport()
			}
		}
		actorType, sourceType := r.choice("actor_type", "admin", "support", "system", "user"), r.choice("source", "main_bot", "support_bot", "job")
		event := auditreports.LegacyAuditInput{SourceID: r.num("id", 1), CreatedAt: r.at("created_at"), Action: r.str("action", 128), TargetTgID: r.maybeNum("target_id", 1), ActorType: &actorType, ActorID: r.maybeNum("actor_id", 1), ActorName: r.maybeStr("actor_name", 256), Source: &sourceType, PayloadJSON: payload}
		p.Audit.Events = append(p.Audit.Events, event)
		if event.Action == "approval.approve" || event.Action == "approval.reject" {
			if event.TargetTgID == nil || !tgIDs[*event.TargetTgID] {
				invalidExport()
			}
			p.Approvals.ApprovalEvents = append(p.Approvals.ApprovalEvents, accounts.LegacyApprovalSourceEvent{SourceID: event.SourceID, TargetTgID: *event.TargetTgID, CreatedAt: event.CreatedAt, Action: event.Action, ActorType: event.ActorType, ActorID: event.ActorID, ActorName: event.ActorName, Source: event.Source})
		}
	}
	if ValidateLegacyPackage(p) != nil {
		invalidExport()
	}
	return p
}

// ExportLegacy reads a private current-schema SQLite snapshot without starting the service.
func ExportLegacy(path, source string, botID, groupID int64) (out LegacyPackage, err error) {
	defer func() {
		if recover() != nil {
			out = LegacyPackage{}
			err = errLegacyExport
		}
		if err != nil {
			err = errLegacyExport
		}
	}()
	if privatePath(path, true, false) != nil || !exportSlug.MatchString(source) || botID < 1 || groupID >= 0 {
		return out, errLegacyExport
	}
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	db, e := sql.Open("sqlite3", uri.String())
	if e != nil {
		return out, errLegacyExport
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	if _, e = db.ExecContext(ctx, "PRAGMA query_only=ON"); e != nil {
		return out, errLegacyExport
	}
	tx, e := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return out, errLegacyExport
	}
	defer tx.Rollback()
	out = buildLegacyExport(readLegacyTables(tx), source, botID, groupID)
	if _, e = MarshalLegacyExport(out); e != nil {
		return LegacyPackage{}, errLegacyExport
	}
	return out, nil
}
func MarshalLegacyExport(p LegacyPackage) ([]byte, error) {
	raw, e := json.Marshal(p)
	if e != nil || len(raw)+1 > MaxLegacyExportPacket {
		return nil, errLegacyExport
	}
	return append(raw, '\n'), nil
}
