# Temporal `tally-worker` Variant — Design

**Date:** 2026-05-13
**Status:** Approved for implementation planning
**Scope:** Add a Temporal-driven variant of `tally-worker` to the voting-app demo, alongside the existing baseline timer worker.

## 1. Goal

Demonstrate Temporal (temporal.io) as an optional, additive way to drive the tally aggregation, with the same business logic as the baseline worker and the same observability signals (traces, metrics, logs) reaching Datadog. The baseline worker must remain the default and keep functioning unchanged.

## 2. Constraints carried in from `CLAUDE.md` and the architecture doc

- **Baseline must keep working standalone.** Temporal is a swap-in variant, not a replacement (`CLAUDE.md` "Temporal is optional and additive").
- **Same business logic.** The Temporal variant must use the existing aggregator; we do not rewrite the SQL or change semantics (architecture §4.4).
- **Single-Postgres-transaction property must be preserved.** Each tally run is `SELECT … GROUP BY` + `INSERT … ON CONFLICT … UPDATE` in one transaction (`CLAUDE.md` "Preserve the single-transaction property in any refactor").
- **Graceful shutdown is mandatory.** Every Go service traps `SIGTERM`/`SIGINT` and calls `provider.Shutdown(ctx)` before exit (`CLAUDE.md`).
- **All Go services emit OTel traces, metrics, and JSON logs with `trace_id`/`span_id` plus business fields.**
- **Telemetry transport stays the same.** App-side traces and metrics go directly to the Datadog Agent (`:8126` traces, `:8125` dogstatsd). No standalone OTel Collector.

## 3. Decisions (locked during brainstorming)

| Decision | Choice |
|---|---|
| Variant coexistence | Compose **profiles**. `baseline` profile runs today's `tally-worker`; `temporal` profile runs `tally-worker-temporal` + Temporal server + UI. Mutually exclusive. |
| Temporal server flavor | `temporalio/auto-setup` (single container with bundled Postgres persistence) + `temporalio/ui`. Temporal's persistence is separate from the app's `postgres`. |
| Activity decomposition | A **single** `TallyActivity` that wraps the existing aggregator, preserving its single-transaction property. The architecture doc's three-activity sketch (§4.4.2) is amended to match. |
| Schedule scope | One **global** Schedule `tally-all` firing one workflow that aggregates all polls — mirrors today's baseline. The architecture doc's per-poll Schedule model is amended to match; per-poll is a possible v2. |
| Temporal server metrics → Datadog | Datadog Agent **OpenMetrics** check scraping Temporal's Prometheus endpoint (`:9090`). |

## 4. Architecture

### 4.1 New Go service: `tally-worker-temporal/`

A new Go module, peer of `tally-worker/`, also listed in `go.work`.

```
tally-worker-temporal/
  cmd/tally-worker-temporal/main.go
  internal/
    workflow/     # TallyWorkflow
    activity/     # TallyActivity
    schedule/     # EnsureTallySchedule
  Dockerfile
  go.mod
```

**`main.go`** does, in order:

1. Read env vars (§5).
2. Initialize `shared/obs` tracing, logging, metrics — identical to baseline.
3. Open pgx pool via `shared/pgxdb`.
4. Construct Temporal client (`go.temporal.io/sdk/client`) against `${TEMPORAL_HOST_PORT}` in namespace `${TEMPORAL_NAMESPACE}`.
5. Call `schedule.EnsureTallySchedule(ctx, client, interval)` — idempotent create-or-update of the `tally-all` Schedule.
6. Construct a Worker on `${TEMPORAL_TASK_QUEUE}` with the Datadog tracing interceptor (`go.temporal.io/sdk/contrib/opentelemetry`).
7. Register `TallyWorkflow` and `TallyActivity{Pool: pool}`.
8. Run the Worker under `obs.RunUntilSignal`, so `SIGTERM`/`SIGINT` triggers worker stop → pool close → tracing shutdown.

**`internal/workflow/tally.go`** — pure workflow code, no I/O:

```go
func TallyWorkflow(ctx workflow.Context) (tally.Stats, error) {
    ao := workflow.ActivityOptions{
        StartToCloseTimeout: 30 * time.Second,
        RetryPolicy: &temporal.RetryPolicy{
            InitialInterval:    time.Second,
            BackoffCoefficient: 2.0,
            MaximumInterval:    10 * time.Second,
            MaximumAttempts:    3,
        },
    }
    ctx = workflow.WithActivityOptions(ctx, ao)
    var stats tally.Stats
    err := workflow.ExecuteActivity(ctx, "TallyActivity").Get(ctx, &stats)
    return stats, err
}
```

**`internal/activity/tally.go`** — thin wrapper around the existing aggregator:

```go
type TallyActivity struct{ Agg *tally.Aggregator }

func (a *TallyActivity) Run(ctx context.Context) (tally.Stats, error) {
    info := activity.GetInfo(ctx)
    // log with temporal.workflow_id, temporal.run_id, temporal.activity_id
    return a.Agg.Run(ctx)
}
```

The `tally` package is imported directly from `github.com/mharner33/voting-app/tally-worker/tally` — it is the canonical implementation and stays in the baseline module.

**`internal/schedule/ensure.go`** — `EnsureTallySchedule(ctx, client, interval)`:

- Look up Schedule `tally-all` via `client.ScheduleClient().GetHandle(ctx, "tally-all").Describe(ctx)`.
- If not found, create it with:
  - Spec: `Intervals: [{Every: interval}]`
  - Action: start workflow `TallyWorkflow` on `tally-task-queue`, `WorkflowIDReusePolicy: AllowDuplicate`, base `WorkflowID: tally-all` (Temporal appends the scheduled timestamp).
- If found with a different interval, `Update()` it.

### 4.2 Temporal infrastructure (compose-only)

Two new services in `docker-compose.yml`, both with `profiles: ["temporal"]`:

- **`temporal`** — `temporalio/auto-setup:1.25` (or current latest stable).
  - Env: `DB=postgres12`, `PROMETHEUS_ENDPOINT=0.0.0.0:9090`, `TEMPORAL_ADDRESS=temporal:7233`.
  - Ports: `7233:7233` (gRPC), `9090:9090` (Prometheus).
  - Healthcheck: `tctl --address temporal:7233 cluster health`.
- **`temporal-ui`** — `temporalio/ui:2.31`.
  - Env: `TEMPORAL_ADDRESS=temporal:7233`.
  - Ports: `8233:8080`.
  - `depends_on: { temporal: { condition: service_healthy } }`.

### 4.3 Variant selection via compose profiles

Existing `tally-worker` gains `profiles: ["baseline"]`. New `tally-worker-temporal` has `profiles: ["temporal"]`. All other services (`postgres`, `migrate`, `vote-api`, `results-api`, `frontend`, `datadog-agent`) remain unprofiled, so they always run.

Makefile gains:

- `make up` → `docker compose --profile baseline up -d` (default — same UX as today).
- `make up-temporal` → `docker compose --profile temporal up -d`.
- `make down` / `make down-temporal` — symmetric.
- `make smoke-temporal` — same flow as `make smoke`, but waits for `temporal` health and for the `tally-all` Schedule to exist before posting votes.

Running both profiles simultaneously is unsupported (two workers would race on `vote_results` upserts). README documents this.

### 4.4 Datadog OpenMetrics integration for Temporal server

New file: `datadog/openmetrics.d/conf.yaml` (mirrors the existing `datadog/postgres.d/conf.yaml` pattern):

```yaml
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
      - env:${DD_ENV}
      - service:temporal
```

Mount it on the `datadog-agent` service the same way Postgres is mounted today, by adding one more volume line:

```yaml
- ./datadog/openmetrics.d/conf.yaml:/etc/datadog-agent/conf.d/openmetrics.d/conf.yaml:ro
```

The mount is unconditional. When the `temporal` profile is not active, the OpenMetrics check fails its scrape and logs a warning, but the rest of the agent is unaffected. This is simpler than a profile-gated override and consistent with the rest of the demo's static-conf approach.

## 5. Configuration

### 5.1 New env vars

Added to `.env.example`:

```
# Temporal (only used by --profile temporal)
TEMPORAL_NAMESPACE=default
```

`TEMPORAL_HOST_PORT=temporal:7233` and `TEMPORAL_TASK_QUEUE=tally-task-queue` are hardcoded in `docker-compose.yml` for `tally-worker-temporal` — they have no operator-tunable value in this demo.

### 5.2 Reused env vars

`POSTGRES_DSN`, `TALLY_INTERVAL`, `DD_SERVICE` (set to `tally-worker-temporal`), `DD_ENV`, `DD_VERSION`, `DD_AGENT_HOST`, `DD_TRACE_AGENT_PORT`, `DD_DOGSTATSD_PORT` — same shape as the baseline worker.

## 6. Data flow

```
Temporal Schedule "tally-all"  ──every TALLY_INTERVAL──▶  WorkflowExecution
   (server-managed, no continue-as-new)                     WorkflowID: tally-all-{ts}
                                                            TaskQueue:  tally-task-queue

WorkflowExecution (TallyWorkflow)
   │
   └─ workflow.ExecuteActivity(ctx, "TallyActivity")
          ActivityOptions { StartToCloseTimeout: 30s,
                            RetryPolicy: 1s × 2.0 → 10s, max 3 attempts }

TallyActivity (tally-worker-temporal process)
   │
   ├─ otel span "tally.activity"  (child of workflow span via DD interceptor)
   ├─ aggregator.Run(ctx)         (imported from tally-worker/tally)
   │     └─ single Postgres tx: SELECT … GROUP BY → UPSERT vote_results → COMMIT
   │           └─ otel span "tally.run" with child SELECT/UPSERT spans
   └─ returns Stats{RowsUpserted, PollsTouched}  (visible in workflow history)
```

**Correctness properties:**

- Activity is idempotent (the aggregator already is). At-least-once activity execution under retry is safe.
- Workflow is short-lived per tick. No `continue-as-new`, no long sleeps.
- Schedule firing while the previous run is still executing is allowed (`AllowDuplicate`). In normal operation each tick completes well under 5s.

## 7. Observability

### 7.1 App-side tracing

- `dd-trace-go` initialized via `shared/obs.StartTracing`, identical to baseline.
- Temporal SDK Datadog interceptor (`go.temporal.io/sdk/contrib/opentelemetry`) registered on the Worker so workflow and activity boundaries get spans automatically and span context flows from workflow → activity.
- Inside `TallyActivity`: explicit `otel` span `tally.activity` for symmetry with the baseline; the underlying `tally.run` and `SELECT`/`UPSERT` spans come from the existing aggregator unchanged.

### 7.2 App-side metrics

Emitted from inside `TallyActivity` (so a retried attempt counts as a separate run, matching baseline semantics):

- `tally_runs_total{status=success|error}`
- `tally_duration_seconds`
- `tally_last_success_timestamp`

Per-workflow-completion counts come from the Temporal server's own metrics scraped via OpenMetrics (`temporal.workflow_*` series), which is more accurate than emitting app-side — the server is the source of truth for completed workflows.

All app-side metrics tagged with `service:tally-worker-temporal` via `DD_SERVICE`, so they don't collide with baseline metrics in Datadog.

### 7.3 Logs

JSON via `shared/obs.Logger`, with the standard `service`/`env`/`version`/`trace_id`/`span_id` fields, plus:

- `temporal.workflow_id`, `temporal.run_id`, `temporal.activity_id` (from `activity.GetInfo(ctx)`).

### 7.4 Temporal server metrics

Datadog Agent's OpenMetrics check scrapes `http://temporal:9090/metrics` (§4.4). Series prefixed `temporal.*` in Datadog. Powers the "Temporal Platform" panel in §5.4 of the architecture doc.

### 7.5 Graceful shutdown

`main.go` wraps the Worker's `Run` inside `obs.RunUntilSignal(ctx, 10*time.Second, …)`, which on SIGTERM:

1. Calls `worker.Stop()` (drains in-flight workflow/activity tasks).
2. Closes the pgx pool.
3. Closes the Datadog tracer.

Skipping any of these silently drops the final batch of telemetry — exactly the failure mode CLAUDE.md calls out.

## 8. Testing

Three layers, mirroring the rest of the repo:

1. **Workflow unit tests** — `internal/workflow/tally_test.go` using `go.temporal.io/sdk/testsuite.TestWorkflowEnvironment`. Mock `TallyActivity`. Assert: workflow invokes it once with the right options, returns the activity's `Stats`, propagates errors, retries within policy. No DB, no Docker.
2. **Activity integration test** — `internal/activity/tally_test.go` using `testsuite.TestActivityEnvironment` + a testcontainers Postgres (same helper pattern as `tally-worker/tally`). Asserts the activity produces the same rows in `vote_results` as calling the baseline aggregator directly. Requires Docker.
3. **E2E smoke** — `make smoke-temporal`: brings up `--profile temporal`, waits for `tctl cluster health` and for Schedule `tally-all`, posts votes via `vote-api`, polls `results-api` until counts match, tears down.

We deliberately do **not** spin up a Temporal server in unit tests — the SDK test environment covers workflow/activity correctness without it.

## 9. Architecture-doc amendments

Two diffs to `voting-app-architecture.md` §4.4.2 to keep the doc consistent with this spec:

1. Replace `ReadVotesActivity`/`AggregateVotesActivity`/`PersistResultsActivity` with a single `TallyActivity`, with a one-line rationale (preserves the single-Postgres-transaction idempotency property).
2. Replace per-poll Schedule (`tally-{poll_id}`) with a single global Schedule `tally-all` aggregating all polls. Note that per-poll scheduling is a possible v2 once polls become first-class entities in the schema.

`CLAUDE.md` already says the Temporal variant is not implemented and points readers at `docs/superpowers/plans/`. After implementation lands, that note gets updated to reference the new compose profile and `make` targets, not changed as part of this spec.

## 10. Out of scope (v1)

- Per-poll Schedules and a "polls" first-class concept.
- Manual ad-hoc workflow trigger CLI.
- Workflow signals or queries.
- Temporal Cloud target (the design works against Cloud unchanged by pointing `TEMPORAL_HOST_PORT` at a Cloud namespace, but we don't ship Cloud config in this spec).
- Multiple worker replicas / horizontal scaling — single replica is enough for the demo.

## 11. Risks and footguns

- **Compose profile mutual exclusion is convention-enforced.** Nothing stops `docker compose --profile baseline --profile temporal up` and the resulting upsert race. Mitigation: explicit warning in README and in each Makefile target's help text.
- **Temporal `auto-setup` is for dev only.** It runs server + persistence in one container and is not what you'd ship to production. The architecture doc already frames everything here as a demo; we restate it in the README section for the Temporal profile.
- **Schedule drift on interval change.** `EnsureTallySchedule` updates the Schedule if `TALLY_INTERVAL` changed between runs. If two workers come up with different intervals (shouldn't happen, but…) the last writer wins. Acceptable for a demo; documented.
- **Datadog Temporal SDK interceptor version drift.** The Temporal Go SDK's Datadog contrib package has historically lagged the main SDK. If a version pin is required at implementation time, capture it in the plan, not here.
