package notifications

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"time"
)

func (s *Service) WithInfrastructureDeliveryGuard(guard ClientGuard) {
	s.infrastructureGuard = guard
}

func (s *Service) EnqueueGroupAlertTx(ctx context.Context, tx pgx.Tx, recipient ReminderRecipient, target uuid.UUID, code string, at time.Time) error {
	if target == uuid.Nil || groupAlertText(code, recipient.Locale, target) == "" || !recipient.Eligible {
		return failure(400, "INVALID_INPUT")
	}
	// shortcut: identical failures notify once per UTC day, add incident state if same-day recovery needs repeat alerts.
	event := "vpn-reconcile:" + target.String() + ":" + code + ":" + at.UTC().Format("2006-01-02")
	return s.EnqueueClientTx(ctx, tx, ClientNotice{AccountID: recipient.AccountID, TelegramID: recipient.TelegramID, CredentialVersion: recipient.CredentialVersion, Locale: recipient.Locale, EventKey: event, Route: "cabinet", VPNAlertCode: code, VPNAlertAccountID: target}, at)
}

func groupAlertText(code, locale string, account uuid.UUID) string {
	copy := map[string][2]string{
		"panel_unavailable":      {"Не удалось прочитать состояние панели.", "The panel state could not be read."},
		"identity_mismatch":      {"Идентификаторы клиента панели не совпадают. Клиент не изменён.", "The panel client identity does not match. The client was left unchanged."},
		"missing_client":         {"Клиент не найден в назначенной панели. Новый клиент не создан.", "The client is missing from its assigned panel. No client was created."},
		"unknown_profile":        {"Профиль доступа неизвестен. Членство клиента не изменено.", "The access profile is unknown. Client memberships were left unchanged."},
		"empty_membership":       {"Теги профиля не задают доступные инбаунды. Членство сохранено; VPN-блокировка действует отдельно.", "The profile tags resolve to no enabled inbounds. Memberships were preserved; VPN ban is enforced separately."},
		"operation_needs_review": {"Результат сверки требует проверки. Новые операции доступа ожидают подтверждённой сверки.", "Reconciliation needs review. Further access operations await confirmed reconciliation."},
	}
	text, ok := copy[code]
	if !ok || account == uuid.Nil {
		return ""
	}
	if locale == "ru" {
		return fmt.Sprintf("Сверка VPN\nАккаунт: %s\n%s\nПроверьте серверы и журнал в кабинете оператора.", account, text[0])
	}
	if locale == "en" {
		return fmt.Sprintf("VPN reconciliation\nAccount: %s\n%s\nCheck servers and history in the operator console.", account, text[1])
	}
	return ""
}
