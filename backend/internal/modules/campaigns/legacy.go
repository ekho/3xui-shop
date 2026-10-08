package campaigns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"time"

	"example.com/cabinet/backend/internal/modules/accounts"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type LegacyPackage struct {
	Version   int              `json:"version"`
	Source    string           `json:"source"`
	Campaigns []LegacyCampaign `json:"campaigns"`
	Users     []LegacyUser     `json:"users"`
}
type LegacyCampaign struct {
	SourceID  int64      `json:"source_id"`
	Name      string     `json:"name"`
	HashCode  string     `json:"hash_code"`
	Clicks    *int64     `json:"clicks"`
	IsActive  *bool      `json:"is_active"`
	CreatedAt *time.Time `json:"created_at"`
}
type LegacyUser struct {
	SourceLegacyUserID int64  `json:"source_legacy_user_id"`
	SourceTgID         int64  `json:"source_tg_id"`
	SourceInviteName   string `json:"source_invite_name"`
	IsTrialUsed        *bool  `json:"is_trial_used"`
}
type ImportResult struct {
	Campaigns         int `json:"campaigns"`
	Users             int `json:"users"`
	InsertedCampaigns int `json:"inserted_campaigns"`
	InsertedUsers     int `json:"inserted_users"`
}

// ValidateLegacyPackage also runs before the CLI opens any database connection.
func ValidateLegacyPackage(p LegacyPackage) error {
	bad := func() error { return failure(400, "IMPORT_INVALID_PACKAGE") }
	if p.Version != 1 || !validCode(p.Source) || p.Campaigns == nil || p.Users == nil || len(p.Campaigns) > 10000 || len(p.Users) > 100000 {
		return bad()
	}
	names, codes := map[string]bool{}, map[string]bool{}
	ids := map[int64]bool{}
	for _, c := range p.Campaigns {
		numeric := true
		for _, ch := range c.HashCode {
			if ch < '0' || ch > '9' {
				numeric = false
			}
		}
		if c.SourceID <= 0 || ids[c.SourceID] || !validText(c.Name, 100) || names[c.Name] || !validCode(c.HashCode) || numeric || codes[c.HashCode] || c.Clicks == nil || *c.Clicks < 0 || c.IsActive == nil {
			return bad()
		}
		if c.CreatedAt != nil && (c.CreatedAt.IsZero() || c.CreatedAt.Year() < 1 || c.CreatedAt.Year() > 9999 || c.CreatedAt.Nanosecond()%1000 != 0) {
			return bad()
		}
		ids[c.SourceID], names[c.Name], codes[c.HashCode] = true, true, true
	}
	legacy, tg := map[int64]bool{}, map[int64]bool{}
	for _, u := range p.Users {
		if u.SourceLegacyUserID <= 0 || u.SourceTgID <= 0 || legacy[u.SourceLegacyUserID] || tg[u.SourceTgID] || !validText(u.SourceInviteName, 100) || u.IsTrialUsed == nil {
			return bad()
		}
		legacy[u.SourceLegacyUserID], tg[u.SourceTgID], names[u.SourceInviteName] = true, true, true
	}
	if len(names) > 10000 {
		return bad()
	}
	return nil
}

type importCampaign struct {
	campaign Campaign
	sourceID *int64
	payload  []byte
	fresh    bool
}
type importMember struct {
	account, campaign uuid.UUID
	source            LegacyUser
	payload           []byte
}

// Compare stored raw JSON without converting signed-int64 identifiers to floats.
func sameSourceJSON(a, b []byte) bool {
	decode := func(raw []byte) (any, error) {
		var value any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		err := d.Decode(&value)
		return value, err
	}
	x, e := decode(a)
	y, f := decode(b)
	return e == nil && f == nil && reflect.DeepEqual(x, y)
}
func importWriteError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return failure(409, "IMPORT_SOURCE_CONFLICT")
	}
	return unavailable()
}

// ImportLegacy transfers metadata only; apply=false is a real read-only Tx.
func (s *Service) ImportLegacy(ctx context.Context, p LegacyPackage, apply bool) (ImportResult, error) {
	out := ImportResult{Users: len(p.Users)}
	if err := ValidateLegacyPackage(p); err != nil {
		return out, err
	}
	opts := pgx.TxOptions{}
	if !apply {
		opts = pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	}
	tx, err := s.pool.BeginTx(ctx, opts)
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	if apply {
		// ponytail: rare CLI imports serialize; source-specific locks if import throughput matters.
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('campaigns:legacy-import',0))`); err != nil {
			return out, unavailable()
		}
	}
	tgIDs := make([]int64, 0, len(p.Users))
	for _, u := range p.Users {
		tgIDs = append(tgIDs, u.SourceTgID)
	}
	located, err := s.authority.LegacyIdentitiesTx(ctx, tx, tgIDs, apply)
	if err != nil {
		return out, unavailable()
	}
	users := map[int64]accounts.Snapshot{}
	accountIDs := make([]uuid.UUID, 0, len(located))
	for _, a := range located {
		if a.TelegramID == nil {
			return out, failure(409, "IMPORT_IDENTITY_CONFLICT")
		}
		users[*a.TelegramID] = a
		accountIDs = append(accountIDs, a.ID)
	}
	for _, u := range p.Users {
		a, ok := users[u.SourceTgID]
		if !ok || a.LegacyUserID == nil || *a.LegacyUserID != u.SourceLegacyUserID {
			return out, failure(409, "IMPORT_IDENTITY_CONFLICT")
		}
		if a.RegistrationSourceCode != nil {
			return out, failure(409, "IMPORT_SOURCE_CONFLICT")
		}
	}
	desired := map[string]*importCampaign{}
	names, codes, sourceIDs := []string{}, []string{}, []int64{}
	for _, c := range p.Campaigns {
		raw, e := json.Marshal(c)
		if e != nil {
			return out, failure(400, "IMPORT_INVALID_PACKAGE")
		}
		code, sourceID := c.HashCode, c.SourceID
		state := "paused"
		if *c.IsActive {
			state = "active"
		}
		desired[c.Name] = &importCampaign{campaign: Campaign{CampaignID: uuid.New(), Name: c.Name, Code: &code, State: state, Revision: 1, LegacyClicks: c.Clicks, LegacyInviteID: func() *string { v := strconv.FormatInt(c.SourceID, 10); return &v }(), CreatedAt: c.CreatedAt, Source: "legacy_import"}, sourceID: &sourceID, payload: raw, fresh: true}
		codes = append(codes, c.HashCode)
		sourceIDs = append(sourceIDs, c.SourceID)
	}
	for _, u := range p.Users {
		if desired[u.SourceInviteName] == nil {
			raw, _ := json.Marshal(map[string]string{"source_invite_name": u.SourceInviteName})
			desired[u.SourceInviteName] = &importCampaign{campaign: Campaign{CampaignID: uuid.New(), Name: u.SourceInviteName, State: "deleted", Revision: 1, Source: "legacy_orphan"}, payload: raw, fresh: true}
		}
	}
	for name := range desired {
		names = append(names, name)
	}
	sort.Strings(names)
	out.Campaigns = len(names)
	query := "SELECT " + campaignColumns + ",legacy_source,legacy_payload FROM campaigns WHERE name=ANY($1::text[]) OR code=ANY($2::text[]) OR (legacy_source=$3 AND legacy_invite_id=ANY($4::bigint[])) ORDER BY id"
	if apply {
		query += " FOR UPDATE"
	}
	rows, err := tx.Query(ctx, query, names, codes, p.Source, sourceIDs)
	if err != nil {
		return out, unavailable()
	}
	for rows.Next() {
		var c Campaign
		var source *string
		var raw []byte
		if rows.Scan(&c.CampaignID, &c.Name, &c.Code, &c.State, &c.Revision, &c.WebVisits, &c.LegacyClicks, &c.LegacyInviteID, &c.CreatedAt, &c.Source, &source, &raw) != nil {
			rows.Close()
			return out, unavailable()
		}
		want := desired[c.Name]
		if want == nil || !want.fresh || source == nil || *source != p.Source || c.Source != want.campaign.Source || !sameSourceJSON(raw, want.payload) {
			rows.Close()
			return out, failure(409, "IMPORT_SOURCE_CONFLICT")
		}
		want.campaign, want.fresh = c, false
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, unavailable()
	}
	wantedUsers := map[int64]LegacyUser{}
	selectedAccounts := map[uuid.UUID]bool{}
	for _, u := range p.Users {
		wantedUsers[u.SourceLegacyUserID] = u
		selectedAccounts[users[u.SourceTgID].ID] = true
	}
	retained := map[uuid.UUID]bool{}
	rows, err = tx.Query(ctx, `SELECT account_id,campaign_id,channel,legacy_source,legacy_payload FROM campaign_acquisitions WHERE account_id=ANY($1::uuid[]) OR (channel='legacy_name' AND legacy_source=$2)`, accountIDs, p.Source)
	if err != nil {
		return out, unavailable()
	}
	for rows.Next() {
		var id, campaign uuid.UUID
		var channel string
		var source *string
		var raw []byte
		if rows.Scan(&id, &campaign, &channel, &source, &raw) != nil {
			rows.Close()
			return out, unavailable()
		}
		if channel != "legacy_name" || source == nil || *source != p.Source {
			rows.Close()
			return out, failure(409, "IMPORT_SOURCE_CONFLICT")
		}
		var previous LegacyUser
		if json.Unmarshal(raw, &previous) != nil {
			rows.Close()
			return out, unavailable()
		}
		want, ok := wantedUsers[previous.SourceLegacyUserID]
		if !ok {
			if _, sameTelegram := users[previous.SourceTgID]; sameTelegram || selectedAccounts[id] {
				rows.Close()
				return out, failure(409, "IMPORT_SOURCE_CONFLICT")
			}
			continue
		}
		packed, _ := json.Marshal(want)
		if users[want.SourceTgID].ID != id || desired[want.SourceInviteName].campaign.CampaignID != campaign || !sameSourceJSON(raw, packed) {
			rows.Close()
			return out, failure(409, "IMPORT_SOURCE_CONFLICT")
		}
		retained[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, unavailable()
	}
	newCampaigns := []*importCampaign{}
	newMembers := []importMember{}
	changed := map[string]int{}
	for _, name := range names {
		if desired[name].fresh {
			newCampaigns = append(newCampaigns, desired[name])
			changed[name] = 0
		}
	}
	for _, u := range p.Users {
		id := users[u.SourceTgID].ID
		if retained[id] {
			continue
		}
		raw, _ := json.Marshal(u)
		newMembers = append(newMembers, importMember{account: id, campaign: desired[u.SourceInviteName].campaign.CampaignID, source: u, payload: raw})
		changed[u.SourceInviteName]++
	}
	out.InsertedCampaigns, out.InsertedUsers = len(newCampaigns), len(newMembers)
	if !apply {
		return out, nil
	}
	if _, err = tx.CopyFrom(ctx, pgx.Identifier{"campaigns"}, []string{"id", "name", "code", "state", "revision", "web_visits", "source", "created_at", "legacy_source", "legacy_invite_id", "legacy_clicks", "legacy_payload"}, pgx.CopyFromSlice(len(newCampaigns), func(i int) ([]any, error) {
		v := newCampaigns[i]
		c := v.campaign
		return []any{c.CampaignID, c.Name, c.Code, c.State, c.Revision, c.WebVisits, c.Source, c.CreatedAt, p.Source, v.sourceID, c.LegacyClicks, v.payload}, nil
	})); err != nil {
		return out, importWriteError(err)
	}
	if _, err = tx.CopyFrom(ctx, pgx.Identifier{"campaign_acquisitions"}, []string{"account_id", "campaign_id", "channel", "legacy_source", "legacy_trial_used", "legacy_payload"}, pgx.CopyFromSlice(len(newMembers), func(i int) ([]any, error) {
		v := newMembers[i]
		return []any{v.account, v.campaign, "legacy_name", p.Source, v.source.IsTrialUsed, v.payload}, nil
	})); err != nil {
		return out, importWriteError(err)
	}
	eventNames := []string{}
	for name := range changed {
		eventNames = append(eventNames, name)
	}
	sort.Strings(eventNames)
	now := s.now()
	if _, err = tx.CopyFrom(ctx, pgx.Identifier{"campaign_events"}, []string{"id", "campaign_id", "action", "created_at", "reason", "before_snapshot", "after_snapshot"}, pgx.CopyFromSlice(len(eventNames), func(i int) ([]any, error) {
		name := eventNames[i]
		v := desired[name]
		after, e := json.Marshal(v.campaign)
		var before []byte
		if !v.fresh {
			before = after
		}
		reason := strconv.Itoa(changed[name]) + " legacy name references imported"
		return []any{uuid.New(), v.campaign.CampaignID, "legacy_import", now, reason, before, after}, e
	})); err != nil {
		return out, importWriteError(err)
	}
	if tx.Commit(ctx) != nil {
		return out, unavailable()
	}
	return out, nil
}
