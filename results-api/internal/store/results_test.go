package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mharner33/voting-app/results-api/internal/store"
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

func TestResults_GetReturnsSortedAggregates(t *testing.T) {
	pool := startPG(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		INSERT INTO vote_results (poll_id, choice, count) VALUES
		('default','burritos',17),
		('default','tacos',42),
		('other','x',5)`)
	require.NoError(t, err)

	r := store.NewResults(pool)
	got, err := r.Get(ctx, "default")
	require.NoError(t, err)
	require.Len(t, got.Results, 2)
	require.Equal(t, "tacos", got.Results[0].Choice)
	require.Equal(t, 42, got.Results[0].Count)
	require.Equal(t, "burritos", got.Results[1].Choice)
	require.Equal(t, 17, got.Results[1].Count)
	require.False(t, got.UpdatedAt.IsZero())
}

func TestResults_GetEmptyPoll(t *testing.T) {
	pool := startPG(t)
	r := store.NewResults(pool)
	got, err := r.Get(context.Background(), "nonexistent")
	require.NoError(t, err)
	require.Empty(t, got.Results)
}
