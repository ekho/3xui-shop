package httpapi

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/support"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
)

func supportError(err error) error {
	if err == nil {
		return nil
	}
	var domain *support.Error
	if errors.As(err, &domain) {
		return &apiError{Status: domain.Status, Code: domain.Code, Message: domain.Message, RetryAfter: domain.RetryAfter}
	}
	return accountError(err)
}

func wireSupportConversation(c *support.SupportConversation) *wire.SupportConversation {
	if c == nil {
		return nil
	}
	return &wire.SupportConversation{CreatedAt: c.CreatedAt, CustomerReceivedSequence: c.CustomerReceivedSequence, Id: c.Id, OperatorReceivedSequence: c.OperatorReceivedSequence, Status: wire.SupportConversationStatus(c.Status), SupportBanned: c.SupportBanned, UpdatedAt: c.UpdatedAt}
}

func wireSupportMessage(m support.SupportMessage) wire.SupportMessage {
	out := wire.SupportMessage{CreatedAt: m.CreatedAt, Delivery: wire.SupportMessageDelivery(m.Delivery), Id: m.Id, Sender: wire.SupportMessageSender(m.Sender), Sequence: m.Sequence, Text: m.Text}
	if m.Attachment != nil {
		out.Attachment = &wire.SupportAttachment{Name: m.Attachment.Name, SizeBytes: m.Attachment.SizeBytes}
	}
	if m.TelegramDelivery != nil {
		delivery := wireSupportTelegramDelivery(*m.TelegramDelivery)
		out.TelegramDelivery = &delivery
	}
	return out
}

func wireSupportTelegramDelivery(d support.TelegramDeliveryStatus) wire.SupportTelegramDelivery {
	return wire.SupportTelegramDelivery{Id: d.Id, Status: wire.SupportTelegramDeliveryStatus(d.Status), Code: d.Code,
		RetryCapability: d.RetryCapability, MediaAvailability: wire.SupportTelegramDeliveryMediaAvailability(d.MediaAvailability)}
}

func wireSupportResult(r support.SupportResult) wire.SupportResult {
	out := wire.SupportResult{Conversation: wireSupportConversation(r.Conversation), HasMore: r.HasMore, OldestSequence: r.OldestSequence}
	if r.Messages != nil {
		out.Messages = make([]wire.SupportMessage, 0, len(r.Messages))
		for _, m := range r.Messages {
			out.Messages = append(out.Messages, wireSupportMessage(m))
		}
	}
	return out
}

func (a *API) requireSupportOperator(ctx context.Context, actor uuid.UUID) error {
	return accountError(a.accounts.RequireOperator(ctx, actor))
}

func (a *API) support(ctx context.Context, actor, target uuid.UUID, operator bool) (wire.SupportResult, error) {
	out, err := a.supportOwner.Support(ctx, actor, target, operator)
	return wireSupportResult(out), supportError(err)
}

func (a *API) supportHistory(ctx context.Context, actor, target uuid.UUID, operator bool, before int64) (wire.SupportResult, error) {
	out, err := a.supportOwner.SupportHistory(ctx, actor, target, operator, before)
	return wireSupportResult(out), supportError(err)
}

func (a *API) createSupportMessage(ctx context.Context, actor, target uuid.UUID, operator bool, key uuid.UUID, text, name string, file []byte) (wire.SupportMessage, bool, error) {
	out, created, err := a.supportOwner.CreateSupportMessage(ctx, actor, target, operator, key, text, name, file)
	return wireSupportMessage(out), created, supportError(err)
}

func (a *API) acknowledgeSupport(ctx context.Context, actor, target uuid.UUID, operator bool, sequence int64) error {
	return supportError(a.supportOwner.AcknowledgeSupport(ctx, actor, target, operator, sequence))
}

func (a *API) setSupportState(ctx context.Context, actor, target uuid.UUID, operator bool, state string) error {
	return supportError(a.supportOwner.SetSupportState(ctx, actor, target, operator, state))
}

func (a *API) setSupportBan(ctx context.Context, actor, target uuid.UUID, banned bool, reason string) error {
	return supportError(a.supportOwner.SetSupportBan(ctx, actor, target, banned, reason))
}

func (a *API) supportAttachment(ctx context.Context, actor, message uuid.UUID) (string, []byte, error) {
	name, body, err := a.supportOwner.SupportAttachment(ctx, actor, message)
	return name, body, supportError(err)
}
