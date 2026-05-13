package tally

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
)

type Stats struct {
	RowsUpserted int
	PollsTouched int
}

type Aggregator struct {
	pool *pgxpool.Pool
}

func NewAggregator(pool *pgxpool.Pool) *Aggregator { return &Aggregator{pool: pool} }

func (a *Aggregator) Run(ctx context.Context) (Stats, error) {
	ctx, span := otel.Tracer("tally-worker").Start(ctx, "tally.run")
	defer span.End()

	var stats Stats
	err := pgx.BeginFunc(ctx, a.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT poll_id, choice, COUNT(*)::int FROM votes GROUP BY poll_id, choice`)
		if err != nil {
			return err
		}
		type agg struct {
			poll, choice string
			count        int
		}
		var batch []agg
		polls := map[string]struct{}{}
		for rows.Next() {
			var a agg
			if err := rows.Scan(&a.poll, &a.choice, &a.count); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, a)
			polls[a.poll] = struct{}{}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, a := range batch {
			_, err := tx.Exec(ctx, `
				INSERT INTO vote_results (poll_id, choice, count, updated_at)
				VALUES ($1, $2, $3, now())
				ON CONFLICT (poll_id, choice)
				DO UPDATE SET count = EXCLUDED.count, updated_at = now()`,
				a.poll, a.choice, a.count)
			if err != nil {
				return err
			}
		}
		stats.RowsUpserted = len(batch)
		stats.PollsTouched = len(polls)
		return nil
	})
	if err != nil {
		span.RecordError(err)
	}
	return stats, err
}

func (a *Aggregator) Ping(ctx context.Context) error { return a.pool.Ping(ctx) }
