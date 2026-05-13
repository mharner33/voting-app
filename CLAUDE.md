# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository layout

- `migrations/` — golang-migrate SQL files (`votes`, `vote_results`).
- `shared/` — Go module with `obs/` (tracing, logging, metrics, shutdown), `httpx/` (CORS, health), `pgxdb/` (otelpgx-wrapped pool). Imported by every service via `go.work`.
- `vote-api/`, `results-api/`, `tally-worker/` — independent Go modules. Each has `cmd/<name>/main.go` + `internal/` packages.
- `frontend/` — static HTML/JS served by nginx.
- `docker-compose.yml` — `postgres`, one-shot `migrate`, the three Go services, `frontend`, and `datadog-agent`. The architecture doc remains the source of truth for HTTP shapes, schema, and metric names.

## Build / test / run

| Task | Command |
| --- | --- |
| Run all unit + integration tests (Docker required) | `make test` |
| Build all images | `make build` |
| Bring the full stack up | `cp .env.example .env && $EDITOR .env && make up` |
| Tear down (incl. volumes) | `make down` |
| End-to-end smoke test | `make smoke` |
| Per-service test | `cd <service> && go test ./... -timeout 120s` |

Integration tests for the data path (`vote-api/internal/store`, `tally-worker/tally`, `results-api/internal/store`) use `testcontainers-go/postgres` and **require a running Docker daemon**. Pure-handler tests (`vote-api/internal/handler`, `results-api/internal/handler`) and `shared/` tests don't.

Temporal variant (§4.4.2) is **not implemented** — see `docs/superpowers/plans/` for that follow-up plan when it lands.

## Architecture (planned)

The system is intentionally split into small services so observability signals can be demonstrated across process boundaries. Treat these boundaries as load-bearing — collapsing them defeats the purpose of the demo.

- **`frontend`** — static HTML/JS (no framework). Calls `vote-api` and `results-api` directly over HTTP.
- **`vote-api`** (Go) — write path. `POST /vote` inserts into the `votes` table.
- **`results-api`** (Go) — read path. `GET /results?poll_id=…` reads pre-aggregated rows from `vote_results`.
- **`tally-worker`** (Go) — periodically aggregates `votes` → `vote_results`. Two planned variants share the same business logic:
  - **Baseline:** plain Go worker on a timer/cron.
  - **Temporal:** the same aggregation as `TallyWorkflow` + Activities. Use this variant only when explicitly demoing Temporal; keep business logic identical to the baseline.
- **`postgres`** — single database, two tables (`votes`, `vote_results`). Schema is in §4.5 of the architecture doc.
- **Observability stack** — OTel API in app code (`go.opentelemetry.io/otel`) implemented by the Datadog Go SDK (`dd-trace-go/v2`). Traces and metrics ship directly to the Datadog Agent — trace-agent port `:8126` for traces, dogstatsd `:8125` for metrics. **No standalone OTel Collector is in the path.** Temporal platform metrics flow via the Datadog Temporal integration when that variant is enabled.

### Key design constraints to preserve

- **Read/write split is deliberate.** `vote-api` only writes raw votes; `results-api` only reads aggregates. Don't have either service compute aggregates on the fly — that's the worker's job, and the staleness it introduces is part of what the demo illustrates.
- **`tally-worker` is idempotent by design.** Each run is a single Postgres transaction: `SELECT … GROUP BY` → `INSERT … ON CONFLICT (poll_id, choice) DO UPDATE` → commit. A crash leaves no partial state; the next interval re-reads everything. Preserve the single-transaction property in any refactor.
- **Every Go service must emit traces, metrics, and logs** — this is the whole point of the project. New endpoints/handlers need OTel HTTP middleware, DB spans (`otelsql`/`otelpgx`), the metrics listed per service in §4.2–4.4, and structured JSON logs carrying `trace_id`/`span_id` plus business fields (`poll_id`, `choice`, `user_id`). A handler without instrumentation is incomplete, not "to be added later."
- **Temporal is optional and additive.** The baseline worker must remain functional standalone; Temporal is a swap-in variant, not a replacement.
- **Graceful shutdown is required, not optional.** Every Go service must trap `SIGTERM`/`SIGINT` and call `provider.Shutdown(ctx)` on the registered OTel `TracerProvider` before exiting. Skipping this silently drops the final batch of spans — the failure mode looks like "the demo works but some traces are missing," which is exactly the kind of footgun this project is supposed to teach against.
- **Duplicate votes are intentional.** The `votes` table has no `UNIQUE(poll_id, user_id)` constraint by design — it keeps the write path simple and generates more telemetry. Don't "fix" this by adding a constraint. The architecture doc (§4.5 "Vote idempotency") explains how to switch behavior if a real deployment ever needs it.

## When implementing

- Follow the HTTP shapes, table schema, and metric names exactly as specified in the architecture doc — downstream dashboards and alerts (§5.4) assume them.
- Configuration is environment-variable driven: DB DSN; Datadog SDK vars (`DD_AGENT_HOST`, `DD_TRACE_AGENT_PORT`, `DD_SERVICE`, `DD_ENV`, `DD_VERSION`); worker tuning (`TALLY_INTERVAL`, default `5s`); Temporal (`TEMPORAL_NAMESPACE`) when that variant is enabled. Don't hardcode any of these.
- Local orchestration is intended to be `docker-compose` with all components (services + postgres + datadog-agent + optional temporal/temporal-ui). The Datadog Agent receives traces and metrics directly from each service — there is no separate OTel Collector container. When you add a service, add it to the compose file at the same time.

## Updating this file

Once code lands, replace the "Repository status" section with real build/test/run commands and prune any planning notes that the code itself now makes obvious.
