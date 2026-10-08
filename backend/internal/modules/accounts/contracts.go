package accounts

import (
	"github.com/google/uuid"
	"time"
)

// Snapshot contains account identity and access metadata, never credentials.
type Snapshot struct {
	ID, VpnID                       uuid.UUID
	EmailKey                        *string
	Locale                          string
	PasswordSet                     bool
	VerifiedAt                      *time.Time
	Restricted                      bool
	SubID, PanelKey                 string
	TermsVersion, PrivacyVersion    *string
	TelegramID, LegacyUserID        *int64
	AssignedPanelID                 *string
	HadSubscription                 bool
	CredentialVersion               int64
	VpnBanned                       bool
	Kind                            string
	SourceKind                      string
	TelegramLoginDisabled           bool
	DisplayName                     *string
	CreatedAt, RestrictionChangedAt *time.Time
	PolicyAcceptedAt                *time.Time
	TelegramStartParam              *string
	RegistrationSourceCode          *string
	RestrictionOperatorAccountID    *uuid.UUID
	AccessProfile                   *string
}

type RegisterInput struct {
	AcceptedPrivacyVersion, AcceptedTermsVersion, Email, Locale string
	SourceCode                                                  *string
}
type RegistrationAccepted struct {
	ChallengeId uuid.UUID
	ResendAfter int64
}
type ResendInput struct{ Email string }
type ResendAccepted struct{ ResendAfter int64 }
type VerifyInput struct {
	ChallengeId *uuid.UUID
	Code        *string
	NewPassword string
	Token       *string
}
type VerifyResult struct{ Verified bool }
type LoginInput struct{ Email, Password string }
type Authentication struct {
	Account   Snapshot
	CsrfToken string
}
type LoginResult = Authentication
type PasswordResetInput struct{ Email, Locale string }
type PasswordResetAccepted struct {
	ChallengeId uuid.UUID
	ResendAfter int64
}
type PasswordResetCompleteInput struct {
	ChallengeId *uuid.UUID
	Code        *string
	NewPassword string
	Token       *string
}
type PasswordChangeInput struct{ CurrentPassword, NewPassword string }
type CurrentPasswordInput struct{ CurrentPassword string }
type SessionContext struct{ CsrfToken string }
type AccountSecurity struct {
	Email              string
	HasOtherSessions   bool
	PendingEmailChange *PendingEmailChange
}
type PendingEmailChange struct {
	CurrentEmailConfirmed bool
	ExpiresAt             time.Time
	NewEmail              string
	NewEmailConfirmed     bool
}
type EmailChangeInput struct{ CurrentPassword, NewEmail string }
type EmailChangeAccepted struct {
	ChangeId    uuid.UUID
	ExpiresAt   time.Time
	ResendAfter int
}
type EmailChangeConfirmInput struct {
	ChallengeId *uuid.UUID
	Code, Token *string
}
type EmailChangeResult struct{ Completed bool }

// Tags preserve the persisted restriction idempotency contract.
type OperatorRestrictionInput struct {
	Reason     string `json:"reason"`
	Restricted bool   `json:"restricted"`
}
type OperatorRestrictionResult struct {
	ChangedAt         *time.Time `json:"changed_at"`
	OperatorAccountId *uuid.UUID `json:"operator_account_id"`
	Restricted        bool       `json:"restricted"`
}
type OperatorSearchInput struct {
	Page, PerPage int
	Q             string
}
type OperatorSearchResult struct {
	Clients       []Snapshot
	Page, PerPage int
	Total         int64
}
type TelegramInput struct {
	TelegramID             int64
	DisplayName, Locale    string
	RegistrationSourceCode *string
}

// TelegramSessionInput is trusted identity supplied only after Telegram signature validation.
type TelegramSessionInput struct {
	TelegramInput
	StartParam, AcceptedTermsVersion, AcceptedPrivacyVersion string
}

// SourceEligible is the common source-identity predicate, without subscription rules.
func SourceEligible(a Snapshot) bool {
	switch a.Kind {
	case "web":
		return a.VerifiedAt != nil && a.EmailKey != nil && a.PasswordSet
	case "telegram":
		return a.TelegramID != nil && *a.TelegramID > 0 && a.DisplayName != nil
	default:
		return false
	}
}
