package schedule

import (
	"context"
	"errors"
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
)

// ScheduleID is the fixed Schedule ID for the global tally Schedule.
const ScheduleID = "tally-all"

// WorkflowIDBase is the base WorkflowID used by the Schedule action.
// Temporal appends the scheduled timestamp on each firing, producing
// WorkflowIDs of the form "tally-all-<RFC3339>".
const WorkflowIDBase = "tally-all"

// EnsureTallySchedule creates the Schedule if it doesn't exist, or
// updates its interval if the existing Schedule's spec doesn't match.
// Idempotent: calling it on every worker startup is safe and cheap.
func EnsureTallySchedule(
	ctx context.Context,
	c client.Client,
	taskQueue string,
	workflowName string,
	interval time.Duration,
) error {
	sc := c.ScheduleClient()
	handle := sc.GetHandle(ctx, ScheduleID)

	desc, err := handle.Describe(ctx)
	if err != nil {
		var notFound *serviceerror.NotFound
		if !errors.As(err, &notFound) {
			return fmt.Errorf("describe schedule: %w", err)
		}
		_, createErr := sc.Create(ctx, client.ScheduleOptions{
			ID:   ScheduleID,
			Spec: client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: interval}}},
			Action: &client.ScheduleWorkflowAction{
				ID:                       WorkflowIDBase,
				Workflow:                 workflowName,
				TaskQueue:                taskQueue,
				WorkflowExecutionTimeout: 1 * time.Minute,
			},
			Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
		})
		if createErr != nil {
			return fmt.Errorf("create schedule: %w", createErr)
		}
		return nil
	}

	if currentInterval(desc) == interval {
		return nil
	}

	updateErr := handle.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: func(in client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			in.Description.Schedule.Spec = &client.ScheduleSpec{
				Intervals: []client.ScheduleIntervalSpec{{Every: interval}},
			}
			return &client.ScheduleUpdate{Schedule: &in.Description.Schedule}, nil
		},
	})
	if updateErr != nil {
		return fmt.Errorf("update schedule interval: %w", updateErr)
	}
	return nil
}

func currentInterval(desc *client.ScheduleDescription) time.Duration {
	if desc == nil || desc.Schedule.Spec == nil || len(desc.Schedule.Spec.Intervals) == 0 {
		return 0
	}
	return desc.Schedule.Spec.Intervals[0].Every
}
