# Voting App – Architecture Document

## 1. Overview

The Voting App is a small microservices system used to demonstrate modern observability practices (Datadog, OpenTelemetry) and, optionally, Temporal for reliable background processing.

Users vote between options via a simple web UI. Votes are persisted in PostgreSQL. A background process periodically aggregates raw votes into results for display in the UI.

Primary technologies:
- Frontend: HTML / CSS / vanilla JavaScript
- Backend services: Go
- Database: PostgreSQL
- Observability: OpenTelemetry API in application code; Datadog Go SDK (`dd-trace-go/v2`) provides the OTel TracerProvider; Datadog Agent ingests traces, metrics, and logs.
- Optional: Temporal (for tally workflows)

---

## 2. Goals and Non‑Goals

**Goals**

- Demonstrate observability patterns across multiple services:
  - Request/DB traces, metrics, and logs with correlation IDs.
  - Dashboards and alerts in Datadog for both app and Temporal platform.
- Showcase a simple, understandable microservices architecture suitable for local dev and containerized deployment.
- Optionally show how Temporal can orchestrate the background tally workflow and expose its metrics/logs via Datadog.

**Non‑Goals**

- No complex domain logic (e.g., multi‑tenant access control, auth flows).
- No requirement for global scale or strong security hardening (this is a demo, not production).
- No sophisticated front‑end frameworks (React/Vue/etc.).

---

## 3. High‑Level Architecture

### 3.1 Components

- `frontend`
  - Static HTML/JS application served by a simple file server or CDN.
  - Talks to backend via JSON over HTTP.

- `vote-api` (Go)
  - Receives vote submissions from `frontend`.
  - Writes raw votes into PostgreSQL.
  - Exposes health and metrics endpoints.

- `results-api` (Go)
  - Serves aggregated results (e.g., total votes per option).
  - Reads pre‑computed results from PostgreSQL.
  - Exposes health and metrics endpoints.

- `tally-worker` (Go) – two variants:
  - **Baseline variant:** simple Go worker invoked on a schedule (cron or internal timer) that aggregates raw votes and writes results.
  - **Temporal variant (optional):** Temporal Worker hosting Workflows and Activities to perform the same aggregation in a durable, observable way.

- `postgres`
  - Persistent storage for raw votes and aggregated results.

- `observability stack`
  - OpenTelemetry API (`go.opentelemetry.io/otel`) used in application code for vendor-neutral instrumentation.
  - Datadog Go SDK (`github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry`) provides the OTel TracerProvider implementation. OTel API calls are handled by dd-trace-go and shipped directly to the Datadog Agent — trace-agent port `8126` for traces, dogstatsd port `8125` for metrics. No standalone OTel Collector is required.
  - Datadog Agent for trace ingestion, metrics aggregation, and log shipping.
  - Temporal metrics/logs integration when the Temporal variant is enabled.

### 3.2 Logical Data Flow

1. User loads `frontend` page.
2. User submits a vote (option A/B/…); JS sends `POST /vote` to `vote-api`.
3. `vote-api` validates input and inserts a row into `votes` table in PostgreSQL.
4. `tally-worker` periodically aggregates votes into `vote_results` (either directly or via Temporal Workflow).
5. `frontend` periodically calls `GET /results` on `results-api`, which reads from `vote_results` and returns aggregates.
6. Observability:
   - OTel spans per request/DB query; metrics and structured logs exported to Datadog.
   - Temporal platform metrics/logs sent to Datadog via Temporal/Datadog integration.

---

## 4. Service Responsibilities and APIs

### 4.1 `frontend`

**Description**
Static single‑page UI for casting votes and viewing results.

**Responsibilities**

- Render vote options and current results.
- Send AJAX requests to `vote-api` and `results-api`.

**Main Interactions**

- `POST /vote` → `vote-api`
- `GET /results` → `results-api`

**Observability (optional — Datadog RUM)**

- Load the Datadog Browser RUM SDK to capture page views, user interactions, JS errors, and resource timing.
- Configure RUM's `allowedTracingUrls` for the `vote-api` and `results-api` origins so outbound XHR/fetch calls inject W3C `traceparent` and Datadog trace headers. The backend continues the trace, yielding a single end-to-end span from browser click through Postgres write.

---

### 4.2 `vote-api` (Go)

**Description**
Write‑path API that records individual votes.

**Responsibilities**

- Validate vote payload.
- Insert vote row into Postgres.
- Emit observability signals (traces, metrics, logs).
- Apply a permissive CORS policy — the static `frontend` is served from a different origin in local dev.

**Example HTTP API**

- `POST /vote`
  - Request body:
    ```json
    {
      "poll_id": "default",
      "choice": "tacos",
      "user_id": "optional-user-id"
    }
    ```
  - Response:
    - `200 OK` on success
    - `400` for invalid payload
    - `500` for internal error

- `GET /healthz`
  - Liveness probe — process is running. Does **not** touch the DB.
- `GET /readyz`
  - Readiness probe — `SELECT 1` against Postgres succeeds. Used by orchestrators to gate traffic.
- `GET /version`
  - Returns `{"service", "version", "git_sha", "build_date"}`. Source of truth for Datadog deploy markers.

**Data Access**

- Inserts into `votes` table.

**Observability**

- OTel HTTP server middleware for trace spans.
- DB spans via `otelsql` or `otelpgx`.
- Metrics:
  - `http_server_requests_total`
  - `vote_submissions_total`
  - `http_request_duration_seconds`
- Logs with trace IDs and fields: `poll_id`, `choice`, `user_id`.

---

### 4.3 `results-api` (Go)

**Description**
Read‑path API that exposes vote aggregates.

**Responsibilities**

- Query `vote_results` for the specified poll.
- Return formatted aggregates to the frontend.
- Emit observability metrics/logs/traces.
- Apply a permissive CORS policy — same rationale as `vote-api`.

**Example HTTP API**

- `GET /results?poll_id=default`
  - Response body:
    ```json
    {
      "poll_id": "default",
      "results": [
        { "choice": "tacos", "count": 42 },
        { "choice": "burritos", "count": 17 }
      ],
      "updated_at": "2026-05-11T15:00:00Z"
    }
    ```

- `GET /healthz` — liveness (process is running, no DB call).
- `GET /readyz` — readiness (`SELECT 1` against Postgres).
- `GET /version` — same shape as `vote-api` §4.2.

**Data Access**

- Reads from `vote_results` table.

**Observability**

- Same patterns as `vote-api`: HTTP handler traces, DB spans, request metrics, structured logs.

---

### 4.4 `tally-worker` (Go)

There are two implementation variants; both share the same business logic.

#### 4.4.1 Baseline Worker (no Temporal)

**Responsibilities**

- Periodically, per poll, in a single Postgres transaction:
  - `SELECT poll_id, choice, COUNT(*) FROM votes GROUP BY poll_id, choice`.
  - For each row, `INSERT INTO vote_results … ON CONFLICT (poll_id, choice) DO UPDATE SET count = EXCLUDED.count, updated_at = now()`.
  - Commit.

**Behavior**

- Default interval: every 5 seconds. Keeps the UI feeling "live" and produces a steady stream of telemetry for dashboards. Override via `TALLY_INTERVAL` env var.
- The whole aggregation runs inside one transaction. A crash mid-tally produces no partial state — the next interval re-reads everything and re-upserts. The operation is naturally idempotent.
- Error handling via retries and logging.

**Observability**

- A span per run (`tally.run`) with child spans for `SELECT` and `UPSERT` queries.
- Metrics:
  - `tally_runs_total` (labels: `status=success|error`).
  - `tally_duration_seconds`.
  - `tally_last_success_timestamp`.
- Logs:
  - Info logs on start/finish, error logs on failure, with poll IDs and row counts.

#### 4.4.2 Temporal Worker Variant (optional)

**Responsibilities**

- Host Temporal Workflows and Activities, e.g.:
  - `TallyWorkflow(pollID string)`
  - `ReadVotesActivity`, `AggregateVotesActivity`, `PersistResultsActivity`

**Behavior**

- **Task queue:** `tally-task-queue`. Workers register the Workflow + Activities on this queue; clients (and the Temporal Schedule below) target the same queue when starting Workflows.
- **Workflow ID strategy:** for scheduled runs, the Temporal Schedule owns the per-run suffix and the base ID is `tally-{poll_id}`. For ad-hoc runs (manual trigger, signal-driven), use `tally-{poll_id}-{RFC3339-timestamp}`. Set `WorkflowIDReusePolicy: AllowDuplicate` so successive runs don't collide with completed history.
- **Schedule mechanism:** Temporal Schedules (server-managed). Schedule ID `tally-{poll_id}`; interval matches the baseline worker (e.g., every 5 seconds). No external cron, no `continue-as-new` loop.
- **Namespace:** single namespace, configurable via `TEMPORAL_NAMESPACE` (default `default`).
- Temporal guarantees:
  - Durable execution across worker restarts.
  - Automatic retries of Activities.
  - Detailed event history.

**Observability (app‑side)**

- Workflow and Activity handlers instrumented with OTel or Datadog tracer.
- Logs enriched with workflow IDs and run IDs.

**Observability (Temporal platform)**

- Temporal Server or Temporal Cloud metrics scraped and sent to Datadog using:
  - Datadog Temporal integration, or
  - Temporal Cloud OpenMetrics integration.
- Example Temporal metrics:
  - Workflow execution counts and latency.
  - Poller success/timeout rates.
  - Persistence errors.

---

### 4.5 `postgres`

**Schema (minimum)**

- `votes`
  - `id` (UUID or bigserial, PK)
  - `poll_id` (text)
  - `choice` (text)
  - `user_id` (text, nullable)
  - `created_at` (timestamp)
  - Index: `(poll_id, created_at)` — supports the worker's grouped read and any future "recent votes" UI.

- `vote_results`
  - `poll_id` (text, PK part)
  - `choice` (text, PK part)
  - `count` (integer)
  - `updated_at` (timestamp)

**Vote idempotency**

Duplicate votes from the same `user_id` are intentionally allowed — there is no `UNIQUE(poll_id, user_id)` constraint. This keeps the write path simple and produces more telemetry for the demo. For a real deployment, add that constraint and switch `vote-api` to `INSERT … ON CONFLICT DO NOTHING`.

**Observability**

- DB access traced from Go via OTel instrumentation.
- Postgres integration in Datadog for query performance and resource metrics.

---

## 5. Observability Design

### 5.1 Tracing

- Use OpenTelemetry SDK in all Go services (`vote-api`, `results-api`, `tally-worker`).
- HTTP server middleware to create a span per request.
- DB instrumentation (`otelsql` or driver‑specific) for per‑query spans with attributes like `db.system`, `db.statement`, `db.operation`.
- Export path: OTel API calls inside each Go service are implemented by the Datadog Go SDK and shipped to the Datadog Agent's trace-agent endpoint directly (see §3.1). An OTel Collector is not part of the trace path.
- Graceful shutdown: each Go service must trap `SIGTERM`/`SIGINT` and call `provider.Shutdown(ctx)` on the registered `TracerProvider` before exit. Skipping this drops the final batch of spans silently — a classic OTel demo footgun.

### 5.2 Metrics

- Transport: metrics are pushed from each Go service to the Datadog Agent via the Datadog Go SDK — no `/metrics` scrape endpoint. The OTel Meter API may be used in application code; the SDK forwards readings to the Agent's dogstatsd port (`8125` by default).
- Standard service metrics:
  - HTTP request count/latency/error rate per endpoint.
  - DB connection metrics.
  - Worker/tally metrics (`tally_runs_total`, `tally_duration_seconds`, `tally_last_success_timestamp`).
- Temporal platform metrics if using Temporal:
  - Workflow execution metrics, poller metrics, service latency, persistence error counts surfaced in Datadog via integration.

### 5.3 Logging

- Structured logs (JSON) from all services with:
  - `service`, `env`, `version`
  - `trace_id`, `span_id` (if available)
  - Business fields: `poll_id`, `choice`, `user_id`
- Logs shipped via Datadog Agent.
- Optionally, trace‑aware Postgres logging into OTel/Datadog.

### 5.4 Dashboards & Alerts (example)

- Dashboards:
  - “Vote API” – latency, error rate, QPS, DB time.
  - “Results API” – latency, error rate, QPS.
  - “Tally Worker” – run frequency, failures, runtime.
  - “Temporal Platform” – key Temporal metrics if enabled.

- Alerts:
  - High error rate for `POST /vote`.
  - p95 latency threshold breaches.
  - `tally_last_success_timestamp` too old (tally stuck).
  - Temporal persistence errors or poller timeouts above threshold.

---

## 6. Deployment and Local Dev

- All components containerized (Docker).
- Local orchestration via `docker-compose`:
  - `frontend`, `vote-api`, `results-api`, `tally-worker`, `postgres`, `datadog-agent`, and optional `temporal`/`temporal-ui`.
- Configuration via environment variables:
  - DB connection strings.
  - `DD_AGENT_HOST`, `DD_TRACE_AGENT_PORT` (default `8126`), `DD_SERVICE`, `DD_ENV`, `DD_VERSION`.
  - Datadog API key (on the Agent), service/env tags.
  - Temporal namespace / endpoint if enabled.

---

## 7. Future Extensions (Optional)

- Add authentication (e.g., simple token or OAuth proxy).
- Add multiple polls with create/close lifecycle modeled in Temporal.
- Add a WebSocket or SSE results channel for “live” updates.
- Extend to more options and richer result analytics.
