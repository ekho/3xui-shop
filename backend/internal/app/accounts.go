package app

import (
	"context"
	"example.com/cabinet/backend/internal/modules/accounts"
	"example.com/cabinet/backend/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/riverqueue/river"
)

// NewService is the production composition root during the remaining extractions.
func NewService(pool *pgxpool.Pool, limiter *redis.Client, queue *river.Client[pgx.Tx], cfg platform.Config) *platform.Service {
	owner := accounts.New(pool, limiter, queue, accounts.Config{CabinetOrigin: cfg.CabinetOrigin, TermsVersion: cfg.TermsVersion, PrivacyVersion: cfg.PrivacyVersion, RateNamespace: cfg.RateNamespace, MailKey: cfg.MailKey, CodeKey: cfg.CodeKey, Operators: cfg.Operators}, func(ctx context.Context, to, subject, body string) error {
		return platform.SendSMTP(ctx, cfg, to, subject, body)
	})
	return platform.NewServiceWithAccounts(pool, limiter, queue, cfg, owner)
}
