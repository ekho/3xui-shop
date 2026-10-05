package app

import (
	"bytes"
	"context"
	"example.com/cabinet/backend/internal/platform"
	"example.com/cabinet/backend/internal/testkit"
	"example.com/cabinet/backend/internal/wire"
	"github.com/google/uuid"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"strings"
	"testing"
)

func TestAccountsComposition(t *testing.T) {
	e := testkit.Open(t)
	queue, err := river.NewClient(riverpgxv5.New(e.Pool), &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	smtp := testkit.MailServer(t)
	cfg := platform.Config{CabinetOrigin: "https://cabinet.example.test", TermsVersion: "1", PrivacyVersion: "1", RateNamespace: uuid.NewString(), MailKey: bytes.Repeat([]byte{1}, 32), CodeKey: bytes.Repeat([]byte{2}, 32), SMTPAddress: smtp.Address, SMTPRootCAs: smtp.Roots, SMTPFrom: "sender@example.test"}
	s := NewService(e.Pool, e.Redis, queue, cfg)
	ctx := context.Background()
	r, err := s.Register(ctx, wire.RegisterInput{Email: "composed@example.test", Locale: "en", AcceptedTermsVersion: "1", AcceptedPrivacyVersion: "1"})
	if err != nil {
		t.Fatal(err)
	}
	id, token, _ := testkit.MailSecrets(t, e.Pool, cfg.MailKey, r.ChallengeId)
	if err = s.SendMail(ctx, id); err != nil || len(smtp.Letters()) != 1 || !strings.Contains(smtp.Letters()[0], "/verify-email#token="+token) {
		t.Fatal("composed TLS SMTP", err)
	}
	if _, err = s.VerifyEmail(ctx, wire.VerifyInput{Token: &token, NewPassword: "my long safe password ✨"}); err != nil {
		t.Fatal(err)
	}
	out, raw, err := s.Login(ctx, wire.LoginInput{Email: "composed@example.test", Password: "my long safe password ✨"}, "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ChangeOperatorRole(ctx, out.Account.AccountId, true); err != nil {
		t.Fatal(err)
	}
	if err = s.RequireSupportOperator(ctx, out.Account.AccountId); err != nil {
		t.Fatal("composed operator owner", err)
	}
	if _, err = s.Authenticate(ctx, raw); err != nil {
		t.Fatal("composed session owner", err)
	}
}
