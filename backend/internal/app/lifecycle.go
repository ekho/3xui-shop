package app

import (
	"context"
	"errors"
	"example.com/cabinet/backend/internal/modules/telegram"
	"net/http"
	"sync"
	"time"
)

func Serve(ctx context.Context, server *http.Server, result, schedulerResult <-chan error, tg *telegram.Runtime, additional ...*telegram.Runtime) error {
	channelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	channelDone := make(chan struct{})
	var channels sync.WaitGroup
	for _, channel := range append([]*telegram.Runtime{tg}, additional...) {
		if channel != nil && channel.State().Enabled {
			channels.Add(1)
			go func() { defer channels.Done(); _ = channel.Run(channelCtx) }()
		}
	}
	go func() { channels.Wait(); close(channelDone) }()
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
