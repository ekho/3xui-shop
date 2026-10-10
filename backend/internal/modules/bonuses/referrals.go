package bonuses

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/bonuses/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var referralCode = regexp.MustCompile(`^r_[0-9a-f]{32}$`)

// ReadReferrals receives an authenticated actor and the current deployment origin.
func (s *Service) ReadReferrals(ctx context.Context, actor uuid.UUID, origin string) (ReferralsResult, error) {
	empty := ReferralsResult{}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return empty, unavailable()
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return empty, unavailable()
	}
	defer tx.Rollback(ctx)
	a, err := s.authority.Lock(ctx, tx, actor)
	if errors.Is(err, accounts.ErrNotFound) {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		var domain *accounts.Error
		if errors.As(err, &domain) {
			return empty, failure(domain.Status, domain.Code)
		}
		return empty, unavailable()
	}
	if a.Restricted {
		return empty, failure(403, "ACCOUNT_RESTRICTED")
	}
	if !accounts.SourceEligible(a) || a.TermsVersion == nil || a.PrivacyVersion == nil {
		return empty, failure(401, "INVALID_CREDENTIALS")
	}
	q := store.New(tx)
	code, err := q.ReferralCode(ctx, actor)
	if errors.Is(err, pgx.ErrNoRows) {
		code = "r_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if _, err = q.AddReferralLink(ctx, store.AddReferralLinkParams{AccountID: actor, Code: code, CreatedAt: pgtype.Timestamptz{Time: s.now(), Valid: true}}); err != nil {
			return empty, unavailable()
		}
		if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: actor, CreatedAt: s.now(), Action: "referral_link_created"}) != nil {
			return empty, unavailable()
		}
	} else if err != nil {
		return empty, unavailable()
	}
	levels, err := q.ReferralLevels(ctx, actor)
	if err != nil {
		return empty, unavailable()
	}
	out := ReferralsResult{Version: ReferralsVersion, Levels: make([]ReferralLevel, 0, 2)}
	for _, r := range levels {
		out.UnclassifiedRecords = r.UnclassifiedRecords
		out.Levels = append(out.Levels, ReferralLevel{Level: r.Level, Invited: r.Invited, GrantedDays: r.GrantedDays, PendingDays: r.PendingDays, GrantedRewards: r.GrantedRewards, PendingRewards: r.PendingRewards, MoneyRecords: r.MoneyRecords, PendingMoneyRecords: r.PendingMoneyRecords})
	}
	if tx.Commit(ctx) != nil {
		return empty, unavailable()
	}
	u.Path, u.RawQuery = "/register", url.Values{"invite": {code}}.Encode()
	out.WebURL = u.String()
	return out, nil
}

// CaptureRegistrationTx is called only by accounts while inserting a new account.
func (s *Service) CaptureRegistrationTx(ctx context.Context, tx pgx.Tx, id uuid.UUID, channel, code string) error {
	if id == uuid.Nil || (channel != "web" && channel != "telegram") {
		return failure(400, "INVALID_INPUT")
	}
	a, err := s.authority.LookupTx(ctx, tx, id)
	if err != nil {
		return unavailable()
	}
	if a.SourceKind != channel || a.RegistrationSourceCode == nil || *a.RegistrationSourceCode != code {
		return nil
	}
	q := store.New(tx)
	var inviter uuid.UUID
	if referralCode.MatchString(code) {
		inviter, err = q.ReferralCodeOwner(ctx, code)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return unavailable()
		}
	} else {
		if len(code) < 1 || len(code) > 19 || strings.IndexFunc(code, func(c rune) bool { return c < '0' || c > '9' }) >= 0 {
			return nil
		}
		tg, parseErr := strconv.ParseInt(code, 10, 64)
		if parseErr != nil || tg <= 0 {
			return nil
		}
		owner, lookupErr := s.authority.ReferralInviterTx(ctx, tx, tg)
		if errors.Is(lookupErr, accounts.ErrNotFound) {
			return nil
		}
		if lookupErr != nil {
			return unavailable()
		}
		inviter = owner.ID
	}
	if inviter == id {
		return nil
	}
	ref := uuid.New()
	changed, err := q.AddReferral(ctx, store.AddReferralParams{ID: ref, ReferrerAccountID: inviter, ReferredAccountID: id, CreatedAt: pgtype.Timestamptz{Time: s.now(), Valid: true}})
	if err != nil {
		return unavailable()
	}
	if changed == 1 {
		reason := "referral=" + ref.String()
		if auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), AccountID: id, CreatedAt: s.now(), Action: "referral_registered", Reason: &reason}) != nil {
			return unavailable()
		}
	}
	return nil
}
