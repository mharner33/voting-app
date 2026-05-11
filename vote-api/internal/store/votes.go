package store

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Vote struct {
	PollID string
	Choice string
	UserID string // may be empty
}

type Votes struct {
	pool *pgxpool.Pool
}

func NewVotes(pool *pgxpool.Pool) *Votes { return &Votes{pool: pool} }

func (v *Votes) Insert(ctx context.Context, vote Vote) error {
	var userID sql.NullString
	if vote.UserID != "" {
		userID = sql.NullString{String: vote.UserID, Valid: true}
	}
	_, err := v.pool.Exec(ctx,
		`INSERT INTO votes (poll_id, choice, user_id) VALUES ($1, $2, $3)`,
		vote.PollID, vote.Choice, userID,
	)
	return err
}

func (v *Votes) Ping(ctx context.Context) error { return v.pool.Ping(ctx) }
