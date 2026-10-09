package telegram

import (
	"context"
	"crypto/sha256"
	"errors"
	"example.com/cabinet/backend/internal/modules/audit_reports"
	"example.com/cabinet/backend/internal/modules/subscriptions"
	"example.com/cabinet/backend/internal/modules/support"
	"example.com/cabinet/backend/internal/modules/telegram/internal/botapi"
	"github.com/google/uuid"
	"strconv"
	"strings"
	"unicode/utf8"
)

func parseSupportCommand(text, username string) (support.TelegramCommandInput, uuid.UUID, bool) {
	var in support.TelegramCommandInput
	topic := uuid.Nil
	if !utf8.ValidString(text) || strings.ContainsRune(text, '\x00') || utf8.RuneCountInString(text) > 4096 {
		return in, topic, false
	}
	args := strings.Fields(text)
	if len(args) == 0 || !strings.HasPrefix(args[0], "/") {
		return in, topic, false
	}
	name, mention, hasMention := strings.Cut(strings.TrimPrefix(args[0], "/"), "@")
	if hasMention && (username == "" || !strings.EqualFold(mention, username)) {
		return in, topic, false
	}
	in.Action = name
	args = args[1:]
	n := 0
	switch name {
	case "info":
		if len(args) > 1 {
			return in, topic, false
		}
		if len(args) == 1 {
			if len(args[0]) != 36 {
				return in, topic, false
			}
			var err error
			topic, err = uuid.Parse(args[0])
			if err != nil || topic == uuid.Nil {
				return in, uuid.Nil, false
			}
			n = 1
		}
	case "pending", "reset":
		if len(args) != 0 {
			return in, topic, false
		}
	case "reset_traffic":
		in.Action = "reset"
	case "open":
		in.Action = "reopen"
	case "close", "reopen", "ban", "unban":
	case "comp":
		if len(args) < 1 {
			return in, topic, false
		}
		days, err := strconv.ParseUint(args[0], 10, 16)
		if err != nil || days < 1 || days > 365 {
			return in, topic, false
		}
		in.Days, n = int(days), 1
	case "approve", "reject", "reconsider", "retry", "bind":
		if len(args) < 1 || len(args[0]) != 36 {
			return in, topic, false
		}
		id, err := uuid.Parse(args[0])
		if err != nil || id == uuid.Nil {
			return in, topic, false
		}
		n = 1
		switch name {
		case "retry":
			if len(args) < 2 {
				return in, topic, false
			}
			in.DeliveryID = id
		case "bind":
			if len(args) < 2 {
				return in, topic, false
			}
			thread, err := strconv.ParseUint(args[1], 10, 52)
			if err != nil || thread <= 1 {
				return in, topic, false
			}
			topic, in.ThreadID, n = id, int64(thread), 2
		default:
			in.TrialRequestID = id
		}
	default:
		return in, topic, false
	}
	in.Reason = strings.Join(args[n:], " ")
	return in, topic, utf8.RuneCountInString(in.Reason) <= 1000
}

func (b *supportBridge) runSupportCommand(ctx context.Context, source support.TelegramSource, topic uuid.UUID, input support.TelegramCommandInput) error {
	r, err := b.owner.BeginTelegramCommand(ctx, source, topic, input)
	if err != nil {
		return err
	}
	if r.Result != nil || r.AwaitingConfirmation {
		return nil
	}
	ctx = auditreports.WithTelegramActor(ctx, r.ActorAccountID, source.ActorID)
	result := support.TelegramCommandResult{Status: "completed"}
	switch r.Input.Action {
	case "pending":
		var rows []subscriptions.PendingOperatorTrial
		rows, result.More, err = b.access.PendingOperatorTrials(ctx, r.ActorAccountID)
		for _, row := range rows {
			result.Links = append(result.Links, support.TelegramCommandLink{AccountID: row.AccountID, RequestID: row.RequestID})
		}
	case "info":
		if r.TargetAccountID != nil {
			result.Links = []support.TelegramCommandLink{{AccountID: *r.TargetAccountID}}
		}
	case "comp", "reset":
		if r.TargetAccountID == nil {
			return &support.Error{Status: 409, Code: "REQUEST_STATE_CONFLICT"}
		}
		in := subscriptions.AccessOperationInput{Kind: "reset_traffic", Reason: r.Input.Reason}
		if r.Input.Action == "comp" {
			in.Kind = "compensate"
			in.Days = &r.Input.Days
		}
		var op subscriptions.AccessOperation
		op, err = b.access.CreateAccessOperation(ctx, r.ActorAccountID, *r.TargetAccountID, r.OwnerKey, in)
		if err == nil {
			result.Status, result.OperationID = op.Status, op.OperationId
		}
	case "approve", "reject", "reconsider":
		if r.TargetAccountID == nil {
			return &support.Error{Status: 409, Code: "REQUEST_STATE_CONFLICT"}
		}
		var rows []subscriptions.OperatorTrialRequest
		rows, _, err = b.access.TrialHistory(ctx, r.ActorAccountID, *r.TargetAccountID, nil, nil)
		if err == nil {
			if len(rows) == 0 || rows[0].RequestId != r.Input.TrialRequestID && (r.Input.Action != "reconsider" || rows[0].PreviousRequestId == nil || *rows[0].PreviousRequestId != r.Input.TrialRequestID) {
				err = &subscriptions.Error{Status: 409, Code: "REQUEST_STATE_CONFLICT"}
			} else if r.Input.Action == "reconsider" {
				var request subscriptions.TrialRequest
				request, _, err = b.access.ReconsiderOperatorTrial(ctx, r.ActorAccountID, r.Input.TrialRequestID, r.OwnerKey, r.Input.Reason)
				if err == nil {
					result.Status, result.RequestID = request.Status, request.RequestId
				}
			} else {
				var decided subscriptions.OperatorDecisionResult
				decided, _, err = b.access.DecideOperatorTrial(ctx, r.ActorAccountID, r.Input.TrialRequestID, r.OwnerKey, subscriptions.OperatorDecisionInput{Decision: r.Input.Action, Reason: r.Input.Reason})
				if err == nil {
					result.Status, result.RequestID = decided.Request.Status, decided.Request.RequestId
					if result.Status == "approved" {
						result.Status = "queued"
					}
					if decided.OperationId != nil {
						result.OperationID = *decided.OperationId
					}
				}
			}
		}
	case "retry":
		if r.TargetAccountID == nil {
			return &support.Error{Status: 409, Code: "REQUEST_STATE_CONFLICT"}
		}
		var delivery support.TelegramDeliveryStatus
		delivery, _, err = b.owner.RetryTelegramDelivery(ctx, r.ActorAccountID, *r.TargetAccountID, r.Input.DeliveryID, r.OwnerKey, r.Input.Confirmed, r.Input.Reason)
		if err == nil {
			result.Status, result.DeliveryID = "queued", delivery.Id
		}
	case "bind":
		return b.owner.BindTelegramTopic(ctx, r.ID, r.Input.ThreadID, r.Input.Confirmed)
	default:
		return &support.Error{Status: 400, Code: "INVALID_INPUT"}
	}
	if err != nil {
		var access *subscriptions.Error
		var owner *support.Error
		switch {
		case errors.As(err, &access) && access.Status < 500:
			result.Status, result.Code = "failed", access.Code
		case errors.As(err, &owner) && owner.Status < 500:
			result.Status, result.Code = "failed", owner.Code
		default:
			return err
		}
	}
	return b.owner.CompleteTelegramCommand(ctx, r.ID, result)
}

func (b *supportBridge) handleSupportCallback(ctx context.Context, cb botapi.Callback, update int64) error {
	m := cb.Message
	if m == nil || m.Date <= 0 || m.From == nil || !m.From.IsBot || m.From.ID != b.botID || m.Chat.ID != b.cfg.GroupID || m.Chat.Type != "supergroup" || m.ID <= 0 || m.ID > 1<<52-1 || m.ThreadID < 0 || m.ThreadID > 1<<52-1 || cb.From.IsBot || cb.From.ID <= 0 || cb.From.ID > 1<<52-1 || len(cb.ID) < 1 || len(cb.ID) > 128 || !utf8.ValidString(cb.ID) || strings.ContainsRune(cb.ID, '\x00') || len(cb.Data) > 64 || !utf8.ValidString(cb.Data) || strings.ContainsRune(cb.Data, '\x00') {
		return nil
	}
	proof, actor, err := b.authority.ResolveTelegramContext(ctx, cb.From.ID)
	if err != nil {
		return supportHandlingError(err)
	}
	if actor == nil {
		return nil
	}
	if err = b.authority.RequireOperator(proof, actor.ID); err != nil {
		return supportHandlingError(err)
	}
	if strings.HasPrefix(cb.Data, "approval:") || strings.HasPrefix(cb.Data, "comp_reset_") {
		return b.api.AnswerCallback(ctx, cb.ID, "Legacy action: use the protected cabinet "+b.origin+"/admin/clients", true)
	}
	args := strings.Split(cb.Data, ":")
	if len(args) != 3 || args[0] != "sp1" || (args[1] != "c" && args[1] != "x") || len(args[2]) != 36 {
		return nil
	}
	id, err := uuid.Parse(args[2])
	if err != nil || id == uuid.Nil {
		return nil
	}
	action := "confirm"
	if args[1] == "x" {
		action = "cancel"
	}
	source := support.TelegramSource{BotID: b.botID, GroupID: b.cfg.GroupID, ChatID: m.Chat.ID, MessageID: m.ID, UpdateID: update, ActorID: cb.From.ID, ThreadID: m.ThreadID, Action: action, CallbackID: cb.ID, Digest: sha256.Sum256([]byte(cb.Data))}
	if err = b.runSupportCommand(proof, source, uuid.Nil, support.TelegramCommandInput{Action: action, ConfirmationID: id, Confirmed: action == "confirm"}); err != nil {
		return supportHandlingError(err)
	}
	return b.api.AnswerCallback(ctx, cb.ID, "Accepted", false)
}
