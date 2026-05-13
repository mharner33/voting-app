package activity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
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

func TestTallyActivity_AgainstRealPostgres_MatchesBaselineAggregator(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	migDir, err := filepath.Abs("../../../migrations")
	require.NoError(t, err)

	pg, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("voting"),
		postgres.WithUsername("voting"),
		postgres.WithPassword("voting"),
		postgres.WithInitScripts(
			filepath.Join(migDir, "0001_create_votes.up.sql"),
			filepath.Join(migDir, "0002_create_vote_results.up.sql"),
		),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(context.Background()) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	pool, err := pgxpool.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	// Seed 3 tacos, 1 burrito.
	for _, v := range [][3]string{
		{"smoke", "tacos", "u1"},
		{"smoke", "tacos", "u2"},
		{"smoke", "tacos", "u3"},
		{"smoke", "burritos", "v1"},
	} {
		_, err := pool.Exec(ctx,
			`INSERT INTO votes (poll_id, choice, user_id) VALUES ($1,$2,$3)`,
			v[0], v[1], v[2])
		require.NoError(t, err)
	}

	a := &activity.TallyActivity{
		Agg: tally.NewAggregator(pool),
		Log: obs.NewLogger(obs.LoggerConfig{Service: "test", Env: "test", Version: "0"}),
	}

	env := newActivityEnv(t, a)

	val, err := env.ExecuteActivity(a.Run)
	require.NoError(t, err)

	var stats tally.Stats
	require.NoError(t, val.Get(&stats))
	require.Equal(t, 2, stats.RowsUpserted)
	require.Equal(t, 1, stats.PollsTouched)

	type row struct {
		choice string
		count  int
	}
	var rows []row
	q, err := pool.Query(ctx,
		`SELECT choice, count FROM vote_results WHERE poll_id='smoke' ORDER BY choice`)
	require.NoError(t, err)
	defer q.Close()
	for q.Next() {
		var r row
		require.NoError(t, q.Scan(&r.choice, &r.count))
		rows = append(rows, r)
	}
	require.Equal(t, []row{{"burritos", 1}, {"tacos", 3}}, rows)
}
