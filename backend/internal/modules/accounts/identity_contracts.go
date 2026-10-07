package accounts

import (
	"github.com/google/uuid"
	"time"
)

type IdentityContext struct {
	Email                                       *string
	SourceKind                                  string
	IndependentLogin, TelegramLinked, CanUnlink bool
	UnlinkBlockedReason                         *string
	PendingInitialEmail                         *IdentityEmailPending
}
type IdentityEmailPending struct {
	ChallengeId uuid.UUID
	Email       string
	ExpiresAt   time.Time
}
type InitialEmailInput struct{ Email string }
type InitialEmailCompleteInput struct {
	ChallengeId                                                     uuid.UUID
	Code, NewPassword, AcceptedTermsVersion, AcceptedPrivacyVersion string
}

type TelegramLinkChallenge struct {
	LinkToken string
	ExpiresAt time.Time
}

// ConfirmTelegramLinkInput contains identity verified by the Telegram adapter.
type ConfirmTelegramLinkInput struct {
	TelegramSessionInput
	LinkToken string
}
type TelegramLinkResult struct{ Linked bool }
type TelegramUnlinkResult struct{ Changed bool }

type OperatorRecoveryInput struct {
	Email, CurrentPassword, Reason string
	Confirmed                      bool
}
type IdentityRecoveryAccepted struct {
	ChallengeId uuid.UUID
	ExpiresAt   time.Time
	ResendAfter int64
}
type IdentityRecoveryCompleteInput struct {
	ChallengeId                                               *uuid.UUID
	Code, Token                                               *string
	NewPassword, AcceptedTermsVersion, AcceptedPrivacyVersion string
}
