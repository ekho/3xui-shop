package campaigns

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/payments"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"strings"
	"time"
	"unicode/utf8"
)

type Error struct {
	Status     int
	Code       string
	RetryAfter int
}

func (e *Error) Error() string              { return e.Code }
func failure(status int, code string) error { return &Error{Status: status, Code: code} }
func unavailable() error                    { return failure(503, "SERVICE_UNAVAILABLE") }

type Service struct {
	pool          *pgxpool.Pool
	limiter       *redis.Client
	authority     *accounts.Service
	subscriptions *subscriptions.Service
	payments      *payments.Service
	namespace     string
	now           func() time.Time
}

func New(pool *pgxpool.Pool, limiter *redis.Client, authority *accounts.Service, subscriptionOwner *subscriptions.Service, paymentOwner *payments.Service, namespace string, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{pool: pool, limiter: limiter, authority: authority, subscriptions: subscriptionOwner, payments: paymentOwner, namespace: namespace, now: now}
}
func validText(v string, max int) bool {
	return utf8.ValidString(v) && !strings.ContainsRune(v, 0) && utf8.RuneCountInString(v) <= max && strings.TrimSpace(v) != ""
}
func validCode(v string) bool {
	if len(v) < 1 || len(v) > 64 {
		return false
	}
	for _, c := range v {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

const campaignColumns = "id,name,code,state,revision,web_visits,legacy_clicks,legacy_invite_id::text,created_at,source"

func scanCampaign(row pgx.Row) (Campaign, error) {
	var c Campaign
	err := row.Scan(&c.CampaignID, &c.Name, &c.Code, &c.State, &c.Revision, &c.WebVisits, &c.LegacyClicks, &c.LegacyInviteID, &c.CreatedAt, &c.Source)
	return c, err
}
func writeError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" && pg.ConstraintName == "campaigns_name_key" {
		return failure(409, "CAMPAIGN_NAME_CONFLICT")
	}
	return unavailable()
}

// Actor row lock serializes all campaign writes for that actor, including key replay.
func replay(ctx context.Context, tx pgx.Tx, actor, key uuid.UUID, op string, hash []byte) (Campaign, bool, error) {
	var c Campaign
	var prior, result []byte
	err := tx.QueryRow(ctx, "SELECT body_hash,result FROM idempotency_records WHERE principal=$1 AND operation=$2 AND key=$3", "operator-account:"+actor.String(), op, key).Scan(&prior, &result)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, unavailable()
	}
	if !bytes.Equal(prior, hash) {
		return c, true, failure(409, "IDEMPOTENCY_CONFLICT")
	}
	if json.Unmarshal(result, &c) != nil {
		return c, true, unavailable()
	}
	return c, true, nil
}
func (s *Service) saveResult(ctx context.Context, tx pgx.Tx, actor, key uuid.UUID, op string, hash []byte, c Campaign) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return unavailable()
	}
	_, err = tx.Exec(ctx, "INSERT INTO idempotency_records(principal,operation,key,body_hash,result,created_at) VALUES($1,$2,$3,$4,$5,$6)", "operator-account:"+actor.String(), op, key, hash, raw, s.now())
	if err != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) eventTx(ctx context.Context, tx pgx.Tx, c Campaign, actor *uuid.UUID, action string, reason *string, before *Campaign) error {
	var previous []byte
	var err error
	if before != nil {
		previous, err = json.Marshal(before)
		if err != nil {
			return unavailable()
		}
	}
	after, err := json.Marshal(c)
	if err != nil {
		return unavailable()
	}
	_, err = tx.Exec(ctx, "INSERT INTO campaign_events(id,campaign_id,actor_account_id,action,created_at,reason,before_snapshot,after_snapshot) VALUES($1,$2,$3,$4,$5,$6,$7,$8)", uuid.New(), c.CampaignID, actor, action, s.now(), reason, previous, after)
	if err != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) Create(ctx context.Context, actor, key uuid.UUID, in CreateInput) (Campaign, error) {
	var c Campaign
	if actor == uuid.Nil || key == uuid.Nil || !validText(in.Name, 100) || !validText(in.Reason, 1000) {
		return c, failure(400, "INVALID_INPUT")
	}
	raw, _ := json.Marshal(in)
	hash := sha256.Sum256(raw)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return c, unavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = s.authority.LockOperatorPair(ctx, tx, actor, actor); err != nil {
		return c, err
	}
	if prior, found, e := replay(ctx, tx, actor, key, "createCampaign", hash[:]); found || e != nil {
		return prior, e
	}
	id := uuid.New()
	code := "c_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	now := s.now().UTC().Truncate(time.Microsecond)
	c = Campaign{CampaignID: id, Name: strings.TrimSpace(in.Name), Code: &code, State: "active", Revision: 1, CreatedAt: &now, Source: "operator"}
	_, err = tx.Exec(ctx, "INSERT INTO campaigns(id,name,code,state,created_at,source) VALUES($1,$2,$3,'active',$4,'operator')", id, c.Name, code, now)
	if err != nil {
		return Campaign{}, writeError(err)
	}
	reason := strings.TrimSpace(in.Reason)
	if err = s.eventTx(ctx, tx, c, &actor, "create", &reason, nil); err != nil {
		return Campaign{}, err
	}
	if err = s.saveResult(ctx, tx, actor, key, "createCampaign", hash[:], c); err != nil {
		return Campaign{}, err
	}
	if tx.Commit(ctx) != nil {
		return Campaign{}, unavailable()
	}
	return c, nil
}
func (s *Service) SetState(ctx context.Context, actor, id, key uuid.UUID, in StateInput) (Campaign, error) {
	var c Campaign
	if actor == uuid.Nil || id == uuid.Nil || key == uuid.Nil || in.ExpectedRevision < 1 || !validText(in.Reason, 1000) || (in.State != "active" && in.State != "paused" && in.State != "deleted") {
		return c, failure(400, "INVALID_INPUT")
	}
	raw, _ := json.Marshal(struct {
		ID    uuid.UUID
		Input StateInput
	}{id, in})
	hash := sha256.Sum256(raw)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return c, unavailable()
	}
	defer tx.Rollback(ctx)
	if _, err = s.authority.LockOperatorPair(ctx, tx, actor, actor); err != nil {
		return c, err
	}
	if prior, found, e := replay(ctx, tx, actor, key, "setCampaignState", hash[:]); found || e != nil {
		return prior, e
	}
	c, err = scanCampaign(tx.QueryRow(ctx, "SELECT "+campaignColumns+" FROM campaigns WHERE id=$1 FOR UPDATE", id))
	if errors.Is(err, pgx.ErrNoRows) {
		return c, failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return c, unavailable()
	}
	if c.State == "deleted" || c.Revision != in.ExpectedRevision {
		return Campaign{}, failure(409, "CAMPAIGN_REVISION_CONFLICT")
	}
	if c.State != in.State {
		before := c
		c.State = in.State
		c.Revision++
		if _, err = tx.Exec(ctx, "UPDATE campaigns SET state=$2,revision=$3 WHERE id=$1", id, c.State, c.Revision); err != nil {
			return Campaign{}, unavailable()
		}
		reason := strings.TrimSpace(in.Reason)
		if err = s.eventTx(ctx, tx, c, &actor, "state", &reason, &before); err != nil {
			return Campaign{}, err
		}
	}
	if err = s.saveResult(ctx, tx, actor, key, "setCampaignState", hash[:], c); err != nil {
		return Campaign{}, err
	}
	if tx.Commit(ctx) != nil {
		return Campaign{}, unavailable()
	}
	return c, nil
}
func (s *Service) List(ctx context.Context, actor uuid.UUID, page, perPage int) (ListResult, error) {
	out := ListResult{Campaigns: []Campaign{}, Page: page, PerPage: perPage}
	if page < 1 || page > 2147483647 || perPage < 1 || perPage > 50 {
		return out, failure(400, "INVALID_INPUT")
	}
	if err := s.authority.RequireOperator(ctx, actor); err != nil {
		return out, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, unavailable()
	}
	defer tx.Rollback(ctx)
	if err = tx.QueryRow(ctx, "SELECT count(*) FROM campaigns").Scan(&out.Total); err != nil {
		return out, unavailable()
	}
	rows, err := tx.Query(ctx, "SELECT "+campaignColumns+" FROM campaigns ORDER BY created_at DESC NULLS LAST,id DESC LIMIT $1 OFFSET $2", perPage, int64(page-1)*int64(perPage))
	if err != nil {
		return out, unavailable()
	}
	defer rows.Close()
	for rows.Next() {
		c, e := scanCampaign(rows)
		if e != nil {
			return out, unavailable()
		}
		out.Campaigns = append(out.Campaigns, c)
	}
	if rows.Err() != nil {
		return out, unavailable()
	}
	return out, nil
}

var visitLimit = redis.NewScript(`
local now=tonumber(ARGV[1]);local key=KEYS[1]
redis.call('ZREMRANGEBYSCORE',key,'-inf',now-60000)
if redis.call('ZCARD',key)>=30 then local first=redis.call('ZRANGE',key,0,0,'WITHSCORES');return math.max(1,math.ceil((60000-now+tonumber(first[2]))/1000)) end
redis.call('ZADD',key,now,ARGV[2]);redis.call('PEXPIRE',key,60000);return 0`)

func (s *Service) Visit(ctx context.Context, code, ip string) error {
	if !validCode(code) {
		return failure(400, "INVALID_INPUT")
	}
	limited, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	hash := sha256.Sum256([]byte(ip))
	retry, err := visitLimit.Run(limited, s.limiter, []string{fmt.Sprintf("{%s}:campaign-visit:%x", s.namespace, hash)}, s.now().UnixMilli(), uuid.NewString()).Int()
	if err != nil {
		return unavailable()
	}
	if retry > 0 {
		return &Error{Status: 429, Code: "RATE_LIMITED", RetryAfter: retry}
	}
	_, err = s.pool.Exec(ctx, "UPDATE campaigns SET web_visits=web_visits+1 WHERE code=$1 AND state='active'", code)
	if err != nil {
		return unavailable()
	}
	return nil
}

// CaptureRegistrationTx is invoked only while creating a new account in this same transaction.
func (s *Service) CaptureRegistrationTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, channel, code string) error {
	if code == "" {
		return nil
	}
	if id == uuid.Nil || (channel != "web" && channel != "telegram") {
		return failure(400, "INVALID_INPUT")
	}
	if !validCode(code) {
		return nil
	} // Signed raw source up to512 is kept by accounts even when it is no campaign code.
	var campaign uuid.UUID
	err := tx.QueryRow(ctx, "SELECT id FROM campaigns WHERE code=$1 AND state='active' FOR SHARE", code).Scan(&campaign)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return unavailable()
	}
	_, err = tx.Exec(ctx, "INSERT INTO campaign_acquisitions(account_id,campaign_id,channel,source_code,created_at) VALUES($1,$2,$3,$4,$5)", id, campaign, channel, code, s.now())
	if err != nil {
		return unavailable()
	}
	return nil
}
