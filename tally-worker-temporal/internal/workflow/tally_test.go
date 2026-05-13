package workflow_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"

	"github.com/mharner33/voting-app/tally-worker-temporal/internal/workflow"
	"github.com/mharner33/voting-app/tally-worker/tally"
)

// stubTallyActivity is a typed stub registered under TallyActivityName so that
// the testsuite can resolve it when OnActivity is called with the string name.
// It accepts a context so the testsuite includes ctx in mock.MethodCalled args.
func stubTallyActivity(_ context.Context) (tally.Stats, error) {
	return tally.Stats{}, nil
}

func registerStub(env *testsuite.TestWorkflowEnvironment) {
	env.RegisterActivityWithOptions(stubTallyActivity, activity.RegisterOptions{
		Name: workflow.TallyActivityName,
	})
}

func TestTallyWorkflow_CallsActivityAndReturnsStats(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerStub(env)

	want := tally.Stats{RowsUpserted: 3, PollsTouched: 2}
	env.OnActivity(workflow.TallyActivityName, mock.Anything).Return(want, nil)

	env.ExecuteWorkflow(workflow.TallyWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var got tally.Stats
	require.NoError(t, env.GetWorkflowResult(&got))
	require.Equal(t, want, got)
}

func TestTallyWorkflow_PropagatesActivityError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerStub(env)

	env.OnActivity(workflow.TallyActivityName, mock.Anything).Return(tally.Stats{}, errors.New("db down"))

	env.ExecuteWorkflow(workflow.TallyWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}

func TestTallyWorkflow_RetriesActivityUpToMaxAttempts(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerStub(env)

	calls := 0
	env.OnActivity(workflow.TallyActivityName, mock.Anything).Return(func(ctx context.Context) (tally.Stats, error) {
		calls++
		return tally.Stats{}, errors.New("transient")
	})

	env.ExecuteWorkflow(workflow.TallyWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Equal(t, 3, calls, "RetryPolicy.MaximumAttempts is 3")
}
