package accounts

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/modules/accounts/internal/store"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

type mailPayload struct{ Type, Locale, Token, Code string }

func (s *Service) enqueueMail(ctx context.Context, tx pgx.Tx, email string, challengeID *uuid.UUID, payload mailPayload) error {
	return s.enqueueMailDelivery(ctx, tx, email, challengeID, nil, "registration", payload)
}
func (s *Service) enqueueCredentialMail(ctx context.Context, tx pgx.Tx, email string, proofID uuid.UUID, payload mailPayload) error {
	return s.enqueueMailDelivery(ctx, tx, email, nil, &proofID, "credential", payload)
}
func (s *Service) enqueueSecurityNotice(ctx context.Context, tx pgx.Tx, email string, payload mailPayload) error {
	return s.enqueueMailDelivery(ctx, tx, email, nil, nil, "security_notice", payload)
}
func (s *Service) enqueueMailDelivery(ctx context.Context, tx pgx.Tx, email string, registrationID, credentialID *uuid.UUID, kind string, payload mailPayload) error {
	id := uuid.New()
	plain, err := json.Marshal(payload)
	if err != nil {
		return unavailable()
	}
	block, err := aes.NewCipher(s.cfg.MailKey)
	if err != nil {
		return unavailable()
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return unavailable()
	}
	nonce := make([]byte, gcm.NonceSize())
	rand.Read(nonce)
	encrypted := gcm.Seal(nonce, nonce, plain, []byte(id.String()))
	if kind == "registration" {
		err = store.New(tx).AddMail(ctx, store.AddMailParams{ID: id, ChallengeID: registrationID, EmailKey: email, Ciphertext: encrypted, CreatedAt: stamp(s.now())})
	} else {
		err = store.New(tx).AddCredentialMail(ctx, store.AddCredentialMailParams{ID: id, CredentialChallengeID: credentialID, EmailKey: email, Ciphertext: encrypted, CreatedAt: stamp(s.now()), Kind: kind})
	}
	if err != nil {
		return unavailable()
	}
	if _, err = s.queue.InsertTx(ctx, tx, MailArgs{DeliveryID: id}, &river.InsertOpts{MaxAttempts: 5}); err != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) SendMail(ctx context.Context, id uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return unavailable()
	}
	defer tx.Rollback(ctx)
	q := store.New(tx)
	ref, err := q.LookupMail(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil || q.LockRegistrationEmail(ctx, ref.EmailKey) != nil {
		return unavailable()
	}
	email := ref.EmailKey
	credentialInvalid := false
	if ref.CredentialChallengeID != nil {
		proof, err := q.LockCredentialProof(ctx, *ref.CredentialChallengeID)
		if err != nil {
			return unavailable()
		}
		credentialInvalid = proof.AccountID == nil || proof.Revoked || proof.UsedAt.Valid || proof.ConfirmedAt.Valid || !s.now().Before(proof.TokenExpiresAt.Time)
		if !credentialInvalid {
			account, err := q.AccountByID(ctx, *proof.AccountID)
			if err != nil {
				return unavailable()
			}
			credentialInvalid = proof.CredentialVersion != account.CredentialVersion || proof.OriginalEmail != account.EmailKey.String
		}
	}
	delivery, err := q.MailByID(ctx, id)
	if err != nil {
		return unavailable()
	}
	if len(delivery.Ciphertext) == 0 || delivery.DeliveredAt.Valid {
		return nil
	}
	if credentialInvalid {
		if q.CompleteMail(ctx, store.CompleteMailParams{ID: id, DeliveredAt: stamp(s.now())}) != nil || tx.Commit(ctx) != nil {
			return unavailable()
		}
		return nil
	}
	if delivery.ChallengeID != nil {
		challenge, err := q.ChallengeByID(ctx, *delivery.ChallengeID)
		if err != nil {
			return unavailable()
		}
		if challenge.Revoked || !s.now().Before(challenge.TokenExpiresAt.Time) {
			if q.CompleteMail(ctx, store.CompleteMailParams{ID: id, DeliveredAt: stamp(s.now())}) != nil || tx.Commit(ctx) != nil {
				return unavailable()
			}
			return nil
		}
	}
	block, err := aes.NewCipher(s.cfg.MailKey)
	if err != nil {
		return unavailable()
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(delivery.Ciphertext) < gcm.NonceSize() {
		return unavailable()
	}
	plain, err := gcm.Open(nil, delivery.Ciphertext[:gcm.NonceSize()], delivery.Ciphertext[gcm.NonceSize():], []byte(id.String()))
	if err != nil {
		return unavailable()
	}
	var payload mailPayload
	if json.Unmarshal(plain, &payload) != nil {
		return unavailable()
	}
	subject := "Sign in to your account"
	body := "Sign in: " + s.cfg.CabinetOrigin + "/login"
	if payload.Locale == "ru" {
		subject = "Вход в кабинет"
		body = "Войти в кабинет: " + s.cfg.CabinetOrigin + "/login"
	}
	if payload.Type == "verify" {
		if delivery.ChallengeID == nil {
			return unavailable()
		}
		subject = "Verify your email"
		body = fmt.Sprintf("Confirm your email: %s/verify-email#token=%s\nCode: %s\nChallenge: %s\nThe code expires in 10 minutes; the link expires in 24 hours.", s.cfg.CabinetOrigin, payload.Token, payload.Code, delivery.ChallengeID.String())
		if payload.Locale == "ru" {
			subject = "Подтверждение email"
			body = fmt.Sprintf("Подтвердить email: %s/verify-email#token=%s\nКод: %s\nНомер заявки: %s\nКод действует 10 минут; ссылка — 24 часа.", s.cfg.CabinetOrigin, payload.Token, payload.Code, delivery.ChallengeID.String())
		}
	}
	if delivery.Kind == "credential" {
		if delivery.CredentialChallengeID == nil {
			return unavailable()
		}
		path := "/reset-password"
		subject = "Reset your password"
		instruction := "Reset your password"
		if payload.Type == "email_change_old" || payload.Type == "email_change_new" {
			path = "/confirm-email-change"
			subject = "Confirm your email change"
			instruction = "Confirm access to this mailbox to change your account email; both mailboxes must be confirmed"
		}
		body = fmt.Sprintf("%s: %s%s?lang=%s#token=%s\nCode: %s\nChallenge: %s\nThe code expires in 10 minutes; the link expires in 30 minutes.", instruction, s.cfg.CabinetOrigin, path, payload.Locale, payload.Token, payload.Code, delivery.CredentialChallengeID.String())
		if payload.Locale == "ru" {
			subject = "Восстановление пароля"
			instruction = "Восстановить пароль"
			if path == "/confirm-email-change" {
				subject = "Подтверждение смены email"
				instruction = "Подтвердите доступ к этой почте для смены адреса аккаунта; обязательны оба подтверждения"
			}
			body = fmt.Sprintf("%s: %s%s?lang=ru#token=%s\nКод: %s\nНомер проверки: %s\nКод действует 10 минут; ссылка — 30 минут.", instruction, s.cfg.CabinetOrigin, path, payload.Token, payload.Code, delivery.CredentialChallengeID.String())
		}
	}
	if delivery.Kind == "security_notice" {
		subject = "Account security changed"
		body = "Your account security settings were changed. If this was not you, contact support. Sign in: " + s.cfg.CabinetOrigin + "/login"
		if payload.Locale == "ru" {
			subject = "Изменение безопасности аккаунта"
			body = "Данные входа аккаунта изменены. Если это сделали не вы, обратитесь в поддержку. Вход: " + s.cfg.CabinetOrigin + "/login"
		}
	}
	// The email advisory lock linearizes send with resend/verify. A previously sent email can only carry a revoked secret after those commit.
	if s.sender == nil {
		return unavailable()
	}
	if err = s.sender(ctx, email, subject, body); err != nil {
		return unavailable()
	}
	if q.CompleteMail(ctx, store.CompleteMailParams{ID: id, DeliveredAt: stamp(s.now())}) != nil || tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}
