package obs_test

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mharner33/voting-app/shared/obs"
)

func TestRunUntilSignal_CancelsContextOnSignal(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan error, 1)

	go func() {
		err := obs.RunUntilSignal(context.Background(), 5*time.Second, func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
		finished <- err
	}()

	<-started
	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGTERM))

	select {
	case err := <-finished:
		require.True(t, errors.Is(err, context.Canceled) || err == nil)
	case <-time.After(2 * time.Second):
		t.Fatal("RunUntilSignal did not return after SIGTERM")
	}
}
