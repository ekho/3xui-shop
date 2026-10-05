package app

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/telegram"
	"net/http"
	"time"
)

func Serve(ctx context.Context, server *http.Server, result, schedulerResult <-chan error, tg *telegram.Runtime) error {
	channelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	channelDone := make(chan struct{})
	if tg != nil && tg.State().Enabled {
		go func() { defer close(channelDone); _ = tg.Run(channelCtx) }()
	} else {
		close(channelDone)
	}
	var err error
	select {
	case err = <-schedulerResult:
	case err = <-result:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	case <-ctx.Done():
	}
	cancel()
	stop, done := context.WithTimeout(context.Background(), 20*time.Second)
	defer done()
	shutdownErr := server.Shutdown(stop)
	select {
	case <-channelDone:
	case <-stop.Done():
		if shutdownErr == nil {
			shutdownErr = stop.Err()
		}
	}
	if err != nil {
		return err
	}
	return shutdownErr
}
