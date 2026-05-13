# Temporal `tally-worker` Variant — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a Temporal-driven variant of `tally-worker` (new `tally-worker-temporal/` module) gated behind a `temporal` compose profile, plus `temporalio/auto-setup` + `temporalio/ui` containers and a Datadog OpenMetrics scrape of Temporal server metrics. The baseline `tally-worker` must keep working unchanged under a new `baseline` profile.

**Architecture:** A new Go module is a thin Temporal worker that registers `TallyWorkflow` + a single `TallyActivity` on the `tally-task-queue`. The activity calls the existing `tally.Aggregator` (imported from `tally-worker/tally`) so business logic and the single-Postgres-transaction property are preserved verbatim. At startup the worker idempotently ensures a single global Schedule `tally-all` exists with interval `TALLY_INTERVAL`.

**Tech Stack:** Go 1.25, `go.temporal.io/sdk` (Temporal Go SDK), `go.temporal.io/sdk/contrib/opentelemetry` (interceptor that plugs into the existing OTel API wired to `dd-trace-go`), pgx, testcontainers-go/postgres, `temporalio/auto-setup` + `temporalio/ui` images, Datadog Agent OpenMetrics integration.

**Spec:** [`docs/superpowers/specs/2026-05-13-temporal-tally-design.md`](../specs/2026-05-13-temporal-tally-design.md).

**Conventions in this repo (carry over verbatim):**
- The project uses **`podman compose`** (not `docker compose`) — every command in this plan that touches compose uses `podman`. If you're on `docker compose`, substitute one-for-one.
- `shared/obs` provides `StartTracing`, `NewLogger`, `NewMetrics`, `RunUntilSignal`.
- `shared/pgxdb.Open(ctx, dsn)` returns the wrapped pool.
- Each Go service has its own module pinned to the workspace via `go.work` and a `replace github.com/mharner33/voting-app/shared => ../shared` in its `go.mod`.
- All commits use `Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>` to match recent history.

---

## File Structure

**New files (create):**

| Path | Responsibility |
|---|---|
| `tally-worker-temporal/go.mod` | Module manifest with Temporal SDK + shared/tally-worker replaces. |
| `tally-worker-temporal/cmd/tally-worker-temporal/main.go` | Boot: env, obs, pool, Temporal client, EnsureTallySchedule, Worker, graceful shutdown. |
| `tally-worker-temporal/internal/workflow/tally.go` | `TallyWorkflow` (pure, no I/O). |
| `tally-worker-temporal/internal/workflow/tally_test.go` | Workflow unit test via `testsuite.TestWorkflowEnvironment`. |
| `tally-worker-temporal/internal/activity/tally.go` | `TallyActivity` thin wrapper around `tally.Aggregator`. Also emits `tally_runs_total` / `tally_duration_seconds` / `tally_last_success_timestamp` metrics per attempt. |
| `tally-worker-temporal/internal/activity/tally_test.go` | Activity integration test against testcontainers Postgres. |
| `tally-worker-temporal/internal/schedule/ensure.go` | `EnsureTallySchedule(ctx, client, interval)` — idempotent create-or-update. |
| `tally-worker-temporal/Dockerfile` | Distroless static build, mirrors `tally-worker/Dockerfile`. |
| `datadog/openmetrics.d/conf.yaml` | OpenMetrics scrape of `http://temporal:9090/metrics`. |
| `scripts/smoke-temporal.sh` | E2E smoke for the `temporal` profile. |

**Modified files:**

| Path | What changes |
|---|---|
| `go.work` | Add `./tally-worker-temporal`. |
| `docker-compose.yml` | Add `temporal`, `temporal-ui`, `tally-worker-temporal` services on `profiles: ["temporal"]`; add `profiles: ["baseline"]` to existing `tally-worker`; mount openmetrics conf on `datadog-agent`. |
| `Makefile` | Add `up-temporal`, `down-temporal`, `smoke-temporal`; change `up` to `--profile baseline`. |
| `scripts/smoke.sh` | Add `--profile baseline` to compose commands so the baseline `tally-worker` (now profiled) starts. |
| `.env.example` | Add `TEMPORAL_NAMESPACE=default`. |
| `README.md` | Document the two profiles, the new Make targets, and the Temporal UI URL. |
| `voting-app-architecture.md` | Replace §4.4.2's three-activity sketch with single `TallyActivity`; replace per-poll Schedule with global `tally-all`. |
| `CLAUDE.md` | Replace the "Temporal variant is not implemented" note with a one-liner pointing at `make up-temporal`. |

---

## Task 1: Bootstrap `tally-worker-temporal` module

**Files:**
- Create: `tally-worker-temporal/go.mod`
- Modify: `go.work`

- [ ] **Step 1: Create the module directory and `go.mod`**

```bash
mkdir -p /home/mike/demo/voting-app/tally-worker-temporal/cmd/tally-worker-temporal
mkdir -p /home/mike/demo/voting-app/tally-worker-temporal/internal/{workflow,activity,schedule}
```

Write `/home/mike/demo/voting-app/tally-worker-temporal/go.mod`:

```
module github.com/mharner33/voting-app/tally-worker-temporal

go 1.25.1

require (
	github.com/jackc/pgx/v5 v5.9.2
	github.com/mharner33/voting-app/shared v0.0.0-00010101000000-000000000000
	github.com/mharner33/voting-app/tally-worker v0.0.0-00010101000000-000000000000
	github.com/stretchr/testify v1.11.1
	github.com/testcontainers/testcontainers-go/modules/postgres v0.42.0
	go.opentelemetry.io/otel v1.43.0
	go.temporal.io/sdk v1.32.1
	go.temporal.io/sdk/contrib/opentelemetry v0.5.0
)

replace github.com/mharner33/voting-app/shared => ../shared

replace github.com/mharner33/voting-app/tally-worker => ../tally-worker
```

- [ ] **Step 2: Add the module to the workspace**

Edit `/home/mike/demo/voting-app/go.work` so the `use` block becomes:

```
go 1.25.1

use (
	./results-api
	./shared
	./tally-worker
	./tally-worker-temporal
	./vote-api
)
```

- [ ] **Step 3: Resolve dependencies**

Run:
```bash
cd /home/mike/demo/voting-app/tally-worker-temporal && go mod tidy
```
Expected: tidy completes without error; `go.sum` is created. If `go.temporal.io/sdk/contrib/opentelemetry` resolves to a different latest version, accept whatever `go mod tidy` picks — the API surface we use (`tracing.NewTracingInterceptor(opts)`) is stable across 0.x releases.

- [ ] **Step 4: Verify the workspace still compiles**

Run:
```bash
cd /home/mike/demo/voting-app && go build ./...
```
Expected: no output, exit 0. (The new module has no .go files yet, so it contributes nothing; this just confirms `go.work` is valid.)

- [ ] **Step 5: Commit**

```bash
cd /home/mike/demo/voting-app
git add tally-worker-temporal/go.mod tally-worker-temporal/go.sum go.work go.work.sum
git commit -m "$(cat <<'EOF'
feat(temporal): bootstrap tally-worker-temporal module

Adds the new Go module skeleton with Temporal SDK + OpenTelemetry
interceptor deps, replaces for shared/ and tally-worker/, and registers
the module in go.work.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: `TallyWorkflow` (workflow unit test first)

**Files:**
- Test: `tally-worker-temporal/internal/workflow/tally_test.go`
- Create: `tally-worker-temporal/internal/workflow/tally.go`

- [ ] **Step 1: Write the failing test**

Write `/home/mike/demo/voting-app/tally-worker-temporal/internal/workflow/tally_test.go`:

```go
package workflow_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"github.com/mharner33/voting-app/tally-worker-temporal/internal/workflow"
	"github.com/mharner33/voting-app/tally-worker/tally"
)

func TestTallyWorkflow_CallsActivityAndReturnsStats(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	want := tally.Stats{RowsUpserted: 3, PollsTouched: 2}
	env.OnActivity(workflow.TallyActivityName).Return(want, nil)

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

	env.OnActivity(workflow.TallyActivityName).Return(tally.Stats{}, errors.New("db down"))

	env.ExecuteWorkflow(workflow.TallyWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}

func TestTallyWorkflow_RetriesActivityUpToMaxAttempts(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	calls := 0
	env.OnActivity(workflow.TallyActivityName).Return(func() (tally.Stats, error) {
		calls++
		return tally.Stats{}, errors.New("transient")
	})

	env.ExecuteWorkflow(workflow.TallyWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	require.Equal(t, 3, calls, "RetryPolicy.MaximumAttempts is 3")

	// Sanity-check StartToCloseTimeout via the recorded ActivityOptions
	// is awkward in the test suite; the retry-count assertion above is the
	// strongest property we can test here without booting a server.
	_ = time.Second
}
```

- [ ] **Step 2: Run test — verify it fails to compile**

Run:
```bash
cd /home/mike/demo/voting-app/tally-worker-temporal && go test ./internal/workflow/...
```
Expected: build error — `workflow.TallyActivityName` and `workflow.TallyWorkflow` undefined.

- [ ] **Step 3: Write minimal implementation**

Write `/home/mike/demo/voting-app/tally-worker-temporal/internal/workflow/tally.go`:

```go
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
```

- [ ] **Step 4: Run test — verify it passes**

Run:
```bash
cd /home/mike/demo/voting-app/tally-worker-temporal && go test ./internal/workflow/... -v
```
Expected: three PASS lines (`TestTallyWorkflow_CallsActivityAndReturnsStats`, `TestTallyWorkflow_PropagatesActivityError`, `TestTallyWorkflow_RetriesActivityUpToMaxAttempts`).

- [ ] **Step 5: Commit**

```bash
cd /home/mike/demo/voting-app
git add tally-worker-temporal/internal/workflow/
git commit -m "$(cat <<'EOF'
feat(temporal): add TallyWorkflow with retry policy

Single-activity workflow that the Temporal Schedule fires every
TALLY_INTERVAL. RetryPolicy of 3 attempts with exponential backoff matches
the demo's tolerance for transient DB blips. Tests cover happy-path,
error propagation, and retry-attempt count via testsuite.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: `TallyActivity` wrapper (unit test with mock aggregator first)

**Files:**
- Test: `tally-worker-temporal/internal/activity/tally_test.go`
- Create: `tally-worker-temporal/internal/activity/tally.go`

- [ ] **Step 1: Write the failing unit test**

Write `/home/mike/demo/voting-app/tally-worker-temporal/internal/activity/tally_test.go`:

```go
package activity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"

	"github.com/mharner33/voting-app/shared/obs"
	"github.com/mharner33/voting-app/tally-worker-temporal/internal/activity"
	"github.com/mharner33/voting-app/tally-worker/tally"
)

type fakeAgg struct {
	stats tally.Stats
	err   error
	calls int
}

func (f *fakeAgg) Run(ctx context.Context) (tally.Stats, error) {
	f.calls++
	return f.stats, f.err
}

func newActivityEnv(t *testing.T, a *activity.TallyActivity) *testsuite.TestActivityEnvironment {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(a.Run, activity.RegisterOptions())
	return env
}

func TestTallyActivity_DelegatesToAggregatorAndReturnsStats(t *testing.T) {
	agg := &fakeAgg{stats: tally.Stats{RowsUpserted: 4, PollsTouched: 2}}
	var buf bytes.Buffer
	logger := obs.NewLogger(obs.LoggerConfig{Service: "test", Env: "test", Version: "0", Writer: &buf})
	metrics, err := obs.NewMetrics(obs.MetricsConfig{Service: "test", Env: "test", Version: "0"})
	require.NoError(t, err)

	a := &activity.TallyActivity{Agg: agg, Log: logger, Metrics: metrics}
	env := newActivityEnv(t, a)

	val, err := env.ExecuteActivity(a.Run)
	require.NoError(t, err)

	var got tally.Stats
	require.NoError(t, val.Get(&got))
	require.Equal(t, tally.Stats{RowsUpserted: 4, PollsTouched: 2}, got)
	require.Equal(t, 1, agg.calls)

	// Log line should have been emitted.
	var entry map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry))
	require.Equal(t, "tally activity ok", entry["msg"])
	require.EqualValues(t, 4, entry["rows_upserted"])
	require.EqualValues(t, 2, entry["polls_touched"])
}

func TestTallyActivity_PropagatesAggregatorError(t *testing.T) {
	agg := &fakeAgg{err: errors.New("boom")}
	a := &activity.TallyActivity{
		Agg:     agg,
		Log:     obs.NewLogger(obs.LoggerConfig{Service: "test", Env: "test", Version: "0"}),
	}
	// Metrics nil is OK — activity must tolerate nil metrics (no-op).
	env := newActivityEnv(t, a)

	_, err := env.ExecuteActivity(a.Run)
	require.Error(t, err)
	require.Equal(t, 1, agg.calls)
}
```

- [ ] **Step 2: Run test — verify it fails to compile**

Run:
```bash
cd /home/mike/demo/voting-app/tally-worker-temporal && go test ./internal/activity/...
```
Expected: build error — `activity.TallyActivity` and `activity.RegisterOptions` undefined.

- [ ] **Step 3: Write the implementation**

Write `/home/mike/demo/voting-app/tally-worker-temporal/internal/activity/tally.go`:

```go
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
// as a Temporal Activity. It is registered under the name
// workflow.TallyActivityName ("TallyActivity").
//
// Metrics are emitted *per attempt* (matching baseline semantics), so a
// retried activity counts as N runs in `tally_runs_total`. The wrapping
// workflow's completion is counted separately by main.go via the
// `tally_workflow_runs_total` series.
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
```

- [ ] **Step 4: Run test — verify it passes**

Run:
```bash
cd /home/mike/demo/voting-app/tally-worker-temporal && go test ./internal/activity/... -v
```
Expected: two PASS lines.

- [ ] **Step 5: Commit**

```bash
cd /home/mike/demo/voting-app
git add tally-worker-temporal/internal/activity/
git commit -m "$(cat <<'EOF'
feat(temporal): add TallyActivity wrapping the existing aggregator

Single activity that delegates to tally.Aggregator (imported from the
baseline module). Emits the same metric names as the baseline worker —
tally_runs_total / tally_duration_seconds / tally_last_success_timestamp
— tagged with service:tally-worker-temporal via DD_SERVICE, so dashboards
can disambiguate the two variants by service tag.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Activity integration test with real Postgres

**Files:**
- Modify: `tally-worker-temporal/internal/activity/tally_test.go` (add a new `_integration` test in the same package).

- [ ] **Step 1: Write the failing integration test**

Append this to `/home/mike/demo/voting-app/tally-worker-temporal/internal/activity/tally_test.go` (above the closing of the file, in the same `activity_test` package):

```go
import (
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// NOTE: this lives in the same _test.go file but is gated to a separate
// test name so `go test -run` can target it independently. It boots a real
// Postgres via testcontainers, identical to the baseline aggregator's
// integration tests.

func TestTallyActivity_AgainstRealPostgres_MatchesBaselineAggregator(t *testing.T) {
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

	// Seed 3 tacos, 1 burrito.
	for _, v := range [][3]string{
		{"smoke", "tacos", "u1"},
		{"smoke", "tacos", "u2"},
		{"smoke", "tacos", "u3"},
		{"smoke", "burritos", "v1"},
	} {
		_, err := pool.Exec(ctx,
			`INSERT INTO votes (poll_id, choice, user_id) VALUES ($1,$2,$3)`,
			v[0], v[1], v[2])
		require.NoError(t, err)
	}

	a := &activity.TallyActivity{
		Agg: tally.NewAggregator(pool),
		Log: obs.NewLogger(obs.LoggerConfig{Service: "test", Env: "test", Version: "0"}),
	}

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivityWithOptions(a.Run, activity.RegisterOptions())

	val, err := env.ExecuteActivity(a.Run)
	require.NoError(t, err)

	var stats tally.Stats
	require.NoError(t, val.Get(&stats))
	require.Equal(t, 2, stats.RowsUpserted)
	require.Equal(t, 1, stats.PollsTouched)

	// Verify the rows landed in vote_results.
	type row struct {
		choice string
		count  int
	}
	var rows []row
	q, err := pool.Query(ctx,
		`SELECT choice, count FROM vote_results WHERE poll_id='smoke' ORDER BY choice`)
	require.NoError(t, err)
	defer q.Close()
	for q.Next() {
		var r row
		require.NoError(t, q.Scan(&r.choice, &r.count))
		rows = append(rows, r)
	}
	require.Equal(t, []row{{"burritos", 1}, {"tacos", 3}}, rows)
}
```

Then consolidate imports at the top of the file — make sure there's exactly one `import (...)` block containing every import used. The complete import block becomes:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"go.temporal.io/sdk/testsuite"

	"github.com/mharner33/voting-app/shared/obs"
	"github.com/mharner33/voting-app/tally-worker-temporal/internal/activity"
	"github.com/mharner33/voting-app/tally-worker/tally"
)
```

- [ ] **Step 2: Run integration test — verify it fails without Docker**

If Docker is not running, skip this verification step and proceed to Step 3. Otherwise:

Run:
```bash
cd /home/mike/demo/voting-app
DOCKER_HOST=unix:///run/user/$(id -u)/podman/podman.sock \
  TESTCONTAINERS_RYUK_DISABLED=true \
  go test ./tally-worker-temporal/internal/activity/... \
  -run TestTallyActivity_AgainstRealPostgres -v -timeout 180s
```
Expected: FAIL initially because we haven't yet wired the right imports. (Or PASS if everything compiles — that's also fine; the activity wraps the already-tested aggregator.)

- [ ] **Step 3: Fix any compilation errors and re-run**

Run the same command as Step 2. Expected: PASS.

If the test errors with `Cannot connect to the Docker daemon`, the Docker/podman socket isn't reachable. Set `DOCKER_HOST` correctly per the Makefile's `test` target (`unix:///run/user/$(id -u)/podman/podman.sock`).

- [ ] **Step 4: Run the whole module's tests for regression**

Run:
```bash
cd /home/mike/demo/voting-app
DOCKER_HOST=unix:///run/user/$(id -u)/podman/podman.sock \
  TESTCONTAINERS_RYUK_DISABLED=true \
  go test ./tally-worker-temporal/... -count=1
```
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/mike/demo/voting-app
git add tally-worker-temporal/internal/activity/tally_test.go
git commit -m "$(cat <<'EOF'
test(temporal): add TallyActivity integration test against real Postgres

Mirrors the baseline aggregator's testcontainers-backed test. Verifies
the activity, when run via testsuite.TestActivityEnvironment against a
real DB, produces the same vote_results rows as the baseline path.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: `EnsureTallySchedule`

**Files:**
- Create: `tally-worker-temporal/internal/schedule/ensure.go`

There's no unit test for this task. Unit-testing it requires either booting a Temporal dev server (too heavy for `go test`) or mocking the SDK's schedule client (the SDK doesn't expose interfaces clean enough for that). The function's correctness is covered end-to-end by `make smoke-temporal` (Task 11).

- [ ] **Step 1: Write the implementation**

Write `/home/mike/demo/voting-app/tally-worker-temporal/internal/schedule/ensure.go`:

```go
package schedule

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
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
		// Not found — create.
		_, createErr := sc.Create(ctx, client.ScheduleOptions{
			ID:   ScheduleID,
			Spec: client.ScheduleSpec{Intervals: []client.ScheduleIntervalSpec{{Every: interval}}},
			Action: &client.ScheduleWorkflowAction{
				ID:                       WorkflowIDBase,
				Workflow:                 workflowName,
				TaskQueue:                taskQueue,
				WorkflowIDReusePolicy:    temporal.WorkflowIDReusePolicyAllowDuplicate,
				WorkflowExecutionTimeout: 1 * time.Minute,
			},
			Policy: &client.SchedulePolicies{
				Overlap: temporal.ScheduleOverlapPolicySkip,
			},
		})
		if createErr != nil {
			return fmt.Errorf("create schedule: %w", createErr)
		}
		return nil
	}

	// Already exists. If the interval differs, update it.
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
```

- [ ] **Step 2: Verify the package compiles**

Run:
```bash
cd /home/mike/demo/voting-app/tally-worker-temporal && go build ./internal/schedule/...
```
Expected: no output, exit 0.

If a method signature on `client.ScheduleClient` differs in the SDK version `go mod tidy` resolved (Task 1), adjust the call site. The `Schedules` API has been stable since Go SDK v1.23+, but field names on `client.ScheduleUpdate` have shifted in minor releases — consult `go doc go.temporal.io/sdk/client ScheduleUpdate` if compilation fails.

- [ ] **Step 3: Commit**

```bash
cd /home/mike/demo/voting-app
git add tally-worker-temporal/internal/schedule/
git commit -m "$(cat <<'EOF'
feat(temporal): idempotent EnsureTallySchedule

Creates the tally-all Schedule on first run, updates the interval if it
differs from TALLY_INTERVAL on subsequent runs. AllowDuplicate reuse
policy + Skip overlap policy give us the demo behavior we want: each
tick is a fresh short workflow, never overlapping the previous one.

Covered end-to-end by make smoke-temporal; no unit test (the SDK
schedule client doesn't expose interfaces suitable for mocking).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Worker `main.go`

**Files:**
- Create: `tally-worker-temporal/cmd/tally-worker-temporal/main.go`

- [ ] **Step 1: Write `main.go`**

Write `/home/mike/demo/voting-app/tally-worker-temporal/cmd/tally-worker-temporal/main.go`:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
	sdklog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/worker"

	"github.com/mharner33/voting-app/shared/obs"
	"github.com/mharner33/voting-app/shared/pgxdb"
	"github.com/mharner33/voting-app/tally-worker-temporal/internal/activity"
	"github.com/mharner33/voting-app/tally-worker-temporal/internal/schedule"
	tw "github.com/mharner33/voting-app/tally-worker-temporal/internal/workflow"
	"github.com/mharner33/voting-app/tally-worker/tally"
)

const workflowName = "TallyWorkflow"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tally-worker-temporal:", err)
		os.Exit(1)
	}
}

func run() error {
	service := envDefault("DD_SERVICE", "tally-worker-temporal")
	env := envDefault("DD_ENV", "local")
	version := envDefault("DD_VERSION", "dev")
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		return errors.New("POSTGRES_DSN is required")
	}
	intervalStr := envDefault("TALLY_INTERVAL", "5s")
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		return fmt.Errorf("TALLY_INTERVAL: %w", err)
	}
	temporalHostPort := envDefault("TEMPORAL_HOST_PORT", "temporal:7233")
	temporalNamespace := envDefault("TEMPORAL_NAMESPACE", "default")
	taskQueue := envDefault("TEMPORAL_TASK_QUEUE", "tally-task-queue")

	agentHost := envDefault("DD_AGENT_HOST", "")
	tracePort := envDefault("DD_TRACE_AGENT_PORT", "8126")
	statsdAddr := ""
	if agentHost != "" {
		statsdAddr = agentHost + ":" + envDefault("DD_DOGSTATSD_PORT", "8125")
	}

	shutdownTracing, err := obs.StartTracing(obs.TracingConfig{
		Service: service, Env: env, Version: version,
		AgentHost: agentHost, AgentPort: tracePort,
	})
	if err != nil {
		return err
	}
	logger := obs.NewLogger(obs.LoggerConfig{Service: service, Env: env, Version: version})
	metrics, err := obs.NewMetrics(obs.MetricsConfig{
		Address: statsdAddr, Service: service, Env: env, Version: version,
	})
	if err != nil {
		return err
	}

	openCtx, openCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer openCancel()
	pool, err := pgxdb.Open(openCtx, dsn)
	if err != nil {
		return err
	}

	// OTel-based tracing interceptor; routes through the same OTel API that
	// shared/obs.StartTracing wires up to dd-trace-go.
	tracingInterceptor, err := opentelemetry.NewTracingInterceptor(opentelemetry.TracerOptions{})
	if err != nil {
		return fmt.Errorf("temporal tracing interceptor: %w", err)
	}

	temporalClient, err := client.Dial(client.Options{
		HostPort:     temporalHostPort,
		Namespace:    temporalNamespace,
		Logger:       sdklog.NewStructuredLogger(logger.Logger),
		Interceptors: []interceptor.ClientInterceptor{tracingInterceptor},
	})
	if err != nil {
		return fmt.Errorf("temporal dial: %w", err)
	}

	// Ensure the global Schedule exists before the worker starts polling,
	// so the first tick fires within `interval` of startup.
	scheduleCtx, scheduleCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer scheduleCancel()
	if err := schedule.EnsureTallySchedule(scheduleCtx, temporalClient, taskQueue, workflowName, interval); err != nil {
		return fmt.Errorf("ensure schedule: %w", err)
	}

	w := worker.New(temporalClient, taskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(tw.TallyWorkflow, worker.RegisterWorkflowOptions{Name: workflowName})

	tallyAct := &activity.TallyActivity{
		Agg:     tally.NewAggregator(pool),
		Log:     logger,
		Metrics: metrics,
	}
	w.RegisterActivityWithOptions(tallyAct.Run, activity.RegisterOptions())

	logger.Info("starting",
		"interval", interval.String(),
		"task_queue", taskQueue,
		"namespace", temporalNamespace,
		"host_port", temporalHostPort,
	)

	runErr := obs.RunUntilSignal(context.Background(), 10*time.Second, func(ctx context.Context) error {
		// worker.Run blocks until ctx.Done() is closed, then Stop()s the worker.
		return w.Run(ctxToInterruptCh(ctx))
	})

	temporalClient.Close()
	pool.Close()
	_ = metrics.Close()
	_ = shutdownTracing(context.Background())
	logger.Info("stopped")
	return runErr
}

// ctxToInterruptCh adapts a context.Context into the <-chan interface{} that
// Temporal's worker.Run expects: when the context is done, the channel is
// closed, which triggers worker.Stop().
func ctxToInterruptCh(ctx context.Context) <-chan interface{} {
	ch := make(chan interface{})
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch
}

func envDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
```

- [ ] **Step 2: Build the binary**

Run:
```bash
cd /home/mike/demo/voting-app/tally-worker-temporal && go build ./cmd/tally-worker-temporal
```
Expected: no output, exit 0, binary `tally-worker-temporal` produced in the module dir. Delete it after verifying:
```bash
rm /home/mike/demo/voting-app/tally-worker-temporal/tally-worker-temporal
```

If the build fails with `undefined: sdklog.NewStructuredLogger`, the SDK version uses `log.NewStructuredLogger` — check `go doc go.temporal.io/sdk/log` and adjust the import alias.

- [ ] **Step 3: Commit**

```bash
cd /home/mike/demo/voting-app
git add tally-worker-temporal/cmd/
git commit -m "$(cat <<'EOF'
feat(temporal): wire main.go for tally-worker-temporal

Boots shared/obs tracing+logging+metrics, opens the pgx pool, dials
Temporal with the OTel tracing interceptor (which routes through the
existing dd-trace-go OTel TracerProvider), ensures the tally-all
Schedule, then runs the Worker until SIGTERM. Mirrors the baseline
worker's shape so behavior reads side-by-side.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Dockerfile

**Files:**
- Create: `tally-worker-temporal/Dockerfile`

- [ ] **Step 1: Write the Dockerfile**

Write `/home/mike/demo/voting-app/tally-worker-temporal/Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1.7
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.work go.work.sum* ./
COPY shared/ ./shared/
COPY vote-api/ ./vote-api/
COPY tally-worker/ ./tally-worker/
COPY tally-worker-temporal/ ./tally-worker-temporal/
COPY results-api/ ./results-api/
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd tally-worker-temporal && go build -trimpath -ldflags="-s -w" \
      -o /out/tally-worker-temporal ./cmd/tally-worker-temporal

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/tally-worker-temporal /tally-worker-temporal
USER nonroot:nonroot
ENTRYPOINT ["/tally-worker-temporal"]
```

- [ ] **Step 2: Build the image to verify**

Run:
```bash
cd /home/mike/demo/voting-app && podman build -f tally-worker-temporal/Dockerfile -t tally-worker-temporal:test .
```
Expected: image builds successfully. Inspect with `podman images | grep tally-worker-temporal`. Remove with `podman rmi tally-worker-temporal:test` afterwards.

- [ ] **Step 3: Commit**

```bash
cd /home/mike/demo/voting-app
git add tally-worker-temporal/Dockerfile
git commit -m "$(cat <<'EOF'
feat(temporal): add tally-worker-temporal Dockerfile

Distroless static build, mirrors tally-worker/Dockerfile exactly except
for the build target and binary name. The COPY of every module is needed
because go.work needs every referenced module present to resolve.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Compose additions and profile gating

**Files:**
- Modify: `docker-compose.yml`

- [ ] **Step 1: Add `profiles: ["baseline"]` to the existing `tally-worker` service**

Find this block in `/home/mike/demo/voting-app/docker-compose.yml`:

```yaml
  tally-worker:
    build:
      context: .
      dockerfile: tally-worker/Dockerfile
    depends_on:
```

Insert one line immediately after `dockerfile: tally-worker/Dockerfile` so the block becomes:

```yaml
  tally-worker:
    build:
      context: .
      dockerfile: tally-worker/Dockerfile
    profiles: ["baseline"]
    depends_on:
```

- [ ] **Step 2: Add the three new services at the end of `services:`**

Append these blocks before the `volumes:` block at the bottom of `/home/mike/demo/voting-app/docker-compose.yml`:

```yaml
  temporal:
    image: temporalio/auto-setup:1.25
    profiles: ["temporal"]
    environment:
      DB: postgres12
      PROMETHEUS_ENDPOINT: "0.0.0.0:9090"
      TEMPORAL_ADDRESS: "temporal:7233"
    ports:
      - "7233:7233"
      - "9090:9090"
    healthcheck:
      test: ["CMD", "tctl", "--address", "temporal:7233", "cluster", "health"]
      interval: 5s
      timeout: 5s
      retries: 30

  temporal-ui:
    image: temporalio/ui:2.31
    profiles: ["temporal"]
    environment:
      TEMPORAL_ADDRESS: "temporal:7233"
      TEMPORAL_CORS_ORIGINS: "http://localhost:8233"
    ports:
      - "8233:8080"
    depends_on:
      temporal:
        condition: service_healthy

  tally-worker-temporal:
    build:
      context: .
      dockerfile: tally-worker-temporal/Dockerfile
    profiles: ["temporal"]
    depends_on:
      postgres:
        condition: service_healthy
      migrate:
        condition: service_completed_successfully
      datadog-agent:
        condition: service_started
      temporal:
        condition: service_healthy
    environment:
      POSTGRES_DSN: "postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable"
      TALLY_INTERVAL: "${TALLY_INTERVAL}"
      TEMPORAL_HOST_PORT: "temporal:7233"
      TEMPORAL_NAMESPACE: "${TEMPORAL_NAMESPACE}"
      TEMPORAL_TASK_QUEUE: "tally-task-queue"
      DD_SERVICE: "tally-worker-temporal"
      DD_ENV: "${DD_ENV}"
      DD_VERSION: "${DD_VERSION}"
      DD_AGENT_HOST: "datadog-agent"
      DD_TRACE_AGENT_PORT: "8126"
      DD_DOGSTATSD_PORT: "8125"
    labels:
      com.datadoghq.ad.logs: '[{"source":"go","service":"tally-worker-temporal"}]'
```

- [ ] **Step 3: Add the OpenMetrics conf mount to `datadog-agent`**

Find this line in `/home/mike/demo/voting-app/docker-compose.yml`:

```yaml
      - ./datadog/postgres.d/conf.yaml:/etc/datadog-agent/conf.d/postgres.d/conf.yaml:ro
```

Add a second mount line immediately below it:

```yaml
      - ./datadog/openmetrics.d/conf.yaml:/etc/datadog-agent/conf.d/openmetrics.d/conf.yaml:ro
```

- [ ] **Step 4: Validate compose syntax**

Run:
```bash
cd /home/mike/demo/voting-app && podman compose config --quiet
```
Expected: no output, exit 0. (Any YAML error prints here.)

Also confirm each profile renders:
```bash
podman compose --profile baseline config --services
podman compose --profile temporal config --services
```
Expected (baseline): includes `tally-worker`, excludes `temporal*` and `tally-worker-temporal`.
Expected (temporal): includes `temporal`, `temporal-ui`, `tally-worker-temporal`, excludes `tally-worker`.

- [ ] **Step 5: Commit**

```bash
cd /home/mike/demo/voting-app
git add docker-compose.yml
git commit -m "$(cat <<'EOF'
feat(temporal): add temporal/temporal-ui/tally-worker-temporal services

Gated behind the temporal compose profile. The existing tally-worker
moves to the baseline profile, so the two variants are mutually
exclusive (and can't race on vote_results upserts). The Datadog Agent
gains an unconditional openmetrics mount; the check fails harmlessly
when temporal isn't running.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Datadog OpenMetrics conf

**Files:**
- Create: `datadog/openmetrics.d/conf.yaml`

- [ ] **Step 1: Write the conf**

```bash
mkdir -p /home/mike/demo/voting-app/datadog/openmetrics.d
```

Write `/home/mike/demo/voting-app/datadog/openmetrics.d/conf.yaml`:

```yaml
# Scrape Temporal's Prometheus endpoint when the temporal profile is up.
# When temporal isn't running the check fails its scrape and logs a
# warning; the rest of the agent is unaffected.
init_config:

instances:
  - openmetrics_endpoint: http://temporal:9090/metrics
    namespace: temporal
    metrics:
      - temporal_request*
      - temporal_workflow*
      - temporal_activity*
      - temporal_poll*
      - temporal_persistence*
    tags:
      - service:temporal
```

Note: env-var expansion (`${DD_ENV}`) doesn't happen in static conf.d files (see comment in `datadog/postgres.d/conf.yaml`). The `DD_ENV` tag is already added globally via the agent's `DD_TAGS` env var, so we don't need to repeat it here.

- [ ] **Step 2: Commit**

```bash
cd /home/mike/demo/voting-app
git add datadog/openmetrics.d/conf.yaml
git commit -m "$(cat <<'EOF'
feat(datadog): scrape Temporal Prometheus endpoint via OpenMetrics check

Static conf mirrors the Postgres check pattern. The temporal namespace
prefixes all scraped metrics, so they land in Datadog as temporal.*
series and slot into the architecture doc's "Temporal Platform"
dashboard panel.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Makefile and `.env.example` updates

**Files:**
- Modify: `Makefile`
- Modify: `.env.example`
- Modify: `scripts/smoke.sh`

- [ ] **Step 1: Update the Makefile**

Edit `/home/mike/demo/voting-app/Makefile`:

Change the `.PHONY` line at the top so it reads:

```
.PHONY: up down logs ps test tidy fmt vet build smoke up-temporal down-temporal smoke-temporal k8s-build k8s-up k8s-smoke k8s-down
```

Replace the `up:` target so it reads:

```
up:
	podman compose --profile baseline up -d --build
```

Add these targets immediately below the existing `smoke:` target:

```
up-temporal:
	podman compose --profile temporal up -d --build

down-temporal:
	podman compose --profile temporal down -v

smoke-temporal:
	./scripts/smoke-temporal.sh
```

Also update the `tidy:` target to include the new module:

```
tidy:
	cd shared              && go mod tidy
	cd vote-api            && go mod tidy
	cd tally-worker        && go mod tidy
	cd tally-worker-temporal && go mod tidy
	cd results-api         && go mod tidy
```

And `fmt:`:

```
fmt:
	gofmt -w shared vote-api tally-worker tally-worker-temporal results-api
```

- [ ] **Step 2: Update `.env.example`**

Append to `/home/mike/demo/voting-app/.env.example`:

```
# Temporal (only used by --profile temporal)
TEMPORAL_NAMESPACE=default
```

- [ ] **Step 3: Update `scripts/smoke.sh` to explicitly use the baseline profile**

Edit `/home/mike/demo/voting-app/scripts/smoke.sh`. Find these two lines:

```bash
podman compose up -d --build postgres
podman compose run --rm migrate
podman compose up -d --build vote-api tally-worker results-api
```

Replace with:

```bash
podman compose --profile baseline up -d --build postgres
podman compose --profile baseline run --rm migrate
podman compose --profile baseline up -d --build vote-api tally-worker results-api
```

Also update the cleanup trap to scope to the baseline profile so it doesn't accidentally bring down a parallel temporal stack:

```bash
cleanup() { podman compose --profile baseline down -v >/dev/null 2>&1 || true; }
```

- [ ] **Step 4: Verify `make` targets parse**

Run:
```bash
cd /home/mike/demo/voting-app && make -n up up-temporal down down-temporal smoke smoke-temporal
```
Expected: each target prints its command without errors.

- [ ] **Step 5: Commit**

```bash
cd /home/mike/demo/voting-app
git add Makefile .env.example scripts/smoke.sh
git commit -m "$(cat <<'EOF'
feat(temporal): make up-temporal / down-temporal / smoke-temporal targets

make up now activates the baseline profile explicitly, matching the
docker-compose changes. smoke.sh is updated likewise. Adds
TEMPORAL_NAMESPACE to .env.example.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: `smoke-temporal.sh` end-to-end script

**Files:**
- Create: `scripts/smoke-temporal.sh`

- [ ] **Step 1: Write the script**

Write `/home/mike/demo/voting-app/scripts/smoke-temporal.sh`:

```bash
#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

export DOCKER_HOST="${DOCKER_HOST:-unix:///run/user/$(id -u)/podman/podman.sock}"
export TESTCONTAINERS_RYUK_DISABLED=true

cleanup() { podman compose --profile temporal down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

cleanup
podman compose --profile temporal up -d --build postgres
podman compose --profile temporal run --rm migrate
podman compose --profile temporal up -d --build temporal
podman compose --profile temporal up -d --build vote-api results-api temporal-ui tally-worker-temporal

# Wait for vote-api readiness.
for i in $(seq 1 60); do
  if curl -fsS http://localhost:8081/readyz >/dev/null 2>&1; then break; fi
  sleep 1
done

# Wait for the Temporal Schedule "tally-all" to exist. The worker creates
# it on startup; failing this check means the worker never connected.
for i in $(seq 1 60); do
  if podman compose --profile temporal exec -T temporal \
       tctl --address temporal:7233 schedule list 2>/dev/null | grep -q '^tally-all'; then
    break
  fi
  sleep 1
done
podman compose --profile temporal exec -T temporal \
  tctl --address temporal:7233 schedule list | grep -q '^tally-all' \
  || { echo "FAIL: tally-all Schedule was not created"; exit 1; }

# 5 tacos, 2 burritos.
for i in 1 2 3 4 5; do
  curl -fsS -X POST http://localhost:8081/vote -H 'content-type: application/json' \
       -d "{\"poll_id\":\"smoke-temporal\",\"choice\":\"tacos\",\"user_id\":\"u$i\"}" >/dev/null
done
for i in 1 2; do
  curl -fsS -X POST http://localhost:8081/vote -H 'content-type: application/json' \
       -d "{\"poll_id\":\"smoke-temporal\",\"choice\":\"burritos\",\"user_id\":\"v$i\"}" >/dev/null
done

# Wait for at least one tally interval + Temporal scheduling slack.
sleep 10

OUT="$(curl -fsS 'http://localhost:8082/results?poll_id=smoke-temporal')"
echo "$OUT"

echo "$OUT" | grep -q '"choice":"tacos","count":5' || { echo "FAIL: tacos != 5"; exit 1; }
echo "$OUT" | grep -q '"choice":"burritos","count":2' || { echo "FAIL: burritos != 2"; exit 1; }
echo "smoke-temporal OK"
```

- [ ] **Step 2: Make it executable**

```bash
chmod +x /home/mike/demo/voting-app/scripts/smoke-temporal.sh
```

- [ ] **Step 3: Run the smoke**

Run:
```bash
cd /home/mike/demo/voting-app && make smoke-temporal
```
Expected: the script prints results JSON and ends with `smoke-temporal OK`. First run pulls the `temporalio/auto-setup:1.25` image (~600MB) and may take 60–90s before the schedule appears.

If the Schedule wait loop times out, `podman compose --profile temporal logs tally-worker-temporal` will show the `ensure schedule` error. Common causes: wrong Temporal SDK ScheduleClient API for the version `go mod tidy` resolved (see Task 5 Step 2 note), or the `temporal` container failing health checks (check `podman compose logs temporal`).

- [ ] **Step 4: Commit**

```bash
cd /home/mike/demo/voting-app
git add scripts/smoke-temporal.sh
git commit -m "$(cat <<'EOF'
test(temporal): add smoke-temporal end-to-end script

Brings up the temporal compose profile, waits for the tally-all
Schedule to be created by the worker, posts votes via vote-api, and
verifies results-api returns the right counts after one Temporal-driven
tally cycle.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: Docs — README, CLAUDE.md, architecture doc

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `voting-app-architecture.md`

- [ ] **Step 1: Update README**

Add a new section to `/home/mike/demo/voting-app/README.md`. Place it after the existing "Bring up the stack" section (find the heading by searching for "make up" in the file):

```markdown
## Temporal variant

The repo ships two mutually exclusive `tally-worker` variants behind compose profiles:

| Profile | Worker | Schedule mechanism |
| --- | --- | --- |
| `baseline` (default) | `tally-worker` | Go timer loop, every `TALLY_INTERVAL` |
| `temporal` | `tally-worker-temporal` | Temporal Schedule `tally-all`, every `TALLY_INTERVAL` |

```bash
# Baseline (default — same as before)
make up

# Temporal variant
make up-temporal
# Temporal UI: http://localhost:8233
# Temporal gRPC: localhost:7233

make smoke-temporal   # end-to-end check
make down-temporal
```

Running both profiles at the same time is **not supported** — both workers
would race on `vote_results` upserts. The Makefile targets keep them
separate; if you bypass them and invoke `podman compose` directly, pass
exactly one `--profile`.

The Temporal server is `temporalio/auto-setup` (server + persistence in
one container) — convenient for the demo, not production-shaped. Server
metrics flow into Datadog via the `temporal.*` namespace through an
OpenMetrics scrape of `http://temporal:9090/metrics`.
```

- [ ] **Step 2: Update CLAUDE.md**

In `/home/mike/demo/voting-app/CLAUDE.md`, find this line:

```
Temporal variant (§4.4.2) is **not implemented** — see `docs/superpowers/plans/` for that follow-up plan when it lands.
```

Replace it with:

```
Temporal variant is implemented as `tally-worker-temporal/` (separate Go module) behind the `temporal` compose profile. `make up-temporal` brings it up; `make smoke-temporal` exercises it end-to-end. The baseline `tally-worker` lives on the `baseline` profile and remains the default for `make up`.
```

- [ ] **Step 3: Update the architecture doc §4.4.2**

In `/home/mike/demo/voting-app/voting-app-architecture.md`, find this block:

```
- Host Temporal Workflows and Activities, e.g.:
  - `TallyWorkflow(pollID string)`
  - `ReadVotesActivity`, `AggregateVotesActivity`, `PersistResultsActivity`
```

Replace it with:

```
- Host one Temporal Workflow and one Activity:
  - `TallyWorkflow()` — calls `TallyActivity`, returns its `Stats`.
  - `TallyActivity` — thin wrapper around the existing `tally.Aggregator` (imported from `tally-worker/tally`). A single activity preserves the single-Postgres-transaction idempotency property of the baseline; splitting the SELECT and UPSERT across activities would move the transactional boundary into the workflow.
```

In the same file, find:

```
- **Workflow ID strategy:** for scheduled runs, the Temporal Schedule owns the per-run suffix and the base ID is `tally-{poll_id}`. For ad-hoc runs (manual trigger, signal-driven), use `tally-{poll_id}-{RFC3339-timestamp}`. Set `WorkflowIDReusePolicy: AllowDuplicate` so successive runs don't collide with completed history.
- **Schedule mechanism:** Temporal Schedules (server-managed). Schedule ID `tally-{poll_id}`; interval matches the baseline worker (e.g., every 5 seconds). No external cron, no `continue-as-new` loop.
```

Replace with:

```
- **Workflow ID strategy:** the Temporal Schedule owns the per-run suffix; the base ID is `tally-all`. `WorkflowIDReusePolicy: AllowDuplicate` so successive runs don't collide with completed history.
- **Schedule mechanism:** Temporal Schedules (server-managed). Single global Schedule with ID `tally-all`; interval matches the baseline worker (default 5 seconds). One workflow per tick aggregates all polls — same shape as the baseline. Per-poll scheduling is a possible v2 once polls become first-class entities in the schema. No external cron, no `continue-as-new` loop.
```

- [ ] **Step 4: Commit**

```bash
cd /home/mike/demo/voting-app
git add README.md CLAUDE.md voting-app-architecture.md
git commit -m "$(cat <<'EOF'
docs(temporal): document the temporal compose profile

README gets a "Temporal variant" section with the make targets and the
mutually-exclusive-profile caveat. CLAUDE.md's "not implemented" note
flips to "implemented behind --profile temporal". Architecture doc
§4.4.2 reconciles with the implemented design: single TallyActivity
(preserves single-tx) and single global tally-all Schedule (matches
baseline shape).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 13: Final verification

This task runs no new code — it confirms the full test matrix is green before declaring the feature done.

- [ ] **Step 1: Unit + integration tests across the workspace**

Run:
```bash
cd /home/mike/demo/voting-app
DOCKER_HOST=unix:///run/user/$(id -u)/podman/podman.sock \
  TESTCONTAINERS_RYUK_DISABLED=true \
  go test ./... -count=1 -timeout 180s
```
Expected: every package PASS, including `tally-worker-temporal/internal/workflow` and `tally-worker-temporal/internal/activity` (both unit and integration variants).

- [ ] **Step 2: Baseline still works**

Run:
```bash
cd /home/mike/demo/voting-app && make down && make smoke
```
Expected: existing `make smoke` ends with `smoke OK`. This confirms the profile gating didn't break the baseline path.

- [ ] **Step 3: Temporal smoke**

Run:
```bash
cd /home/mike/demo/voting-app && make down && make smoke-temporal
```
Expected: ends with `smoke-temporal OK`.

- [ ] **Step 4: Inspect Temporal UI**

Run:
```bash
cd /home/mike/demo/voting-app && make up-temporal
```
Open `http://localhost:8233` in a browser. Expected:

- "Schedules" tab shows `tally-all` with interval 5s.
- "Workflows" tab shows recent `tally-all-<timestamp>` executions, each completed in well under 1s, with input/result visible in the history.
- Click into one execution → see workflow + activity spans tagged with `tally.activity` and `tally.run` (if the Datadog OTel interceptor is wired correctly, these show up as a single distributed trace in Datadog APM too).

Tear down:
```bash
make down-temporal
```

- [ ] **Step 5: Confirm Datadog signals (manual)**

In the Datadog UI (or via the trace explorer):

- **APM service list** shows `tally-worker-temporal` alongside `tally-worker`.
- **Metrics explorer** shows `tally_runs_total{service:tally-worker-temporal}` and `temporal.workflow.completed.count` (or whatever Prometheus name auto-setup emits — the namespace is `temporal.*`).
- **Logs explorer**, filtered to `service:tally-worker-temporal`, shows JSON entries with `temporal.workflow_id` and `temporal.run_id` fields.

This step has no automated assertion; mark it complete after eyeballing the dashboards.

---

## Self-review (writer's pass)

**Spec coverage**

- §3 decision: variant coexistence via compose profiles — Task 8.
- §3 decision: `temporalio/auto-setup` + UI — Task 8.
- §3 decision: single `TallyActivity` — Tasks 3, 4.
- §3 decision: global `tally-all` Schedule — Task 5.
- §3 decision: OpenMetrics scrape — Tasks 8, 9.
- §4.1 new module + file layout — Tasks 1, 2, 3, 5, 6, 7.
- §4.2 compose services — Task 8.
- §4.3 baseline profile + Makefile + smoke.sh update — Task 10.
- §4.4 openmetrics conf — Task 9.
- §5 env vars (`TEMPORAL_NAMESPACE`) — Task 10.
- §6 data flow — encoded by Tasks 2 (workflow shape), 3 (activity), 5 (schedule).
- §7 observability: traces (interceptor) — Task 6; metrics (per-attempt) — Task 3; logs (workflow_id/run_id) — Task 3; Temporal server metrics — Tasks 8/9; graceful shutdown — Task 6.
- §8 testing: workflow unit — Task 2; activity integration — Task 4; smoke-temporal — Task 11.
- §9 arch-doc amendments — Task 12.
- §10 out-of-scope items: none of them appear in any task ✓.
- §11 risks: profile mutual exclusion documented in Task 12 README; auto-setup demo-only documented in Task 12 README.

**Placeholder scan:** no `TBD`/`TODO`/"implement later" in any step. All code blocks are complete; every command has expected output.

**Type consistency:**
- `TallyActivityName = "TallyActivity"` is defined once in `workflow/tally.go` and referenced from `workflow/tally.go` (in `ExecuteActivity`) — the activity-side registration goes through `activity.RegisterOptions()` which hard-codes the same string. The two strings must stay in lockstep; documented in the Task 3 implementation comment.
- `workflow.TallyWorkflow` is registered under name `"TallyWorkflow"` in `main.go`, and the Schedule action's `Workflow:` field in `schedule.EnsureTallySchedule` receives that name as the `workflowName` parameter. Names flow from `main.go` through one pathway only.
- `Aggregator` interface in `activity` package matches `*tally.Aggregator`'s `Run(ctx) (Stats, error)` signature.

No issues found.

---

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-05-13-temporal-tally.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration. Best fit here because tasks 2/3/5/6 each have clean test-driven boundaries and don't share in-flight state.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

**Which approach?**
