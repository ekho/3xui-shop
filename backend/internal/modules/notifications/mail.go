package notifications

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"example.com/cabinet/backend/internal/modules/notifications/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
)

type MailPayload struct{ Type, Locale, Token, Code string }
type MailArgs struct {
	DeliveryID uuid.UUID `json:"delivery_id"`
}

func (MailArgs) Kind() string { return "mail_delivery" }

type MailConfig struct {
	CabinetOrigin, SMTPAddress, SMTPFrom, SMTPUser, SMTPPassword string
	MailKey                                                      []byte
	SMTPRootCAs                                                  *x509.CertPool
	Now                                                          func() time.Time
}
type MailService struct {
	pool   *pgxpool.Pool
	queue  *river.Client[pgx.Tx]
	cfg    func() MailConfig
	guard  func(context.Context, string, func(*pgxpool.Conn) error) error
	valid  func(context.Context, pgx.Tx, *uuid.UUID, *uuid.UUID) (bool, error)
	sender func(context.Context, string, string, string) error
}

func NewMail(pool *pgxpool.Pool, queue *river.Client[pgx.Tx], cfg func() MailConfig, guard func(context.Context, string, func(*pgxpool.Conn) error) error, valid func(context.Context, pgx.Tx, *uuid.UUID, *uuid.UUID) (bool, error), sender func(context.Context, string, string, string) error) *MailService {
	return &MailService{pool: pool, queue: queue, cfg: cfg, guard: guard, valid: valid, sender: sender}
}
func (s *MailService) now() time.Time {
	if now := s.cfg().Now; now != nil {
		return now()
	}
	return time.Now()
}
func mailStamp(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }
func (s *MailService) EnqueueMailTx(ctx context.Context, tx pgx.Tx, email string, registrationID, credentialID *uuid.UUID, kind string, payload MailPayload, createdAt time.Time) error {
	id := uuid.New()
	plain, err := json.Marshal(payload)
	if err != nil {
		return unavailable()
	}
	block, err := aes.NewCipher(s.cfg().MailKey)
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
	q := store.New(tx)
	if kind == "registration" {
		err = q.AddMail(ctx, store.AddMailParams{ID: id, ChallengeID: registrationID, EmailKey: email, Ciphertext: encrypted, CreatedAt: mailStamp(createdAt)})
	} else {
		err = q.AddCredentialMail(ctx, store.AddCredentialMailParams{ID: id, CredentialChallengeID: credentialID, EmailKey: email, Ciphertext: encrypted, CreatedAt: mailStamp(createdAt), Kind: kind})
	}
	if err != nil {
		return unavailable()
	}
	if _, err = s.queue.InsertTx(ctx, tx, MailArgs{DeliveryID: id}, &river.InsertOpts{MaxAttempts: 5}); err != nil {
		return unavailable()
	}
	return nil
}
func (s *MailService) ClearRegistrationMailTx(ctx context.Context, tx pgx.Tx, email string) error {
	if store.New(tx).RevokeRegistrationMail(ctx, email) != nil {
		return unavailable()
	}
	return nil
}
func (s *MailService) ClearCredentialMailTx(ctx context.Context, tx pgx.Tx, proofIDs []uuid.UUID) error {
	if len(proofIDs) == 0 {
		return nil
	}
	if store.New(tx).ClearCredentialMail(ctx, proofIDs) != nil {
		return unavailable()
	}
	return nil
}
func (s *MailService) SendMail(ctx context.Context, id uuid.UUID) error {
	ref, err := store.New(s.pool).LookupMail(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return unavailable()
	}
	err = s.guard(ctx, ref.EmailKey, func(conn *pgxpool.Conn) error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return unavailable()
		}
		defer tx.Rollback(ctx)
		q := store.New(tx)
		credentialValid := true
		if ref.CredentialChallengeID != nil {
			credentialValid, err = s.valid(ctx, tx, nil, ref.CredentialChallengeID)
			if err != nil {
				return unavailable()
			}
		}
		delivery, err := q.MailByID(ctx, id)
		if err != nil {
			return unavailable()
		}
		if len(delivery.Ciphertext) == 0 || delivery.DeliveredAt.Valid {
			return nil
		}
		complete := func() error {
			if q.CompleteMail(ctx, store.CompleteMailParams{ID: id, DeliveredAt: mailStamp(s.now())}) != nil || tx.Commit(ctx) != nil {
				return unavailable()
			}
			return nil
		}
		if !credentialValid {
			return complete()
		}
		if delivery.ChallengeID != nil {
			valid, err := s.valid(ctx, tx, delivery.ChallengeID, nil)
			if err != nil {
				return unavailable()
			}
			if !valid {
				return complete()
			}
		}
		cfg := s.cfg()
		block, err := aes.NewCipher(cfg.MailKey)
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
		var payload MailPayload
		if json.Unmarshal(plain, &payload) != nil {
			return unavailable()
		}
		subject := "Sign in to your account"
		body := "Sign in: " + cfg.CabinetOrigin + "/login"
		if payload.Locale == "ru" {
			subject = "Вход в кабинет"
			body = "Войти в кабинет: " + cfg.CabinetOrigin + "/login"
		}
		if payload.Type == "verify" {
			if delivery.ChallengeID == nil {
				return unavailable()
			}
			subject = "Verify your email"
			body = fmt.Sprintf("Confirm your email: %s/verify-email#token=%s\nCode: %s\nChallenge: %s\nThe code expires in 10 minutes; the link expires in 24 hours.", cfg.CabinetOrigin, payload.Token, payload.Code, delivery.ChallengeID.String())
			if payload.Locale == "ru" {
				subject = "Подтверждение email"
				body = fmt.Sprintf("Подтвердить email: %s/verify-email#token=%s\nКод: %s\nНомер заявки: %s\nКод действует 10 минут; ссылка — 24 часа.", cfg.CabinetOrigin, payload.Token, payload.Code, delivery.ChallengeID.String())
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
			body = fmt.Sprintf("%s: %s%s?lang=%s#token=%s\nCode: %s\nChallenge: %s\nThe code expires in 10 minutes; the link expires in 30 minutes.", instruction, cfg.CabinetOrigin, path, payload.Locale, payload.Token, payload.Code, delivery.CredentialChallengeID.String())
			if payload.Locale == "ru" {
				subject = "Восстановление пароля"
				instruction = "Восстановить пароль"
				if path == "/confirm-email-change" {
					subject = "Подтверждение смены email"
					instruction = "Подтвердите доступ к этой почте для смены адреса аккаунта; обязательны оба подтверждения"
				}
				body = fmt.Sprintf("%s: %s%s?lang=ru#token=%s\nКод: %s\nНомер проверки: %s\nКод действует 10 минут; ссылка — 30 минут.", instruction, cfg.CabinetOrigin, path, payload.Token, payload.Code, delivery.CredentialChallengeID.String())
			}
			if payload.Type == "initial_email" {
				subject = "Add email login to your account"
				body = fmt.Sprintf("Enter this code in the Telegram Mini App to add independent email login to your existing account.\nCode: %s\nChallenge: %s\nThe code expires in 10 minutes.", payload.Code, delivery.CredentialChallengeID.String())
				if payload.Locale == "ru" {
					subject = "Добавление входа по email"
					body = fmt.Sprintf("Введите код в Telegram Mini App для добавления независимого входа по email к существующему аккаунту.\nКод: %s\nНомер проверки: %s\nКод действует 10 минут.", payload.Code, delivery.CredentialChallengeID.String())
				}
			}
			if payload.Type == "identity_recovery" {
				subject = "Recover your account"
				body = fmt.Sprintf("Support has started recovery of your existing account. Confirm explicitly and set a password: %s/recover-account?lang=%s#token=%s\nCode: %s\nChallenge: %s\nThe code expires in 10 minutes; the link expires in 30 minutes.", cfg.CabinetOrigin, payload.Locale, payload.Token, payload.Code, delivery.CredentialChallengeID.String())
				if payload.Locale == "ru" {
					subject = "Восстановление доступа к аккаунту"
					body = fmt.Sprintf("Поддержка начала восстановление существующего аккаунта. Подтвердите действие и задайте пароль: %s/recover-account?lang=ru#token=%s\nКод: %s\nНомер проверки: %s\nКод действует 10 минут; ссылка — 30 минут.", cfg.CabinetOrigin, payload.Token, payload.Code, delivery.CredentialChallengeID.String())
				}
			}
		}
		if delivery.Kind == "security_notice" {
			subject = "Account security changed"
			body = "Your account security settings were changed. If this was not you, contact support. Sign in: " + cfg.CabinetOrigin + "/login"
			if payload.Locale == "ru" {
				subject = "Изменение безопасности аккаунта"
				body = "Данные входа аккаунта изменены. Если это сделали не вы, обратитесь в поддержку. Вход: " + cfg.CabinetOrigin + "/login"
			}
		}

		// Keep the email session guard, but release all SQL row locks before SMTP.
		if tx.Commit(ctx) != nil {
			return unavailable()
		}
		if s.sender != nil {
			err = s.sender(ctx, ref.EmailKey, subject, body)
		} else {
			err = SendSMTP(ctx, s.cfg(), ref.EmailKey, subject, body)
		}
		if err != nil {
			return unavailable()
		}
		tx, err = conn.Begin(ctx)
		if err != nil {
			return unavailable()
		}
		defer tx.Rollback(ctx)
		if store.New(tx).CompleteMail(ctx, store.CompleteMailParams{ID: id, DeliveredAt: mailStamp(s.now())}) != nil || tx.Commit(ctx) != nil {
			return unavailable()
		}
		return nil
	})
	if err != nil {
		return unavailable()
	}
	return nil
}

type MailWorker struct {
	river.WorkerDefaults[MailArgs]
	Service *MailService
}

func (w *MailWorker) Work(ctx context.Context, job *river.Job[MailArgs]) error {
	return w.Service.SendMail(ctx, job.Args.DeliveryID)
}
