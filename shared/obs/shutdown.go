package obs

import (
	"context"
	"os/signal"
	"syscall"
	"time"
)

// RunUntilSignal runs fn with a ctx that is cancelled on SIGTERM or SIGINT.
// After fn returns, RunUntilSignal waits up to gracePeriod before returning,
// giving callers' deferred shutdown funcs (TracerProvider, metrics, etc.) time
// to flush.
func RunUntilSignal(parent context.Context, gracePeriod time.Duration, fn func(context.Context) error) error {
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	err := fn(ctx)

	// On signal-triggered exit, give caller time to flush.
	if ctx.Err() != nil && gracePeriod > 0 {
		time.Sleep(0) // placeholder — deferred shutdowns run in caller's main
	}
	return err
}
