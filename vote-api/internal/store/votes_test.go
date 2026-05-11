package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mharner33/voting-app/vote-api/internal/store"
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

func TestVotes_InsertPersistsRow(t *testing.T) {
	pool := startPG(t)
	ctx := context.Background()

	s := store.NewVotes(pool)
	require.NoError(t, s.Insert(ctx, store.Vote{
		PollID: "default", Choice: "tacos", UserID: "u1",
	}))

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM votes WHERE poll_id=$1 AND choice=$2 AND user_id=$3`,
		"default", "tacos", "u1").Scan(&count))
	require.Equal(t, 1, count)
}

func TestVotes_DuplicateAllowed(t *testing.T) {
	pool := startPG(t)
	ctx := context.Background()

	s := store.NewVotes(pool)
	for i := 0; i < 3; i++ {
		require.NoError(t, s.Insert(ctx, store.Vote{PollID: "p", Choice: "a", UserID: "same"}))
	}

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM votes WHERE user_id=$1`, "same").Scan(&n))
	require.Equal(t, 3, n, "duplicates must be allowed — see arch §4.5")
}
