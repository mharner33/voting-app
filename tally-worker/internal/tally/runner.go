package tally

import (
	"context"
	"time"

	"github.com/mharner33/voting-app/shared/obs"
)

type AggregatorRunner interface {
	Run(ctx context.Context) (Stats, error)
}

type Runner struct {
	agg      AggregatorRunner
	interval time.Duration
	metrics  *obs.Metrics
	log      *obs.Logger
}

func NewRunner(agg AggregatorRunner, interval time.Duration, m *obs.Metrics, l *obs.Logger) *Runner {
	return &Runner{agg: agg, interval: interval, metrics: m, log: l}
}

func (r *Runner) Run(ctx context.Context) error {
	t := time.NewTicker(r.interval)
	defer t.Stop()

	// One immediate tick on startup so the dashboard isn't empty for `interval` seconds.
	r.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			r.tick(ctx)
		}
	}
}

func (r *Runner) tick(ctx context.Context) {
	start := time.Now()
	stats, err := r.agg.Run(ctx)
	dur := time.Since(start)

	_ = r.metrics.Histogram("tally_duration_seconds", dur.Seconds(), nil)
	if err != nil {
		_ = r.metrics.Count("tally_runs_total", 1, []string{"status:error"})
		r.log.ErrorContext(ctx, "tally run failed", "err", err.Error(),
			"duration_ms", dur.Milliseconds())
		return
	}
	_ = r.metrics.Count("tally_runs_total", 1, []string{"status:success"})
	_ = r.metrics.Gauge("tally_last_success_timestamp", float64(time.Now().Unix()), nil)
	r.log.InfoContext(ctx, "tally run ok",
		"rows_upserted", stats.RowsUpserted,
		"polls_touched", stats.PollsTouched,
		"duration_ms", dur.Milliseconds())
}
