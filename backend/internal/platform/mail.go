package platform

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

func (s *Service) SendMail(ctx context.Context, id uuid.UUID) error {
	return accountError(s.accounts.SendMail(ctx, id))
}
func (s *Service) smtpSend(ctx context.Context, to, subject, body string) error {
	return SendSMTP(ctx, s.cfg, to, subject, body)
}
func SendSMTP(ctx context.Context, cfg Config, to, subject, body string) error {
	from, e := mail.ParseAddress(cfg.SMTPFrom)
	if e != nil || from.Address != cfg.SMTPFrom || strings.ContainsAny(cfg.SMTPFrom, "\r\n") {
		return unavailable()
	}
	host, _, e := net.SplitHostPort(cfg.SMTPAddress)
	if e != nil {
		return unavailable()
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host, RootCAs: cfg.SMTPRootCAs}}
	conn, e := dialer.DialContext(ctx, "tcp", cfg.SMTPAddress)
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
	if cfg.SMTPUser != "" {
		if e = client.Auth(smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, host)); e != nil {
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
