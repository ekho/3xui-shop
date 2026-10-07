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
