package workflow

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"github.com/mharner33/voting-app/tally-worker/tally"
)

// TallyActivityName is the registered name of the tally activity.
// Workflows execute the activity by name to decouple from the activity's
// receiver type.
const TallyActivityName = "TallyActivity"

// TallyWorkflow runs one tally aggregation by invoking TallyActivity.
// It is intentionally tiny: the Schedule fires fresh executions every
// TALLY_INTERVAL, so the workflow never sleeps or continues-as-new.
func TallyWorkflow(ctx workflow.Context) (tally.Stats, error) {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    10 * time.Second,
			MaximumAttempts:    3,
		},
	})

	var stats tally.Stats
	err := workflow.ExecuteActivity(ctx, TallyActivityName).Get(ctx, &stats)
	return stats, err
}
