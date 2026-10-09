package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"example.com/cabinet/backend/internal/modules/operations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func runBackupCommand(args []string) error {
	code := "INVALID_BACKUP_COMMAND"
	defer func() {
		if code != "" {
			slog.Error("backup operation failed", "code", code)
		}
	}()
	if (len(args) != 5 && len(args) != 7) || (args[0] != "create" && args[0] != "rehearse") || args[1] != "--operator-file" || args[3] != "--directory" || len(args) == 7 && (args[0] != "rehearse" || args[5] != "--target") || len(args) == 5 && args[0] != "create" {
		return errors.New(code)
	}
	actorText, err := operations.ReadPrivateText(args[2])
	if err != nil {
		code = "INVALID_OPERATOR_FILE"
		return errors.New(code)
	}
	actor, err := uuid.Parse(actorText)
	if err != nil || actor == uuid.Nil {
		code = "INVALID_OPERATOR_FILE"
		return errors.New(code)
	}
	if os.Getenv("DATABASE_URL") != "" {
		code = "INVALID_PRIVATE_FILE"
		return errors.New(code)
	}
	source, err := operations.ReadPrivateText(os.Getenv("DATABASE_URL_FILE"))
	if err != nil {
		code = "INVALID_PRIVATE_FILE"
		return errors.New(code)
	}
	restore := ""
	if args[0] == "rehearse" {
		if os.Getenv("RESTORE_DATABASE_URL") != "" {
			code = "INVALID_PRIVATE_FILE"
			return errors.New(code)
		}
		restore, err = operations.ReadPrivateText(os.Getenv("RESTORE_DATABASE_URL_FILE"))
		if err != nil {
			code = "INVALID_PRIVATE_FILE"
			return errors.New(code)
		}
	}
	if err = operations.ValidateConnectionURL(source); err != nil {
		code = err.Error()
		return err
	}
	if args[0] == "rehearse" {
		if err = operations.ValidateConnectionURL(restore); err != nil {
			code = err.Error()
			return err
		}
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalCtx, 30*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, source)
	if err != nil {
		code = "DATABASE_UNAVAILABLE"
		return errors.New(code)
	}
	defer pool.Close()
	svc := operations.New(pool, source, restore)
	if args[0] == "create" {
		err = svc.Create(ctx, actor, args[4])
	} else {
		err = svc.Rehearse(ctx, actor, args[4], args[6])
	}
	if err != nil {
		code = err.Error()
		return err
	}
	code = ""
	return nil
}
