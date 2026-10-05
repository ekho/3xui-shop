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
	DisplayName                     *string
	CreatedAt, RestrictionChangedAt *time.Time
	RestrictionOperatorAccountID    *uuid.UUID
	AccessProfile                   *string
}

type RegisterInput struct {
	AcceptedPrivacyVersion, AcceptedTermsVersion, Email, Locale string
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
