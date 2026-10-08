package accounts

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

func (s *Service) LockOperatorPair(ctx context.Context, tx pgx.Tx, actor, target uuid.UUID) (Snapshot, error) {
	a, err := s.lockOperatorPair(ctx, tx, actor, target)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshot(a), nil
}

var ErrTelegramExists = errors.New("Telegram identity already exists")

func (s *Service) LookupTelegram(ctx context.Context, id int64) (Snapshot, error) {
	a, err := store.New(s.pool).AccountByTelegramID(ctx, pgtype.Int8{Int64: id, Valid: true})
	return accountResult(a, err)
}

// CheckTelegramAvailable reserves the identity in the caller's transaction.
// The trial caller checks trial configuration before CreateTelegram, as before.
func (s *Service) CheckTelegramAvailable(ctx context.Context, tx pgx.Tx, id int64) error {
	if id <= 0 {
		return failure(400, "INVALID_INPUT")
	}
	if err := lockTelegramIdentity(ctx, tx, id); err != nil {
		return err
	}
	if _, err := store.New(tx).AccountByTelegramID(ctx, pgtype.Int8{Int64: id, Valid: true}); err == nil {
		return ErrTelegramExists
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return unavailable()
	}
	if _, err := store.New(tx).TelegramReservationOwner(ctx, id); err == nil {
		return ErrTelegramRetired
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return unavailable()
	}
	return nil
}

func (s *Service) CreateTelegram(ctx context.Context, tx pgx.Tx, in TelegramInput) (Snapshot, error) {
	if in.TelegramID <= 0 || !validOperatorName(in.DisplayName) || (in.Locale != "ru" && in.Locale != "en") {
		return Snapshot{}, failure(400, "INVALID_INPUT")
	}
	if err := s.CheckTelegramAvailable(ctx, tx, in.TelegramID); err != nil {
		return Snapshot{}, err
	}
	sub, err := newOperatorSubID()
	if err != nil {
		return Snapshot{}, err
	}
	if in.RegistrationSourceCode != nil && (len(*in.RegistrationSourceCode) < 1 || !validStartParam(*in.RegistrationSourceCode)) {
		return Snapshot{}, failure(400, "INVALID_INPUT")
	}
	id := uuid.New()
	q := store.New(tx)
	if err = q.AddTelegramAccount(ctx, store.AddTelegramAccountParams{ID: id, DisplayName: pgtype.Text{String: in.DisplayName, Valid: true}, TelegramID: pgtype.Int8{Int64: in.TelegramID, Valid: true}, Locale: in.Locale, VpnID: uuid.New(), SubID: sub, PanelKey: "acct_" + strings.ReplaceAll(id.String(), "-", ""), RegistrationSourceCode: sourceText(in.RegistrationSourceCode)}); err != nil {
		var constraint *pgconn.PgError
		if errors.As(err, &constraint) && constraint.Code == "23505" {
			return Snapshot{}, ErrTelegramExists
		}
		return Snapshot{}, unavailable()
	}
	if in.RegistrationSourceCode != nil && s.cfg.CaptureRegistration != nil {
		if err = s.cfg.CaptureRegistration(ctx, tx, id, "telegram", *in.RegistrationSourceCode); err != nil {
			return Snapshot{}, err
		}
	}
	a, err := q.AccountByID(ctx, id)
	return accountResult(a, err)
}
func (s *Service) lockOperatorPair(ctx context.Context, tx pgx.Tx, actor, target uuid.UUID) (store.Account, error) {
	var empty store.Account
	if actor == uuid.Nil || target == uuid.Nil {
		return empty, failure(400, "INVALID_INPUT")
	}
	first, second := actor, target
	if bytes.Compare(actor[:], target[:]) > 0 {
		first, second = target, actor
	}
	ids := []uuid.UUID{first}
	if second != first {
		ids = append(ids, second)
	}
	q := store.New(tx)
	var targetAccount store.Account
	for _, id := range ids {
		a, err := q.LockAccount(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return empty, failure(404, "INVALID_INPUT")
		}
		if err != nil {
			return empty, unavailable()
		}
		if id == actor {
			if a.Restricted {
				return empty, failure(403, "ACCOUNT_RESTRICTED")
			}
			if a.Kind != "web" || !a.VerifiedAt.Valid || !a.EmailKey.Valid || !a.PasswordHash.Valid {
				return empty, failure(403, "INVALID_CREDENTIALS")
			}
		}
		if id == target {
			targetAccount = a
		}
	}
	if _, err := q.LockOperatorRole(ctx, actor); errors.Is(err, pgx.ErrNoRows) {
		return empty, failure(403, "INVALID_CREDENTIALS")
	} else if err != nil {
		return empty, unavailable()
	}
	return targetAccount, nil
}
func (s *Service) RequireOperator(ctx context.Context, actor uuid.UUID) error {
	if actor == uuid.Nil {
		return failure(401, "INVALID_CREDENTIALS")
	}
	q := store.New(s.pool)
	account, err := q.AccountByID(ctx, actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return failure(401, "INVALID_CREDENTIALS")
	}
	if err != nil {
		return unavailable()
	}
	if account.Restricted {
		return failure(403, "ACCOUNT_RESTRICTED")
	}
	if account.Kind != "web" || !account.VerifiedAt.Valid {
		return failure(403, "INVALID_CREDENTIALS")
	}
	allowed, err := q.OperatorExists(ctx, actor)
	if err != nil {
		return unavailable()
	}
	if !allowed {
		return failure(403, "INVALID_CREDENTIALS")
	}
	return nil
}

func (s *Service) ChangeOperatorRole(ctx context.Context, target uuid.UUID, grant bool) error {
	if target == uuid.Nil {
		return failure(400, "INVALID_INPUT")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	a, err := q.LockAccount(ctx, target)
	if errors.Is(err, pgx.ErrNoRows) {
		return failure(404, "INVALID_INPUT")
	}
	if err != nil {
		return unavailable()
	}
	if grant && (a.Kind != "web" || !a.VerifiedAt.Valid || !a.EmailKey.Valid || !a.PasswordHash.Valid || a.Restricted) {
		return failure(403, "INVALID_CREDENTIALS")
	}
	var changed int64
	if grant {
		changed, err = q.GrantOperator(ctx, store.GrantOperatorParams{AccountID: target, GrantedAt: stamp(s.now())})
	} else {
		if err = s.revokeIssuedRecoveries(ctx, tx, target); err != nil {
			return err
		}
		changed, err = q.RevokeOperator(ctx, target)
	}
	if err != nil {
		return unavailable()
	}
	if changed > 0 {
		action := "operator_revoked"
		if grant {
			action = "operator_granted"
		}
		if err = auditreports.RecordTx(ctx, tx, auditreports.Event{ID: uuid.New(), CreatedAt: s.now(), Action: action, AccountID: target}); err != nil {
			return unavailable()
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) OperatorAllowed(actor int64) bool {
	if actor <= 0 {
		return false
	}
	for _, id := range s.cfg.Operators {
		if id == actor {
			return true
		}
	}
	return false
}

func (s *Service) SearchClients(ctx context.Context, actor uuid.UUID, in OperatorSearchInput) (OperatorSearchResult, error) {
	out := OperatorSearchResult{Clients: []Snapshot{}, Page: in.Page, PerPage: in.PerPage}
	if err := s.RequireOperator(ctx, actor); err != nil {
		return out, err
	}
	if !utf8.ValidString(in.Q) || strings.ContainsRune(in.Q, '\x00') || utf8.RuneCountInString(in.Q) > 256 ||
		in.Page < 1 || in.Page > math.MaxInt32 || in.PerPage < 1 || in.PerPage > 50 {
		return out, failure(400, "INVALID_INPUT")
	}
	q := store.New(s.pool)
	count, err := q.CountOperatorClients(ctx, in.Q)
	if err != nil {
		return out, unavailable()
	}
	rows, err := q.SearchOperatorClients(ctx, store.SearchOperatorClientsParams{Query: in.Q, PageLimit: int32(in.PerPage), PageOffset: int64(in.Page-1) * int64(in.PerPage)})
	if err != nil {
		return out, unavailable()
	}
	out.Total = count
	for _, a := range rows {
		out.Clients = append(out.Clients, snapshot(a))
	}
	return out, nil
}
func validOperatorName(name string) bool {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 128 || strings.TrimSpace(name) == "" || strings.ContainsRune(name, '\x00') {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func newOperatorSubID() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", unavailable()
	}
	const alphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	for i, b := range random {
		random[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(random), nil
}
