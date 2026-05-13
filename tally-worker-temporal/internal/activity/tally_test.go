package activity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"github.com/mharner33/voting-app/shared/obs"
	"github.com/mharner33/voting-app/tally-worker-temporal/internal/activity"
	"github.com/mharner33/voting-app/tally-worker/tally"
)

type fakeAgg struct {
	stats tally.Stats
	err   error
	calls int
}

func (f *fakeAgg) Run(ctx context.Context) (tally.Stats, error) {
	f.calls++
	return f.stats, f.err
}

func newActivityEnv(t *testing.T, a *activity.TallyActivity) *testsuite.TestActivityEnvironment {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(a.Run, activity.RegisterOptions())
	return env
}

func TestTallyActivity_DelegatesToAggregatorAndReturnsStats(t *testing.T) {
	agg := &fakeAgg{stats: tally.Stats{RowsUpserted: 4, PollsTouched: 2}}
	var buf bytes.Buffer
	logger := obs.NewLogger(obs.LoggerConfig{Service: "test", Env: "test", Version: "0", Writer: &buf})
	metrics, err := obs.NewMetrics(obs.MetricsConfig{Service: "test", Env: "test", Version: "0"})
	require.NoError(t, err)

	a := &activity.TallyActivity{Agg: agg, Log: logger, Metrics: metrics}
	env := newActivityEnv(t, a)

	val, err := env.ExecuteActivity(a.Run)
	require.NoError(t, err)

	var got tally.Stats
	require.NoError(t, val.Get(&got))
	require.Equal(t, tally.Stats{RowsUpserted: 4, PollsTouched: 2}, got)
	require.Equal(t, 1, agg.calls)

	var entry map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry))
	require.Equal(t, "tally activity ok", entry["msg"])
	require.EqualValues(t, 4, entry["rows_upserted"])
	require.EqualValues(t, 2, entry["polls_touched"])
}

func TestTallyActivity_PropagatesAggregatorError(t *testing.T) {
	agg := &fakeAgg{err: errors.New("boom")}
	a := &activity.TallyActivity{
		Agg: agg,
		Log: obs.NewLogger(obs.LoggerConfig{Service: "test", Env: "test", Version: "0"}),
	}
	// Metrics nil is OK — activity must tolerate nil metrics (no-op).
	env := newActivityEnv(t, a)

	_, err := env.ExecuteActivity(a.Run)
	require.Error(t, err)
	require.Equal(t, 1, agg.calls)
}
