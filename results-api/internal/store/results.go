package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ChoiceCount struct {
	Choice string `json:"choice"`
	Count  int    `json:"count"`
}

type Aggregate struct {
	PollID    string        `json:"poll_id"`
	Results   []ChoiceCount `json:"results"`
	UpdatedAt time.Time     `json:"updated_at"`
}

type Results struct {
	pool *pgxpool.Pool
}

func NewResults(pool *pgxpool.Pool) *Results { return &Results{pool: pool} }

func (r *Results) Get(ctx context.Context, pollID string) (Aggregate, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT choice, count, updated_at FROM vote_results
		 WHERE poll_id = $1
		 ORDER BY count DESC, choice ASC`, pollID)
	if err != nil {
		return Aggregate{}, err
	}
	defer rows.Close()

	out := Aggregate{PollID: pollID}
	for rows.Next() {
		var c ChoiceCount
		var ts time.Time
		if err := rows.Scan(&c.Choice, &c.Count, &ts); err != nil {
			return Aggregate{}, err
		}
		out.Results = append(out.Results, c)
		if ts.After(out.UpdatedAt) {
			out.UpdatedAt = ts
		}
	}
	return out, rows.Err()
}

func (r *Results) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }
