package activity

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	temporalactivity "go.temporal.io/sdk/activity"

	"github.com/mharner33/voting-app/shared/obs"
	"github.com/mharner33/voting-app/tally-worker/tally"
)

// Aggregator is the interface TallyActivity needs from the underlying
// aggregator. Defined here as an interface (rather than depending directly
// on *tally.Aggregator) so unit tests can substitute a fake.
type Aggregator interface {
	Run(ctx context.Context) (tally.Stats, error)
}

// TallyActivity wraps the existing tally.Aggregator so it can be invoked
// as a Temporal Activity. It is registered under the name "TallyActivity"
// (kept in lockstep with workflow.TallyActivityName).
//
// Metrics are emitted *per attempt* (matching baseline semantics), so a
// retried activity counts as N runs in `tally_runs_total`. Per-workflow-
// completion counts come from the Temporal server's own metrics, scraped
// into Datadog via OpenMetrics (`temporal.workflow_*` series).
type TallyActivity struct {
	Agg     Aggregator
	Log     *obs.Logger
	Metrics *obs.Metrics
}

// RegisterOptions returns the activity registration options used by both
// the worker main and the unit tests, keeping the registered name in one
// place.
func RegisterOptions() temporalactivity.RegisterOptions {
	return temporalactivity.RegisterOptions{Name: "TallyActivity"}
}

func (a *TallyActivity) Run(ctx context.Context) (tally.Stats, error) {
	ctx, span := otel.Tracer("tally-worker-temporal").Start(ctx, "tally.activity")
	defer span.End()

	start := time.Now()
	stats, err := a.Agg.Run(ctx)
	dur := time.Since(start)

	a.recordMetrics(dur, err)

	info := temporalactivity.GetInfo(ctx)
	logArgs := []any{
		"duration_ms", dur.Milliseconds(),
		"temporal.workflow_id", info.WorkflowExecution.ID,
		"temporal.run_id", info.WorkflowExecution.RunID,
		"temporal.activity_id", info.ActivityID,
	}

	if err != nil {
		span.RecordError(err)
		a.Log.ErrorContext(ctx, "tally activity failed",
			append(logArgs, "err", err.Error())...)
		return stats, err
	}

	a.Log.InfoContext(ctx, "tally activity ok",
		append(logArgs,
			"rows_upserted", stats.RowsUpserted,
			"polls_touched", stats.PollsTouched,
		)...)
	return stats, nil
}

func (a *TallyActivity) recordMetrics(dur time.Duration, err error) {
	if a.Metrics == nil {
		return
	}
	_ = a.Metrics.Histogram("tally_duration_seconds", dur.Seconds(), nil)
	if err != nil {
		_ = a.Metrics.Count("tally_runs_total", 1, []string{"status:error"})
		return
	}
	_ = a.Metrics.Count("tally_runs_total", 1, []string{"status:success"})
	_ = a.Metrics.Gauge("tally_last_success_timestamp", float64(time.Now().Unix()), nil)
}
