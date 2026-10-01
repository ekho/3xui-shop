package s01

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"example.com/cabinet/backend/internal/store"
	"fmt"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type mailPayload struct{ Type, Locale, Token, Code string }

func (s *Service) enqueueMail(ctx context.Context, tx pgx.Tx, email string, challengeID *uuid.UUID, payload mailPayload) error {
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
	if err = store.New(tx).AddMail(ctx, store.AddMailParams{ID: id, ChallengeID: challengeID, EmailKey: email, Ciphertext: encrypted, CreatedAt: stamp(s.now())}); err != nil {
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
	email, err := q.MailEmailByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil || q.LockRegistrationEmail(ctx, email) != nil {
		return unavailable()
	}
	delivery, err := q.MailByID(ctx, id)
	if err != nil {
		return unavailable()
	}
	if len(delivery.Ciphertext) == 0 || delivery.DeliveredAt.Valid {
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
		subject = "Verify your email"
		body = fmt.Sprintf("Confirm your email: %s/verify-email#token=%s\nCode: %s\nChallenge: %s\nThe code expires in 10 minutes; the link expires in 24 hours.", s.cfg.CabinetOrigin, payload.Token, payload.Code, delivery.ChallengeID.String())
		if payload.Locale == "ru" {
			subject = "Подтверждение email"
			body = fmt.Sprintf("Подтвердить email: %s/verify-email#token=%s\nКод: %s\nНомер заявки: %s\nКод действует 10 минут; ссылка — 24 часа.", s.cfg.CabinetOrigin, payload.Token, payload.Code, delivery.ChallengeID.String())
		}
	}
	// The email advisory lock linearizes send with resend/verify. A previously sent email can only carry a revoked secret after those commit.
	if err = s.smtpSend(ctx, email, subject, body); err != nil {
		return unavailable()
	}
	if q.CompleteMail(ctx, store.CompleteMailParams{ID: id, DeliveredAt: stamp(s.now())}) != nil || tx.Commit(ctx) != nil {
		return unavailable()
	}
	return nil
}
func (s *Service) smtpSend(ctx context.Context, to, subject, body string) error {
	from, e := mail.ParseAddress(s.cfg.SMTPFrom)
	if e != nil || from.Address != s.cfg.SMTPFrom || strings.ContainsAny(s.cfg.SMTPFrom, "\r\n") {
		return unavailable()
	}
	host, _, e := net.SplitHostPort(s.cfg.SMTPAddress)
	if e != nil {
		return unavailable()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, RootCAs: s.cfg.SMTPRootCAs}}
	conn, e := dialer.DialContext(ctx, "tcp", s.cfg.SMTPAddress)
	if e != nil {
		return unavailable()
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	client, e := smtp.NewClient(conn, host)
	if e != nil {
		return unavailable()
	}
	defer client.Close()
	if s.cfg.SMTPUser != "" {
		if e = client.Auth(smtp.PlainAuth("", s.cfg.SMTPUser, s.cfg.SMTPPassword, host)); e != nil {
			return unavailable()
		}
	}
	if client.Mail(from.Address) != nil || client.Rcpt(to) != nil {
		return unavailable()
	}
	data, e := client.Data()
	if e != nil {
		return unavailable()
	}
	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n%s\r\n", from.Address, to, mime.BEncoding.Encode("UTF-8", subject), strings.ReplaceAll(body, "\n", "\r\n"))
	if _, e = data.Write([]byte(msg)); e != nil {
		return unavailable()
	}
	if e = data.Close(); e != nil {
		return unavailable()
	}
	if client.Quit() != nil {
		return unavailable()
	}
	return nil
}

type MailWorker struct {
	river.WorkerDefaults[MailArgs]
	Service *Service
}

func (w *MailWorker) Work(ctx context.Context, job *river.Job[MailArgs]) error {
	return w.Service.SendMail(ctx, job.Args.DeliveryID)
}
