package tally_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mharner33/voting-app/shared/obs"
	"github.com/mharner33/voting-app/tally-worker/internal/tally"
)

type fakeAgg struct {
	calls int32
	err   error
}

func (f *fakeAgg) Run(ctx context.Context) (tally.Stats, error) {
	atomic.AddInt32(&f.calls, 1)
	return tally.Stats{RowsUpserted: 1, PollsTouched: 1}, f.err
}

func TestRunner_TicksUntilContextCancelled(t *testing.T) {
	a := &fakeAgg{}
	log := obs.NewLogger(obs.LoggerConfig{Service: "tally"})
	m, _ := obs.NewMetrics(obs.MetricsConfig{Service: "tally"})
	r := tally.NewRunner(a, 20*time.Millisecond, m, log)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	require.NoError(t, r.Run(ctx))
	require.GreaterOrEqual(t, int(atomic.LoadInt32(&a.calls)), 3)
}

func TestRunner_ContinuesAfterError(t *testing.T) {
	a := &fakeAgg{err: errors.New("boom")}
	log := obs.NewLogger(obs.LoggerConfig{Service: "tally"})
	m, _ := obs.NewMetrics(obs.MetricsConfig{Service: "tally"})
	r := tally.NewRunner(a, 10*time.Millisecond, m, log)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	require.NoError(t, r.Run(ctx))
	require.GreaterOrEqual(t, int(atomic.LoadInt32(&a.calls)), 3,
		"runner must keep ticking despite errors")
}
