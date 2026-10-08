package accounts

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

func snapshot(a store.Account) Snapshot {
	source := a.Kind
	if a.OriginalKind.Valid {
		source = a.OriginalKind.String
	}
	return Snapshot{ID: a.ID, VpnID: a.VpnID, EmailKey: textPointer(a.EmailKey), Locale: a.Locale, PasswordSet: a.PasswordHash.Valid,
		VerifiedAt: timePointer(a.VerifiedAt), Restricted: a.Restricted, SubID: a.SubID, PanelKey: a.PanelKey,
		TermsVersion: textPointer(a.TermsVersion), PrivacyVersion: textPointer(a.PrivacyVersion), TelegramID: intPointer(a.TelegramID), LegacyUserID: intPointer(a.LegacyUserID),
		AssignedPanelID: textPointer(a.AssignedPanelID), HadSubscription: a.HadSubscription, CredentialVersion: a.CredentialVersion, VpnBanned: a.VpnBanned,
		Kind: a.Kind, SourceKind: source, TelegramLoginDisabled: a.TelegramLoginDisabled, DisplayName: textPointer(a.DisplayName), CreatedAt: timePointer(a.CreatedAt), RestrictionChangedAt: timePointer(a.RestrictionChangedAt),
		RestrictionOperatorAccountID: a.RestrictionOperatorAccountID, AccessProfile: textPointer(a.AccessProfile), PolicyAcceptedAt: timePointer(a.PolicyAcceptedAt), TelegramStartParam: textPointer(a.TelegramStartParam), RegistrationSourceCode: textPointer(a.RegistrationSourceCode)}
}
func (s *Service) Lookup(ctx context.Context, id uuid.UUID) (Snapshot, error) {
	a, err := store.New(s.pool).AccountByID(ctx, id)
	return accountResult(a, err)
}
func (s *Service) LookupTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Snapshot, error) {
	a, err := store.New(tx).AccountByID(ctx, id)
	return accountResult(a, err)
}

// LegacyIdentitiesTx resolves and optionally locks a bounded import's identity
// set in UUID order, without foreign SQL or one query per imported user.
func (s *Service) LegacyIdentitiesTx(ctx context.Context, tx pgx.Tx, telegramIDs []int64, lock bool) ([]Snapshot, error) {
	if len(telegramIDs) > 100000 {
		return nil, failure(400, "INVALID_INPUT")
	}
	for _, id := range telegramIDs {
		if id <= 0 {
			return nil, failure(400, "INVALID_INPUT")
		}
	}
	q := store.New(tx)
	var rows []store.Account
	var err error
	if lock {
		rows, err = q.LockLegacyIdentities(ctx, telegramIDs)
	} else {
		rows, err = q.LegacyIdentities(ctx, telegramIDs)
	}
	if err != nil {
		return nil, unavailable()
	}
	out := make([]Snapshot, 0, len(rows))
	for _, a := range rows {
		out = append(out, snapshot(a))
	}
	return out, nil
}

// Lock participates in the caller's transaction; the caller commits or rolls back.
func (s *Service) Lock(ctx context.Context, tx pgx.Tx, id uuid.UUID) (Snapshot, error) {
	a, err := store.New(tx).LockAccount(ctx, id)
	return accountResult(a, err)
}
func accountResult(a store.Account, err error) (Snapshot, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, unavailable()
	}
	return snapshot(a), nil
}

func textPointer(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}
func intPointer(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
func timePointer(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}

func (s *Service) AnyWebOperator(ctx context.Context, tx pgx.Tx) (bool, error) {
	q := store.New(s.pool)
	if tx != nil {
		q = store.New(tx)
	}
	result, err := q.AnyWebOperator(ctx)
	if err != nil {
		return false, unavailable()
	}
	return result, nil
}

func (s *Service) OperatorRoleExists(ctx context.Context, tx pgx.Tx, id uuid.UUID) (bool, error) {
	q := store.New(s.pool)
	if tx != nil {
		q = store.New(tx)
	}
	protected, err := q.OperatorRoleExists(ctx, id)
	if err != nil {
		return false, unavailable()
	}
	return protected, nil
}
func (s *Service) VPNBanned(ctx context.Context, id uuid.UUID) (bool, error) {
	result, err := store.New(s.pool).AccountVPNBan(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, unavailable()
	}
	return result, nil
}
func (s *Service) AssignPanel(ctx context.Context, tx pgx.Tx, id uuid.UUID, panelID string) error {
	if store.New(tx).AssignPanel(ctx, store.AssignPanelParams{ID: id, AssignedPanelID: pgtype.Text{String: panelID, Valid: true}}) != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) SetAccessMetadata(ctx context.Context, tx pgx.Tx, id uuid.UUID, profile string, banned bool) error {
	if (banned || profile == "unlimited") && s.cfg.RequireStarsCancellation != nil {
		if err := s.cfg.RequireStarsCancellation(ctx, tx, id, "Account access restricted"); err != nil {
			return unavailable()
		}
	}
	if store.New(tx).SetAccessMetadata(ctx, store.SetAccessMetadataParams{ID: id, AccessProfile: pgtype.Text{String: profile, Valid: true}, VpnBanned: banned}) != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) UnlimitedAccounts(ctx context.Context) ([]uuid.UUID, error) {
	ids, err := store.New(s.pool).UnlimitedAccounts(ctx)
	if err != nil {
		return nil, unavailable()
	}
	return ids, nil
}
func (s *Service) LegacyApproval(ctx context.Context, id uuid.UUID) (LegacyApprovalUser, error) {
	r, err := store.New(s.pool).LegacyApprovalByAccount(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return LegacyApprovalUser{}, ErrNotFound
	}
	if err != nil {
		return LegacyApprovalUser{}, unavailable()
	}
	return LegacyApprovalUser{SourceLegacyUserID: r.SourceLegacyUserID, SourceTgID: r.SourceTgID, Status: r.Status, RequestedAt: timePointer(r.RequestedAt), DecidedAt: timePointer(r.DecidedAt), DecidedBy: intPointer(r.DecidedBy)}, nil
}
func (s *Service) LegacyEvents(ctx context.Context, id uuid.UUID, before *time.Time, sourceID int64) ([]LegacyApprovalSourceEvent, error) {
	var at pgtype.Timestamptz
	if before != nil {
		at = stamp(*before)
	}
	rows, err := store.New(s.pool).LegacyApprovalPage(ctx, store.LegacyApprovalPageParams{AccountID: id, BeforeCreatedAt: at, BeforeSourceID: sourceID})
	if err != nil {
		return nil, unavailable()
	}
	result := make([]LegacyApprovalSourceEvent, 0, len(rows))
	for _, r := range rows {
		result = append(result, LegacyApprovalSourceEvent{SourceID: r.SourceID, TargetTgID: r.TargetTgID, CreatedAt: r.CreatedAt.Time, Action: r.Action, ActorType: textPointer(r.ActorType), ActorID: intPointer(r.ActorID), ActorName: textPointer(r.ActorName), Source: textPointer(r.Source)})
	}
	return result, nil
}
