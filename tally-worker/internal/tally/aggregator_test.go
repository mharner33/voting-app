package tally_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mharner33/voting-app/tally-worker/internal/tally"
)

func startPG(t *testing.T) *pgxpool.Pool {
	t.Helper()
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
	return pool
}

func insertVotes(t *testing.T, pool *pgxpool.Pool, rows ...[3]string) {
	t.Helper()
	for _, r := range rows {
		_, err := pool.Exec(context.Background(),
			`INSERT INTO votes (poll_id, choice, user_id) VALUES ($1,$2,$3)`,
			r[0], r[1], r[2])
		require.NoError(t, err)
	}
}

func TestAggregator_UpsertsCountsPerPollChoice(t *testing.T) {
	pool := startPG(t)
	insertVotes(t, pool,
		[3]string{"default", "tacos", "u1"},
		[3]string{"default", "tacos", "u2"},
		[3]string{"default", "burritos", "u3"},
		[3]string{"polls/cat", "yes", "x"},
	)

	a := tally.NewAggregator(pool)
	stats, err := a.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, stats.RowsUpserted)
	require.Equal(t, 2, stats.PollsTouched)

	type res struct {
		poll, choice string
		count        int
	}
	var got []res
	rows, err := pool.Query(context.Background(),
		`SELECT poll_id, choice, count FROM vote_results ORDER BY poll_id, choice`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var r res
		require.NoError(t, rows.Scan(&r.poll, &r.choice, &r.count))
		got = append(got, r)
	}
	require.Equal(t, []res{
		{"default", "burritos", 1},
		{"default", "tacos", 2},
		{"polls/cat", "yes", 1},
	}, got)
}

func TestAggregator_IsIdempotent(t *testing.T) {
	pool := startPG(t)
	insertVotes(t, pool, [3]string{"p", "a", "u1"}, [3]string{"p", "a", "u2"})

	a := tally.NewAggregator(pool)
	for i := 0; i < 3; i++ {
		_, err := a.Run(context.Background())
		require.NoError(t, err)
	}

	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count FROM vote_results WHERE poll_id='p' AND choice='a'`).Scan(&n))
	require.Equal(t, 2, n)
}

func TestAggregator_OverwritesStaleCounts(t *testing.T) {
	pool := startPG(t)
	// Pre-seed a stale row in vote_results
	_, err := pool.Exec(context.Background(),
		`INSERT INTO vote_results (poll_id, choice, count) VALUES ('p','a', 99)`)
	require.NoError(t, err)
	// Reality: only 2 votes
	insertVotes(t, pool, [3]string{"p", "a", "u1"}, [3]string{"p", "a", "u2"})

	_, err = tally.NewAggregator(pool).Run(context.Background())
	require.NoError(t, err)

	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count FROM vote_results WHERE poll_id='p' AND choice='a'`).Scan(&n))
	require.Equal(t, 2, n, "upsert must overwrite stale count, not add to it")
}
