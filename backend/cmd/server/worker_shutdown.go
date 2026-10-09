package main

import (
	"context"
	"errors"
	"time"
)

type riverStopper interface {
	Stop(context.Context) error
	StopAndCancel(context.Context) error
}

func stopRiver(worker riverStopper, grace, hardGrace time.Duration) (bool, error) {
	graceCtx, done := context.WithTimeout(context.Background(), grace)
	err := worker.Stop(graceCtx)
	done()
	if err == nil {
		return true, nil
	}
	hardCtx, hardDone := context.WithTimeout(context.Background(), hardGrace)
	hardErr := worker.StopAndCancel(hardCtx)
	hardDone()
	return hardErr == nil, errors.Join(err, hardErr)
}
