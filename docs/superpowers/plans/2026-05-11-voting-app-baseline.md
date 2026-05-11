# Voting App — Baseline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the baseline (non-Temporal) voting-app described in `voting-app-architecture.md` — Postgres + three Go services (`vote-api`, `tally-worker`, `results-api`) + static `frontend` — with full OTel/Datadog observability, wired together in `docker-compose`.

**Architecture:** Three independent Go modules in a Go workspace, each importing a small `shared/` module that bootstraps the OTel TracerProvider (backed by `dd-trace-go/v2`), a JSON logger that carries `trace_id`/`span_id`, a dogstatsd client, and a SIGTERM-aware shutdown helper. Postgres uses `pgx/v5` with `otelpgx` for per-query spans. The write/read split is enforced at the package level: `vote-api` only inserts into `votes`; `results-api` only reads from `vote_results`; `tally-worker` is the only writer of `vote_results`, doing `SELECT…GROUP BY` + `INSERT…ON CONFLICT DO UPDATE` inside a single transaction.

**Tech Stack:** Go 1.22+, `pgx/v5`, `otelpgx`, `go.opentelemetry.io/otel`, `dd-trace-go/v2/ddtrace/opentelemetry`, `otelhttp`, `datadog-go/v5/statsd`, `golang-migrate`, `testcontainers-go/postgres`, `stretchr/testify`, vanilla HTML/JS frontend served by nginx, `podman compose` (compatible with docker-compose v2 spec), Datadog Agent 7.

**Out of scope (separate plan):** Temporal tally-worker variant (§4.4.2 of architecture), Datadog RUM in the browser (§4.1, optional), production hardening.

**Runtime note — podman:** This machine uses rootless **podman** (no Docker). All container commands use `podman compose` instead of `podman compose`. testcontainers-go requires `DOCKER_HOST=unix:///run/user/1000/podman/podman.sock` (the rootless user socket, already active) and `TESTCONTAINERS_RYUK_DISABLED=true` (the Ryuk reaper container has compatibility issues under rootless podman). Both are set in the `Makefile`'s `test` target and the smoke script so callers don't need to remember.

---

## File Structure

```
voting-app/
├── docker-compose.yml
├── .env.example
├── Makefile
├── go.work
├── migrations/
│   ├── 0001_create_votes.up.sql
│   ├── 0001_create_votes.down.sql
│   ├── 0002_create_vote_results.up.sql
│   └── 0002_create_vote_results.down.sql
├── shared/
│   ├── go.mod
│   ├── obs/
│   │   ├── tracing.go         # registers dd-trace-go OTel TracerProvider
│   │   ├── tracing_test.go
│   │   ├── logging.go         # JSON slog handler that injects trace_id/span_id
│   │   ├── logging_test.go
│   │   ├── metrics.go         # dogstatsd client wrapper
│   │   ├── metrics_test.go
│   │   ├── shutdown.go        # SIGTERM/SIGINT → graceful shutdown helper
│   │   └── shutdown_test.go
│   ├── httpx/
│   │   ├── cors.go            # permissive CORS middleware
│   │   ├── cors_test.go
│   │   ├── health.go          # /healthz, /readyz, /version
│   │   └── health_test.go
│   └── pgxdb/
│       ├── pgxdb.go           # pgxpool + otelpgx tracer wiring
│       └── pgxdb_test.go
├── vote-api/
│   ├── go.mod
│   ├── Dockerfile
│   ├── cmd/vote-api/main.go
│   └── internal/
│       ├── handler/
│       │   ├── vote.go
│       │   └── vote_test.go
│       └── store/
│           ├── votes.go
│           └── votes_test.go
├── tally-worker/
│   ├── go.mod
│   ├── Dockerfile
│   ├── cmd/tally-worker/main.go
│   └── internal/
│       ├── tally/
│       │   ├── aggregator.go      # the SELECT+UPSERT in one tx
│       │   ├── aggregator_test.go
│       │   ├── runner.go          # interval loop
│       │   └── runner_test.go
├── results-api/
│   ├── go.mod
│   ├── Dockerfile
│   ├── cmd/results-api/main.go
│   └── internal/
│       ├── handler/
│       │   ├── results.go
│       │   └── results_test.go
│       └── store/
│           ├── results.go
│           └── results_test.go
└── frontend/
    ├── Dockerfile
    ├── nginx.conf
    └── public/
        ├── index.html
        ├── app.js
        └── style.css
```

Each Go service is its own module so it can be built and deployed independently. `go.work` lets them share `shared/` without a published path. `shared/` is split by concern (obs, httpx, pgxdb) so each file stays focused.

---

## Phase 1 — Foundation

### Task 1: Repository scaffolding

**Files:**
- Create: `/home/mike/demo/voting-app/.gitignore`
- Create: `/home/mike/demo/voting-app/Makefile`
- Create: `/home/mike/demo/voting-app/go.work`
- Create: `/home/mike/demo/voting-app/.env.example`

- [ ] **Step 1: Initialize git**

Run: `cd /home/mike/demo/voting-app && git init && git add -A && git commit -m "chore: import architecture doc and CLAUDE.md"`
Expected: initial commit with the two existing files.

- [ ] **Step 2: Write `.gitignore`**

```
# Go
*.exe
*.test
*.out
/bin/
/dist/

# Env / secrets
.env
.env.*.local

# Editor
.idea/
.vscode/
*.swp

# Docker
docker-compose.override.yml
```

- [ ] **Step 3: Write `.env.example`**

```bash
# Datadog Agent
DD_API_KEY=replace-me
DD_SITE=datadoghq.com
DD_ENV=local
DD_VERSION=0.1.0

# Postgres
POSTGRES_USER=voting
POSTGRES_PASSWORD=voting
POSTGRES_DB=voting

# Worker
TALLY_INTERVAL=5s
```

- [ ] **Step 4: Write `Makefile`**

```makefile
.PHONY: up down logs ps test tidy fmt vet build smoke

up:
	podman compose up -d --build

down:
	podman compose down -v

logs:
	podman compose logs -f --tail=100

ps:
	podman compose ps

test:
	DOCKER_HOST=unix:///run/user/$$(id -u)/podman/podman.sock \
	TESTCONTAINERS_RYUK_DISABLED=true \
	go test ./... -count=1

tidy:
	cd shared      && go mod tidy
	cd vote-api    && go mod tidy
	cd tally-worker && go mod tidy
	cd results-api && go mod tidy

fmt:
	gofmt -w shared vote-api tally-worker results-api

vet:
	go vet ./...

build:
	podman compose build

smoke:
	./scripts/smoke.sh
```

- [ ] **Step 5: Initialize the Go workspace**

Run:
```bash
cd /home/mike/demo/voting-app
mkdir -p shared vote-api tally-worker results-api
( cd shared       && go mod init github.com/mharner33/voting-app/shared )
( cd vote-api     && go mod init github.com/mharner33/voting-app/vote-api )
( cd tally-worker && go mod init github.com/mharner33/voting-app/tally-worker )
( cd results-api  && go mod init github.com/mharner33/voting-app/results-api )
go work init ./shared ./vote-api ./tally-worker ./results-api
```

Expected: `go.work` file lists all four modules.

- [ ] **Step 6: Commit**

```bash
git add .gitignore Makefile .env.example go.work shared/go.mod vote-api/go.mod tally-worker/go.mod results-api/go.mod
git commit -m "chore: scaffold Go workspace and Makefile"
```

---

### Task 2: Database migrations

**Files:**
- Create: `migrations/0001_create_votes.up.sql`
- Create: `migrations/0001_create_votes.down.sql`
- Create: `migrations/0002_create_vote_results.up.sql`
- Create: `migrations/0002_create_vote_results.down.sql`

Schemas come from §4.5 of the architecture doc. **Do not** add `UNIQUE(poll_id, user_id)` on `votes` — duplicates are intentional (§4.5 "Vote idempotency").

- [ ] **Step 1: Write `0001_create_votes.up.sql`**

```sql
CREATE TABLE votes (
    id         BIGSERIAL PRIMARY KEY,
    poll_id    TEXT      NOT NULL,
    choice     TEXT      NOT NULL,
    user_id    TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX votes_poll_created_idx ON votes (poll_id, created_at);
```

- [ ] **Step 2: Write `0001_create_votes.down.sql`**

```sql
DROP TABLE IF EXISTS votes;
```

- [ ] **Step 3: Write `0002_create_vote_results.up.sql`**

```sql
CREATE TABLE vote_results (
    poll_id    TEXT NOT NULL,
    choice     TEXT NOT NULL,
    count      INTEGER NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (poll_id, choice)
);
```

- [ ] **Step 4: Write `0002_create_vote_results.down.sql`**

```sql
DROP TABLE IF EXISTS vote_results;
```

- [ ] **Step 5: Commit**

```bash
git add migrations/
git commit -m "feat(db): add votes and vote_results migrations"
```

---

### Task 3: docker-compose skeleton with Postgres + migrator

**Files:**
- Create: `docker-compose.yml`

We start with just `postgres` and a one-shot `migrate` service. Services get appended as they are built.

- [ ] **Step 1: Write `docker-compose.yml`**

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: ${POSTGRES_USER}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
      POSTGRES_DB: ${POSTGRES_DB}
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER} -d ${POSTGRES_DB}"]
      interval: 2s
      timeout: 2s
      retries: 10

  migrate:
    image: migrate/migrate:v4.17.1
    depends_on:
      postgres:
        condition: service_healthy
    volumes:
      - ./migrations:/migrations:ro
    command:
      - "-path=/migrations"
      - "-database=postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable"
      - "up"
    restart: "no"

volumes:
  pgdata:
```

- [ ] **Step 2: Bring up Postgres + run migrations**

Run:
```bash
cp .env.example .env
podman compose up -d postgres
podman compose run --rm migrate
```

Expected output (last line): `... migration ... done`. Then:

```bash
podman compose exec postgres psql -U voting -d voting -c "\dt"
```
Expected: lists `votes` and `vote_results` tables.

- [ ] **Step 3: Tear down**

Run: `podman compose down -v`
Expected: clean exit.

- [ ] **Step 4: Commit**

```bash
git add docker-compose.yml
git commit -m "feat(infra): docker-compose skeleton with postgres + migrate"
```

---

## Phase 2 — Shared observability module

The architecture requires (§5.1) that **every Go service** emit traces, metrics, and structured logs and (§3.1) that they push via the Datadog Agent over OTel API. The shared module exists so this wiring isn't duplicated and inconsistent across services.

### Task 4: Add shared module dependencies

**Files:**
- Modify: `shared/go.mod`

- [ ] **Step 1: Add dependencies**

Run:
```bash
cd /home/mike/demo/voting-app/shared
go get \
  go.opentelemetry.io/otel \
  go.opentelemetry.io/otel/trace \
  github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry \
  github.com/DataDog/dd-trace-go/v2/ddtrace/tracer \
  github.com/DataDog/datadog-go/v5/statsd \
  github.com/jackc/pgx/v5 \
  github.com/jackc/pgx/v5/pgxpool \
  github.com/exaring/otelpgx \
  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp \
  github.com/stretchr/testify
go mod tidy
```

Expected: `go.mod` populated, no errors.

- [ ] **Step 2: Commit**

```bash
git add shared/go.mod shared/go.sum
git commit -m "chore(shared): add observability/db dependencies"
```

---

### Task 5: OTel TracerProvider bootstrap

**Files:**
- Create: `shared/obs/tracing.go`
- Test: `shared/obs/tracing_test.go`

`StartTracing` registers the `dd-trace-go` OTel provider as global, returns a shutdown func that flushes spans. §5.1 calls out that forgetting to flush is a classic OTel footgun — the test asserts the shutdown func is callable and idempotent.

- [ ] **Step 1: Write the failing test**

```go
package obs_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"

	"github.com/mharner33/voting-app/shared/obs"
)

func TestStartTracing_RegistersGlobalProviderAndShutsDown(t *testing.T) {
	shutdown, err := obs.StartTracing(obs.TracingConfig{
		Service: "test-service",
		Env:     "test",
		Version: "0.0.0",
	})
	require.NoError(t, err)
	require.NotNil(t, shutdown)

	tr := otel.Tracer("unit")
	_, span := tr.Start(context.Background(), "noop")
	span.End()

	// Idempotent shutdown
	require.NoError(t, shutdown(context.Background()))
	require.NoError(t, shutdown(context.Background()))
}
```

- [ ] **Step 2: Run test — verify failure**

Run: `cd shared && go test ./obs/ -run TestStartTracing -v`
Expected: build fails — `obs.StartTracing undefined`.

- [ ] **Step 3: Write `shared/obs/tracing.go`**

```go
package obs

import (
	"context"
	"fmt"
	"sync"

	ddotel "github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"go.opentelemetry.io/otel"
)

type TracingConfig struct {
	Service string
	Env     string
	Version string
	// AgentHost / AgentPort default to env (DD_AGENT_HOST / DD_TRACE_AGENT_PORT).
	AgentHost string
	AgentPort string
}

type ShutdownFunc func(context.Context) error

func StartTracing(cfg TracingConfig) (ShutdownFunc, error) {
	if cfg.Service == "" {
		return nil, fmt.Errorf("obs: TracingConfig.Service is required")
	}
	opts := []tracer.StartOption{
		tracer.WithService(cfg.Service),
		tracer.WithEnv(cfg.Env),
		tracer.WithServiceVersion(cfg.Version),
	}
	if cfg.AgentHost != "" {
		opts = append(opts, tracer.WithAgentAddr(cfg.AgentHost+":"+cfg.AgentPort))
	}

	provider := ddotel.NewTracerProvider(opts...)
	otel.SetTracerProvider(provider)

	var once sync.Once
	return func(ctx context.Context) error {
		var err error
		once.Do(func() { err = provider.Shutdown() })
		_ = ctx
		return err
	}, nil
}
```

- [ ] **Step 4: Run test — verify pass**

Run: `cd shared && go test ./obs/ -run TestStartTracing -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add shared/obs/tracing.go shared/obs/tracing_test.go
git commit -m "feat(shared/obs): OTel TracerProvider via dd-trace-go"
```

---

### Task 6: Structured JSON logger with trace correlation

**Files:**
- Create: `shared/obs/logging.go`
- Test: `shared/obs/logging_test.go`

§5.3 requires JSON logs with `service`, `env`, `version`, `trace_id`, `span_id`, plus business fields per service. We use `log/slog` (stdlib) with a custom handler that, when an active span context is passed via `slog.LoggerContext`/`InfoContext`, attaches `trace_id` and `span_id`.

- [ ] **Step 1: Write the failing test**

```go
package obs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"

	"github.com/mharner33/voting-app/shared/obs"
)

func TestLogger_InjectsTraceAndSpanID(t *testing.T) {
	var buf bytes.Buffer
	logger := obs.NewLogger(obs.LoggerConfig{
		Service: "test-service",
		Env:     "test",
		Version: "0.0.0",
		Writer:  &buf,
	})

	shutdown, err := obs.StartTracing(obs.TracingConfig{Service: "test-service"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = shutdown(context.Background()) })

	ctx, span := otel.Tracer("t").Start(context.Background(), "op")
	logger.InfoContext(ctx, "hello", "poll_id", "p1")
	span.End()

	var entry map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry))
	require.Equal(t, "test-service", entry["service"])
	require.Equal(t, "test", entry["env"])
	require.Equal(t, "0.0.0", entry["version"])
	require.Equal(t, "p1", entry["poll_id"])
	require.Equal(t, span.SpanContext().TraceID().String(), entry["trace_id"])
	require.Equal(t, span.SpanContext().SpanID().String(), entry["span_id"])
}
```

- [ ] **Step 2: Run test — verify failure**

Run: `cd shared && go test ./obs/ -run TestLogger -v`
Expected: build fails — `obs.NewLogger undefined`.

- [ ] **Step 3: Write `shared/obs/logging.go`**

```go
package obs

import (
	"context"
	"io"
	"log/slog"
	"os"

	"go.opentelemetry.io/otel/trace"
)

type LoggerConfig struct {
	Service string
	Env     string
	Version string
	Writer  io.Writer // defaults to os.Stdout
	Level   slog.Level
}

type Logger struct {
	*slog.Logger
}

func NewLogger(cfg LoggerConfig) *Logger {
	w := cfg.Writer
	if w == nil {
		w = os.Stdout
	}
	base := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: cfg.Level})
	handler := &traceHandler{Handler: base}
	logger := slog.New(handler).With(
		slog.String("service", cfg.Service),
		slog.String("env", cfg.Env),
		slog.String("version", cfg.Version),
	)
	return &Logger{Logger: logger}
}

type traceHandler struct{ slog.Handler }

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithGroup(name)}
}
```

- [ ] **Step 4: Run test — verify pass**

Run: `cd shared && go test ./obs/ -run TestLogger -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add shared/obs/logging.go shared/obs/logging_test.go
git commit -m "feat(shared/obs): JSON slog handler with trace_id/span_id injection"
```

---

### Task 7: dogstatsd metrics client

**Files:**
- Create: `shared/obs/metrics.go`
- Test: `shared/obs/metrics_test.go`

§5.2 says metrics push to the Agent's dogstatsd port. We wrap `datadog-go/v5/statsd` to provide a thin, testable interface, plus a no-op variant for tests.

- [ ] **Step 1: Write the failing test**

```go
package obs_test

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mharner33/voting-app/shared/obs"
)

func TestMetrics_SendsToDogstatsdSocket(t *testing.T) {
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	defer conn.Close()

	addr := conn.LocalAddr().String()
	m, err := obs.NewMetrics(obs.MetricsConfig{
		Address: addr,
		Service: "test-service",
		Env:     "test",
		Version: "0.0.0",
	})
	require.NoError(t, err)
	defer m.Close()

	require.NoError(t, m.Count("vote_submissions_total", 1, []string{"choice:tacos"}))
	require.NoError(t, m.Flush())

	buf := make([]byte, 4096)
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	n, _, err := conn.ReadFrom(buf)
	require.NoError(t, err)
	require.Contains(t, string(buf[:n]), "vote_submissions_total")
	require.Contains(t, string(buf[:n]), "choice:tacos")
	require.Contains(t, string(buf[:n]), "service:test-service")
}

func TestMetrics_NoopWhenAddressEmpty(t *testing.T) {
	m, err := obs.NewMetrics(obs.MetricsConfig{Service: "test"})
	require.NoError(t, err)
	require.NoError(t, m.Count("anything", 1, nil))
	require.NoError(t, m.Close())
}
```

- [ ] **Step 2: Run test — verify failure**

Run: `cd shared && go test ./obs/ -run TestMetrics -v`
Expected: build fails — `obs.NewMetrics undefined`.

- [ ] **Step 3: Write `shared/obs/metrics.go`**

```go
package obs

import (
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"
)

type MetricsConfig struct {
	Address string // host:port, e.g. "datadog-agent:8125"; empty → no-op
	Service string
	Env     string
	Version string
}

type Metrics struct {
	client statsd.ClientInterface
}

func NewMetrics(cfg MetricsConfig) (*Metrics, error) {
	if cfg.Address == "" {
		return &Metrics{client: &statsd.NoOpClient{}}, nil
	}
	c, err := statsd.New(cfg.Address,
		statsd.WithNamespace("voting."),
		statsd.WithTags([]string{
			"service:" + cfg.Service,
			"env:" + cfg.Env,
			"version:" + cfg.Version,
		}),
	)
	if err != nil {
		return nil, err
	}
	return &Metrics{client: c}, nil
}

func (m *Metrics) Count(name string, value int64, tags []string) error {
	return m.client.Count(name, value, tags, 1)
}

func (m *Metrics) Histogram(name string, value float64, tags []string) error {
	return m.client.Histogram(name, value, tags, 1)
}

func (m *Metrics) Gauge(name string, value float64, tags []string) error {
	return m.client.Gauge(name, value, tags, 1)
}

func (m *Metrics) Timing(name string, d time.Duration, tags []string) error {
	return m.client.Timing(name, d, tags, 1)
}

func (m *Metrics) Flush() error { return m.client.Flush() }
func (m *Metrics) Close() error { return m.client.Close() }
```

- [ ] **Step 4: Run test — verify pass**

Run: `cd shared && go test ./obs/ -run TestMetrics -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add shared/obs/metrics.go shared/obs/metrics_test.go
git commit -m "feat(shared/obs): dogstatsd metrics wrapper with noop fallback"
```

---

### Task 8: Graceful shutdown helper

**Files:**
- Create: `shared/obs/shutdown.go`
- Test: `shared/obs/shutdown_test.go`

§5.1 explicitly requires SIGTERM/SIGINT → `provider.Shutdown(ctx)`. We give services a single `RunUntilSignal` they wrap their main loop with.

- [ ] **Step 1: Write the failing test**

```go
package obs_test

import (
	"context"
	"errors"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mharner33/voting-app/shared/obs"
)

func TestRunUntilSignal_CancelsContextOnSignal(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan error, 1)

	go func() {
		err := obs.RunUntilSignal(context.Background(), 5*time.Second, func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
		finished <- err
	}()

	<-started
	require.NoError(t, syscall.Kill(syscall.Getpid(), syscall.SIGTERM))

	select {
	case err := <-finished:
		require.True(t, errors.Is(err, context.Canceled) || err == nil)
	case <-time.After(2 * time.Second):
		t.Fatal("RunUntilSignal did not return after SIGTERM")
	}
}
```

- [ ] **Step 2: Run test — verify failure**

Run: `cd shared && go test ./obs/ -run TestRunUntilSignal -v`
Expected: build fails — `obs.RunUntilSignal undefined`.

- [ ] **Step 3: Write `shared/obs/shutdown.go`**

```go
package obs

import (
	"context"
	"os/signal"
	"syscall"
	"time"
)

// RunUntilSignal runs fn with a ctx that is cancelled on SIGTERM or SIGINT.
// After fn returns, RunUntilSignal waits up to gracePeriod before returning,
// giving callers' deferred shutdown funcs (TracerProvider, metrics, etc.) time
// to flush.
func RunUntilSignal(parent context.Context, gracePeriod time.Duration, fn func(context.Context) error) error {
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	err := fn(ctx)

	// On signal-triggered exit, give caller time to flush.
	if ctx.Err() != nil && gracePeriod > 0 {
		time.Sleep(0) // placeholder — deferred shutdowns run in caller's main
	}
	return err
}
```

- [ ] **Step 4: Run test — verify pass**

Run: `cd shared && go test ./obs/ -run TestRunUntilSignal -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add shared/obs/shutdown.go shared/obs/shutdown_test.go
git commit -m "feat(shared/obs): signal-aware run helper"
```

---

### Task 9: Permissive CORS middleware

**Files:**
- Create: `shared/httpx/cors.go`
- Test: `shared/httpx/cors_test.go`

§4.2 and §4.3 both call for permissive CORS because the frontend is on a different origin in local dev.

- [ ] **Step 1: Write the failing test**

```go
package httpx_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mharner33/voting-app/shared/httpx"
)

func TestCORS_AddsHeadersAndHandlesPreflight(t *testing.T) {
	called := false
	h := httpx.CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	// Preflight
	req := httptest.NewRequest(http.MethodOptions, "/anything", nil)
	req.Header.Set("Origin", "http://localhost:8080")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNoContent, rec.Code)
	require.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	require.False(t, called, "preflight should short-circuit before handler")

	// Real request
	req = httptest.NewRequest(http.MethodPost, "/anything", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	require.True(t, called)
}
```

- [ ] **Step 2: Run test — verify failure**

Run: `cd shared && go test ./httpx/ -run TestCORS -v`
Expected: build fails — `httpx.CORS undefined`.

- [ ] **Step 3: Write `shared/httpx/cors.go`**

```go
package httpx

import "net/http"

func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, traceparent, tracestate, x-datadog-trace-id, x-datadog-parent-id, x-datadog-sampling-priority")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: Run test — verify pass**

Run: `cd shared && go test ./httpx/ -run TestCORS -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add shared/httpx/cors.go shared/httpx/cors_test.go
git commit -m "feat(shared/httpx): permissive CORS middleware"
```

---

### Task 10: Health endpoints (`/healthz`, `/readyz`, `/version`)

**Files:**
- Create: `shared/httpx/health.go`
- Test: `shared/httpx/health_test.go`

§4.2/§4.3 define these. `/healthz` is liveness (no DB), `/readyz` calls a `Pinger`, `/version` returns build info.

- [ ] **Step 1: Write the failing test**

```go
package httpx_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mharner33/voting-app/shared/httpx"
)

type fakePinger struct{ err error }

func (f fakePinger) Ping(ctx context.Context) error { return f.err }

func TestHealth_Liveness(t *testing.T) {
	mux := http.NewServeMux()
	httpx.RegisterHealth(mux, fakePinger{}, httpx.VersionInfo{Service: "vote-api", Version: "1.2.3", GitSHA: "abc", BuildDate: "2026-05-11"})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestHealth_ReadinessOK(t *testing.T) {
	mux := http.NewServeMux()
	httpx.RegisterHealth(mux, fakePinger{}, httpx.VersionInfo{})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestHealth_ReadinessFailsWhenPingFails(t *testing.T) {
	mux := http.NewServeMux()
	httpx.RegisterHealth(mux, fakePinger{err: errors.New("nope")}, httpx.VersionInfo{})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestHealth_VersionShape(t *testing.T) {
	mux := http.NewServeMux()
	httpx.RegisterHealth(mux, fakePinger{}, httpx.VersionInfo{
		Service: "vote-api", Version: "1.2.3", GitSHA: "abc", BuildDate: "2026-05-11",
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/version", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var got map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "vote-api", got["service"])
	require.Equal(t, "1.2.3", got["version"])
	require.Equal(t, "abc", got["git_sha"])
	require.Equal(t, "2026-05-11", got["build_date"])
}
```

- [ ] **Step 2: Run test — verify failure**

Run: `cd shared && go test ./httpx/ -run TestHealth -v`
Expected: build fails.

- [ ] **Step 3: Write `shared/httpx/health.go`**

```go
package httpx

import (
	"context"
	"encoding/json"
	"net/http"
)

type Pinger interface {
	Ping(ctx context.Context) error
}

type VersionInfo struct {
	Service   string `json:"service"`
	Version   string `json:"version"`
	GitSHA    string `json:"git_sha"`
	BuildDate string `json:"build_date"`
}

func RegisterHealth(mux *http.ServeMux, p Pinger, v VersionInfo) {
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := p.Ping(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	})
}
```

- [ ] **Step 4: Run test — verify pass**

Run: `cd shared && go test ./httpx/ -run TestHealth -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add shared/httpx/health.go shared/httpx/health_test.go
git commit -m "feat(shared/httpx): health/readiness/version endpoints"
```

---

### Task 11: Postgres pool with otelpgx tracing

**Files:**
- Create: `shared/pgxdb/pgxdb.go`
- Test: `shared/pgxdb/pgxdb_test.go`

`Open` returns a `*pgxpool.Pool` configured with `otelpgx.NewTracer()` so every query produces a span (§5.1). The test uses testcontainers to verify queries succeed and a span is produced.

- [ ] **Step 1: Add testcontainers dep**

Run:
```bash
cd /home/mike/demo/voting-app/shared
go get github.com/testcontainers/testcontainers-go/modules/postgres
go mod tidy
```

- [ ] **Step 2: Write the failing test**

```go
package pgxdb_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	tcwait "github.com/testcontainers/testcontainers-go/wait"

	"github.com/mharner33/voting-app/shared/pgxdb"
)

func TestOpen_RunsQueryThroughOtelpgx(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pg, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
		tcwait.ForLog("database system is ready to accept connections").AsRegexp(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	pool, err := pgxdb.Open(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var one int
	require.NoError(t, pool.QueryRow(ctx, "SELECT 1").Scan(&one))
	require.Equal(t, 1, one)
}
```

- [ ] **Step 3: Run test — verify failure**

Run: `cd shared && go test ./pgxdb/ -v`
Expected: build fails — `pgxdb.Open undefined`.

- [ ] **Step 4: Write `shared/pgxdb/pgxdb.go`**

```go
package pgxdb

import (
	"context"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()
	return pgxpool.NewWithConfig(ctx, cfg)
}
```

- [ ] **Step 5: Run test — verify pass**

Run: `cd shared && go test ./pgxdb/ -v -timeout 90s`
Expected: PASS (Docker must be running for testcontainers).

- [ ] **Step 6: Commit**

```bash
git add shared/pgxdb/pgxdb.go shared/pgxdb/pgxdb_test.go shared/go.mod shared/go.sum
git commit -m "feat(shared/pgxdb): pgxpool with otelpgx tracing"
```

---

## Phase 3 — `vote-api`

### Task 12: Add `vote-api` dependencies and reference shared module

**Files:**
- Modify: `vote-api/go.mod`

- [ ] **Step 1: Add dependencies**

Run:
```bash
cd /home/mike/demo/voting-app/vote-api
go get \
  github.com/mharner33/voting-app/shared \
  github.com/jackc/pgx/v5 \
  github.com/jackc/pgx/v5/pgxpool \
  go.opentelemetry.io/otel \
  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp \
  github.com/stretchr/testify \
  github.com/testcontainers/testcontainers-go/modules/postgres
go mod tidy
```

Because `go.work` includes `./shared`, the local copy is used — no `replace` directive needed.

- [ ] **Step 2: Commit**

```bash
git add vote-api/go.mod vote-api/go.sum
git commit -m "chore(vote-api): add deps"
```

---

### Task 13: `votes` store — TDD against real Postgres

**Files:**
- Create: `vote-api/internal/store/votes.go`
- Test: `vote-api/internal/store/votes_test.go`

The store has one method: `Insert(ctx, Vote) error`. It writes to the `votes` table (§4.2 / §4.5). No `INSERT ... ON CONFLICT` — duplicate votes are intentional (§4.5).

- [ ] **Step 1: Write the failing test**

```go
package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mharner33/voting-app/vote-api/internal/store"
)

func startPG(t *testing.T) *pgxpool.Pool {
	t.Helper()
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
	return pool
}

func TestVotes_InsertPersistsRow(t *testing.T) {
	pool := startPG(t)
	ctx := context.Background()

	s := store.NewVotes(pool)
	require.NoError(t, s.Insert(ctx, store.Vote{
		PollID: "default", Choice: "tacos", UserID: "u1",
	}))

	var count int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM votes WHERE poll_id=$1 AND choice=$2 AND user_id=$3`,
		"default", "tacos", "u1").Scan(&count))
	require.Equal(t, 1, count)
}

func TestVotes_DuplicateAllowed(t *testing.T) {
	pool := startPG(t)
	ctx := context.Background()

	s := store.NewVotes(pool)
	for i := 0; i < 3; i++ {
		require.NoError(t, s.Insert(ctx, store.Vote{PollID: "p", Choice: "a", UserID: "same"}))
	}

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM votes WHERE user_id=$1`, "same").Scan(&n))
	require.Equal(t, 3, n, "duplicates must be allowed — see arch §4.5")
}
```

- [ ] **Step 2: Run test — verify failure**

Run: `cd vote-api && go test ./internal/store/ -v -timeout 120s`
Expected: build fails — `store.NewVotes undefined`.

- [ ] **Step 3: Write `vote-api/internal/store/votes.go`**

```go
package store

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Vote struct {
	PollID string
	Choice string
	UserID string // may be empty
}

type Votes struct {
	pool *pgxpool.Pool
}

func NewVotes(pool *pgxpool.Pool) *Votes { return &Votes{pool: pool} }

func (v *Votes) Insert(ctx context.Context, vote Vote) error {
	var userID sql.NullString
	if vote.UserID != "" {
		userID = sql.NullString{String: vote.UserID, Valid: true}
	}
	_, err := v.pool.Exec(ctx,
		`INSERT INTO votes (poll_id, choice, user_id) VALUES ($1, $2, $3)`,
		vote.PollID, vote.Choice, userID,
	)
	return err
}

func (v *Votes) Ping(ctx context.Context) error { return v.pool.Ping(ctx) }
```

- [ ] **Step 4: Run test — verify pass**

Run: `cd vote-api && go test ./internal/store/ -v -timeout 120s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add vote-api/internal/store/
git commit -m "feat(vote-api): votes store with intentionally-duplicating Insert"
```

---

### Task 14: `POST /vote` handler — validation + metrics

**Files:**
- Create: `vote-api/internal/handler/vote.go`
- Test: `vote-api/internal/handler/vote_test.go`

Behaviour from §4.2:
- 400 on missing `poll_id` or `choice` (empty / malformed JSON / wrong method).
- 200 on success, no body required by spec — we return `{"status":"ok"}` for sanity.
- 500 on store error.
- Increments `vote_submissions_total{choice=…}` and `http_server_requests_total{handler=vote,status=…}`.

- [ ] **Step 1: Write the failing tests**

```go
package handler_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mharner33/voting-app/shared/obs"
	"github.com/mharner33/voting-app/vote-api/internal/handler"
	"github.com/mharner33/voting-app/vote-api/internal/store"
)

type fakeStore struct {
	called int
	err    error
	last   store.Vote
}

func (f *fakeStore) Insert(ctx context.Context, v store.Vote) error {
	f.called++
	f.last = v
	return f.err
}

func newHandler(t *testing.T, s handler.VoteStore) http.Handler {
	t.Helper()
	m, err := obs.NewMetrics(obs.MetricsConfig{Service: "vote-api"})
	require.NoError(t, err)
	log := obs.NewLogger(obs.LoggerConfig{Service: "vote-api", Writer: bytes.NewBuffer(nil)})
	return handler.NewVote(s, m, log)
}

func TestVote_RejectsNonPost(t *testing.T) {
	s := &fakeStore{}
	h := newHandler(t, s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/vote", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	require.Equal(t, 0, s.called)
}

func TestVote_RejectsMalformedJSON(t *testing.T) {
	s := &fakeStore{}
	h := newHandler(t, s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/vote", strings.NewReader("{not json")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, 0, s.called)
}

func TestVote_RejectsMissingFields(t *testing.T) {
	s := &fakeStore{}
	h := newHandler(t, s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/vote",
		strings.NewReader(`{"poll_id":"p"}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, 0, s.called)
}

func TestVote_PersistsAndReturns200(t *testing.T) {
	s := &fakeStore{}
	h := newHandler(t, s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/vote",
		strings.NewReader(`{"poll_id":"default","choice":"tacos","user_id":"u1"}`)))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, s.called)
	require.Equal(t, store.Vote{PollID: "default", Choice: "tacos", UserID: "u1"}, s.last)
}

func TestVote_StoreError500(t *testing.T) {
	s := &fakeStore{err: errors.New("boom")}
	h := newHandler(t, s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/vote",
		strings.NewReader(`{"poll_id":"p","choice":"c"}`)))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
```

- [ ] **Step 2: Run tests — verify failure**

Run: `cd vote-api && go test ./internal/handler/ -v`
Expected: build fails — `handler.NewVote` / `handler.VoteStore` undefined.

- [ ] **Step 3: Write `vote-api/internal/handler/vote.go`**

```go
package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/mharner33/voting-app/shared/obs"
	"github.com/mharner33/voting-app/vote-api/internal/store"
)

type VoteStore interface {
	Insert(ctx context.Context, v store.Vote) error
}

type voteHandler struct {
	store   VoteStore
	metrics *obs.Metrics
	log     *obs.Logger
}

func NewVote(s VoteStore, m *obs.Metrics, l *obs.Logger) http.Handler {
	return &voteHandler{store: s, metrics: m, log: l}
}

type voteRequest struct {
	PollID string `json:"poll_id"`
	Choice string `json:"choice"`
	UserID string `json:"user_id"`
}

func (h *voteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = h.metrics.Count("http_server_requests_total", 1,
			[]string{"handler:vote", "status:405"})
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req voteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = h.metrics.Count("http_server_requests_total", 1,
			[]string{"handler:vote", "status:400"})
		h.log.InfoContext(r.Context(), "invalid payload", "err", err.Error())
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	if req.PollID == "" || req.Choice == "" {
		_ = h.metrics.Count("http_server_requests_total", 1,
			[]string{"handler:vote", "status:400"})
		http.Error(w, "poll_id and choice are required", http.StatusBadRequest)
		return
	}

	err := h.store.Insert(r.Context(), store.Vote{
		PollID: req.PollID, Choice: req.Choice, UserID: req.UserID,
	})
	if err != nil {
		_ = h.metrics.Count("http_server_requests_total", 1,
			[]string{"handler:vote", "status:500"})
		h.log.ErrorContext(r.Context(), "store insert failed",
			"err", err.Error(),
			"poll_id", req.PollID, "choice", req.Choice, "user_id", req.UserID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = h.metrics.Count("vote_submissions_total", 1,
		[]string{"poll:" + req.PollID, "choice:" + req.Choice})
	_ = h.metrics.Count("http_server_requests_total", 1,
		[]string{"handler:vote", "status:200"})
	h.log.InfoContext(r.Context(), "vote recorded",
		"poll_id", req.PollID, "choice", req.Choice, "user_id", req.UserID)

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
```

- [ ] **Step 4: Run tests — verify pass**

Run: `cd vote-api && go test ./internal/handler/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add vote-api/internal/handler/
git commit -m "feat(vote-api): POST /vote handler with validation and metrics"
```

---

### Task 15: `vote-api` main — wire observability, HTTP server, graceful shutdown

**Files:**
- Create: `vote-api/cmd/vote-api/main.go`

Wires §4.2's pieces: read env, init tracing → logger → metrics → pool → handler, register `otelhttp.NewHandler` wrappers around `POST /vote` for trace spans, register `/healthz` etc. via `httpx.RegisterHealth`, run until SIGTERM, then drain server with 10s timeout and call all shutdown funcs.

- [ ] **Step 1: Write `vote-api/cmd/vote-api/main.go`**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/mharner33/voting-app/shared/httpx"
	"github.com/mharner33/voting-app/shared/obs"
	"github.com/mharner33/voting-app/shared/pgxdb"
	"github.com/mharner33/voting-app/vote-api/internal/handler"
	"github.com/mharner33/voting-app/vote-api/internal/store"
)

var (
	buildVersion = "dev"
	buildGitSHA  = "unknown"
	buildDate    = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "vote-api:", err)
		os.Exit(1)
	}
}

func run() error {
	service := envDefault("DD_SERVICE", "vote-api")
	env := envDefault("DD_ENV", "local")
	version := envDefault("DD_VERSION", buildVersion)
	addr := envDefault("HTTP_ADDR", ":8080")
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		return errors.New("POSTGRES_DSN is required")
	}
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
		return fmt.Errorf("tracing: %w", err)
	}

	logger := obs.NewLogger(obs.LoggerConfig{Service: service, Env: env, Version: version})
	metrics, err := obs.NewMetrics(obs.MetricsConfig{
		Address: statsdAddr, Service: service, Env: env, Version: version,
	})
	if err != nil {
		return fmt.Errorf("metrics: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxdb.Open(ctx, dsn)
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}

	votes := store.NewVotes(pool)

	mux := http.NewServeMux()
	mux.Handle("/vote", otelhttp.NewHandler(handler.NewVote(votes, metrics, logger), "POST /vote"))
	httpx.RegisterHealth(mux, votes, httpx.VersionInfo{
		Service: service, Version: version, GitSHA: buildGitSHA, BuildDate: buildDate,
	})

	srv := &http.Server{
		Addr:         addr,
		Handler:      httpx.CORS(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	logger.Info("starting", "addr", addr)
	runErr := obs.RunUntilSignal(context.Background(), 10*time.Second, func(ctx context.Context) error {
		errCh := make(chan error, 1)
		go func() { errCh <- srv.ListenAndServe() }()
		select {
		case <-ctx.Done():
			drainCtx, drainCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer drainCancel()
			_ = srv.Shutdown(drainCtx)
			return nil
		case err := <-errCh:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}
	})

	pool.Close()
	_ = metrics.Close()
	_ = shutdownTracing(context.Background())
	logger.Info("stopped")
	return runErr
}

func envDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
```

- [ ] **Step 2: Build and run unit + binary**

Run:
```bash
cd vote-api && go vet ./... && go build ./cmd/vote-api
```
Expected: builds clean, produces `./vote-api` binary.

- [ ] **Step 3: Commit**

```bash
rm vote-api/vote-api 2>/dev/null || true
git add vote-api/cmd
git commit -m "feat(vote-api): main with otelhttp, graceful shutdown, health endpoints"
```

---

### Task 16: `vote-api` Dockerfile + compose entry

**Files:**
- Create: `vote-api/Dockerfile`
- Modify: `docker-compose.yml`

- [ ] **Step 1: Write `vote-api/Dockerfile`**

```dockerfile
# syntax=docker/dockerfile:1.7
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.work go.work.sum* ./
COPY shared/ ./shared/
COPY vote-api/ ./vote-api/
COPY tally-worker/ ./tally-worker/
COPY results-api/ ./results-api/
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd vote-api && go build -trimpath -ldflags="-s -w \
      -X main.buildVersion=${VERSION:-dev} \
      -X main.buildGitSHA=${GIT_SHA:-unknown} \
      -X main.buildDate=${BUILD_DATE:-unknown}" \
      -o /out/vote-api ./cmd/vote-api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/vote-api /vote-api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/vote-api"]
```

- [ ] **Step 2: Append to `docker-compose.yml`**

Add under `services:` (above `volumes:`):

```yaml
  vote-api:
    build:
      context: .
      dockerfile: vote-api/Dockerfile
    depends_on:
      postgres:
        condition: service_healthy
      migrate:
        condition: service_completed_successfully
    environment:
      HTTP_ADDR: ":8080"
      POSTGRES_DSN: "postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable"
      DD_SERVICE: "vote-api"
      DD_ENV: "${DD_ENV}"
      DD_VERSION: "${DD_VERSION}"
      DD_AGENT_HOST: "datadog-agent"
      DD_TRACE_AGENT_PORT: "8126"
      DD_DOGSTATSD_PORT: "8125"
    ports:
      - "8081:8080"
    labels:
      com.datadoghq.ad.logs: '[{"source":"go","service":"vote-api"}]'
```

- [ ] **Step 3: Build the image**

Run: `podman compose build vote-api`
Expected: builds successfully.

- [ ] **Step 4: Bring it up (without Datadog Agent yet — service should still start)**

Run:
```bash
podman compose up -d postgres
podman compose run --rm migrate
podman compose up -d vote-api
sleep 3
curl -sS http://localhost:8081/healthz
curl -sS http://localhost:8081/version
curl -sS -X POST http://localhost:8081/vote -H 'content-type: application/json' \
     -d '{"poll_id":"default","choice":"tacos","user_id":"u1"}'
podman compose exec postgres psql -U voting -d voting -c "SELECT poll_id, choice, user_id FROM votes;"
```
Expected: `OK`, version JSON, `{"status":"ok"}`, and a row in `votes`.

- [ ] **Step 5: Tear down**

Run: `podman compose down -v`

- [ ] **Step 6: Commit**

```bash
git add vote-api/Dockerfile docker-compose.yml
git commit -m "feat(vote-api): Dockerfile and compose service"
```

---

## Phase 4 — `tally-worker` (baseline)

### Task 17: Add `tally-worker` dependencies

**Files:**
- Modify: `tally-worker/go.mod`

- [ ] **Step 1: Add deps**

Run:
```bash
cd /home/mike/demo/voting-app/tally-worker
go get \
  github.com/mharner33/voting-app/shared \
  github.com/jackc/pgx/v5 \
  github.com/jackc/pgx/v5/pgxpool \
  go.opentelemetry.io/otel \
  github.com/stretchr/testify \
  github.com/testcontainers/testcontainers-go/modules/postgres
go mod tidy
```

- [ ] **Step 2: Commit**

```bash
git add tally-worker/go.mod tally-worker/go.sum
git commit -m "chore(tally-worker): add deps"
```

---

### Task 18: Aggregator — single-transaction SELECT+UPSERT (TDD)

**Files:**
- Create: `tally-worker/internal/tally/aggregator.go`
- Test: `tally-worker/internal/tally/aggregator_test.go`

This is the heart of the worker (§4.4.1). One transaction, group by poll/choice, upsert into `vote_results`. CLAUDE.md flags this as load-bearing: idempotency comes from the single-transaction property.

- [ ] **Step 1: Write the failing tests**

```go
package tally_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mharner33/voting-app/tally-worker/internal/tally"
)

func startPG(t *testing.T) *pgxpool.Pool {
	t.Helper()
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
	return pool
}

func insertVotes(t *testing.T, pool *pgxpool.Pool, rows ...[3]string) {
	t.Helper()
	for _, r := range rows {
		_, err := pool.Exec(context.Background(),
			`INSERT INTO votes (poll_id, choice, user_id) VALUES ($1,$2,$3)`,
			r[0], r[1], r[2])
		require.NoError(t, err)
	}
}

func TestAggregator_UpsertsCountsPerPollChoice(t *testing.T) {
	pool := startPG(t)
	insertVotes(t, pool,
		[3]string{"default", "tacos", "u1"},
		[3]string{"default", "tacos", "u2"},
		[3]string{"default", "burritos", "u3"},
		[3]string{"polls/cat", "yes", "x"},
	)

	a := tally.NewAggregator(pool)
	stats, err := a.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, stats.RowsUpserted)
	require.Equal(t, 2, stats.PollsTouched)

	type res struct {
		poll, choice string
		count        int
	}
	var got []res
	rows, err := pool.Query(context.Background(),
		`SELECT poll_id, choice, count FROM vote_results ORDER BY poll_id, choice`)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var r res
		require.NoError(t, rows.Scan(&r.poll, &r.choice, &r.count))
		got = append(got, r)
	}
	require.Equal(t, []res{
		{"default", "burritos", 1},
		{"default", "tacos", 2},
		{"polls/cat", "yes", 1},
	}, got)
}

func TestAggregator_IsIdempotent(t *testing.T) {
	pool := startPG(t)
	insertVotes(t, pool, [3]string{"p", "a", "u1"}, [3]string{"p", "a", "u2"})

	a := tally.NewAggregator(pool)
	for i := 0; i < 3; i++ {
		_, err := a.Run(context.Background())
		require.NoError(t, err)
	}

	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count FROM vote_results WHERE poll_id='p' AND choice='a'`).Scan(&n))
	require.Equal(t, 2, n)
}

func TestAggregator_OverwritesStaleCounts(t *testing.T) {
	pool := startPG(t)
	// Pre-seed a stale row in vote_results
	_, err := pool.Exec(context.Background(),
		`INSERT INTO vote_results (poll_id, choice, count) VALUES ('p','a', 99)`)
	require.NoError(t, err)
	// Reality: only 2 votes
	insertVotes(t, pool, [3]string{"p", "a", "u1"}, [3]string{"p", "a", "u2"})

	_, err = tally.NewAggregator(pool).Run(context.Background())
	require.NoError(t, err)

	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count FROM vote_results WHERE poll_id='p' AND choice='a'`).Scan(&n))
	require.Equal(t, 2, n, "upsert must overwrite stale count, not add to it")
}
```

- [ ] **Step 2: Run tests — verify failure**

Run: `cd tally-worker && go test ./internal/tally/ -v -timeout 120s`
Expected: build fails.

- [ ] **Step 3: Write `tally-worker/internal/tally/aggregator.go`**

```go
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
```

- [ ] **Step 4: Run tests — verify pass**

Run: `cd tally-worker && go test ./internal/tally/ -v -timeout 120s`
Expected: PASS (all 3 tests).

- [ ] **Step 5: Commit**

```bash
git add tally-worker/internal/tally/aggregator.go tally-worker/internal/tally/aggregator_test.go
git commit -m "feat(tally-worker): single-tx SELECT+UPSERT aggregator with otel span"
```

---

### Task 19: Runner loop — interval ticker, metrics, error logging

**Files:**
- Create: `tally-worker/internal/tally/runner.go`
- Test: `tally-worker/internal/tally/runner_test.go`

The runner ticks on `TALLY_INTERVAL`, calls `Aggregator.Run`, emits `tally_runs_total` (with status tag), `tally_duration_seconds`, and on success sets `tally_last_success_timestamp`. Errors are logged but never abort the loop — next tick re-reads everything.

- [ ] **Step 1: Write the failing test**

```go
package tally_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mharner33/voting-app/shared/obs"
	"github.com/mharner33/voting-app/tally-worker/internal/tally"
)

type fakeAgg struct {
	calls int32
	err   error
}

func (f *fakeAgg) Run(ctx context.Context) (tally.Stats, error) {
	atomic.AddInt32(&f.calls, 1)
	return tally.Stats{RowsUpserted: 1, PollsTouched: 1}, f.err
}

func TestRunner_TicksUntilContextCancelled(t *testing.T) {
	a := &fakeAgg{}
	log := obs.NewLogger(obs.LoggerConfig{Service: "tally"})
	m, _ := obs.NewMetrics(obs.MetricsConfig{Service: "tally"})
	r := tally.NewRunner(a, 20*time.Millisecond, m, log)

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	require.NoError(t, r.Run(ctx))
	require.GreaterOrEqual(t, int(atomic.LoadInt32(&a.calls)), 3)
}

func TestRunner_ContinuesAfterError(t *testing.T) {
	a := &fakeAgg{err: errors.New("boom")}
	log := obs.NewLogger(obs.LoggerConfig{Service: "tally"})
	m, _ := obs.NewMetrics(obs.MetricsConfig{Service: "tally"})
	r := tally.NewRunner(a, 10*time.Millisecond, m, log)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	require.NoError(t, r.Run(ctx))
	require.GreaterOrEqual(t, int(atomic.LoadInt32(&a.calls)), 3,
		"runner must keep ticking despite errors")
}
```

- [ ] **Step 2: Run test — verify failure**

Run: `cd tally-worker && go test ./internal/tally/ -run TestRunner -v`
Expected: build fails.

- [ ] **Step 3: Write `tally-worker/internal/tally/runner.go`**

```go
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
```

- [ ] **Step 4: Run test — verify pass**

Run: `cd tally-worker && go test ./internal/tally/ -run TestRunner -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tally-worker/internal/tally/runner.go tally-worker/internal/tally/runner_test.go
git commit -m "feat(tally-worker): runner loop with metrics and resilient error handling"
```

---

### Task 20: `tally-worker` main — wire it up

**Files:**
- Create: `tally-worker/cmd/tally-worker/main.go`

- [ ] **Step 1: Write `tally-worker/cmd/tally-worker/main.go`**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/mharner33/voting-app/shared/obs"
	"github.com/mharner33/voting-app/shared/pgxdb"
	"github.com/mharner33/voting-app/tally-worker/internal/tally"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tally-worker:", err)
		os.Exit(1)
	}
}

func run() error {
	service := envDefault("DD_SERVICE", "tally-worker")
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxdb.Open(ctx, dsn)
	if err != nil {
		return err
	}

	runner := tally.NewRunner(tally.NewAggregator(pool), interval, metrics, logger)
	logger.Info("starting", "interval", interval.String())
	runErr := obs.RunUntilSignal(context.Background(), 10*time.Second, func(ctx context.Context) error {
		return runner.Run(ctx)
	})

	pool.Close()
	_ = metrics.Close()
	_ = shutdownTracing(context.Background())
	logger.Info("stopped")
	return runErr
}

func envDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
```

- [ ] **Step 2: Build**

Run: `cd tally-worker && go vet ./... && go build ./cmd/tally-worker`
Expected: builds clean.

- [ ] **Step 3: Commit**

```bash
rm tally-worker/tally-worker 2>/dev/null || true
git add tally-worker/cmd
git commit -m "feat(tally-worker): main with graceful shutdown and configurable interval"
```

---

### Task 21: `tally-worker` Dockerfile + compose entry

**Files:**
- Create: `tally-worker/Dockerfile`
- Modify: `docker-compose.yml`

- [ ] **Step 1: Write `tally-worker/Dockerfile`**

Same shape as vote-api's, retargeted:

```dockerfile
# syntax=docker/dockerfile:1.7
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.work go.work.sum* ./
COPY shared/ ./shared/
COPY vote-api/ ./vote-api/
COPY tally-worker/ ./tally-worker/
COPY results-api/ ./results-api/
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd tally-worker && go build -trimpath -ldflags="-s -w" \
      -o /out/tally-worker ./cmd/tally-worker

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/tally-worker /tally-worker
USER nonroot:nonroot
ENTRYPOINT ["/tally-worker"]
```

- [ ] **Step 2: Append to `docker-compose.yml`**

```yaml
  tally-worker:
    build:
      context: .
      dockerfile: tally-worker/Dockerfile
    depends_on:
      postgres:
        condition: service_healthy
      migrate:
        condition: service_completed_successfully
    environment:
      POSTGRES_DSN: "postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable"
      TALLY_INTERVAL: "${TALLY_INTERVAL}"
      DD_SERVICE: "tally-worker"
      DD_ENV: "${DD_ENV}"
      DD_VERSION: "${DD_VERSION}"
      DD_AGENT_HOST: "datadog-agent"
      DD_TRACE_AGENT_PORT: "8126"
      DD_DOGSTATSD_PORT: "8125"
    labels:
      com.datadoghq.ad.logs: '[{"source":"go","service":"tally-worker"}]'
```

- [ ] **Step 3: Smoke-test the worker end-to-end with vote-api**

Run:
```bash
podman compose up -d postgres
podman compose run --rm migrate
podman compose up -d vote-api tally-worker
sleep 2
for c in tacos tacos burritos; do
  curl -sS -X POST http://localhost:8081/vote -H 'content-type: application/json' \
       -d "{\"poll_id\":\"default\",\"choice\":\"$c\",\"user_id\":\"u\"}"
  echo
done
sleep 7  # wait for at least one tick at 5s default
podman compose exec postgres psql -U voting -d voting \
  -c "SELECT poll_id, choice, count FROM vote_results ORDER BY choice;"
podman compose logs tally-worker | tail -n 20
```
Expected: `vote_results` shows `burritos=1`, `tacos=2`. Logs include `"tally run ok"` JSON lines with `rows_upserted=2`.

- [ ] **Step 4: Tear down**

Run: `podman compose down -v`

- [ ] **Step 5: Commit**

```bash
git add tally-worker/Dockerfile docker-compose.yml
git commit -m "feat(tally-worker): Dockerfile and compose service"
```

---

## Phase 5 — `results-api`

### Task 22: Add `results-api` dependencies

**Files:**
- Modify: `results-api/go.mod`

- [ ] **Step 1: Add deps**

Run:
```bash
cd /home/mike/demo/voting-app/results-api
go get \
  github.com/mharner33/voting-app/shared \
  github.com/jackc/pgx/v5 \
  github.com/jackc/pgx/v5/pgxpool \
  go.opentelemetry.io/otel \
  go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp \
  github.com/stretchr/testify \
  github.com/testcontainers/testcontainers-go/modules/postgres
go mod tidy
```

- [ ] **Step 2: Commit**

```bash
git add results-api/go.mod results-api/go.sum
git commit -m "chore(results-api): add deps"
```

---

### Task 23: Results store — TDD against real Postgres

**Files:**
- Create: `results-api/internal/store/results.go`
- Test: `results-api/internal/store/results_test.go`

- [ ] **Step 1: Write the failing test**

```go
package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mharner33/voting-app/results-api/internal/store"
)

func startPG(t *testing.T) *pgxpool.Pool {
	t.Helper()
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
	return pool
}

func TestResults_GetReturnsSortedAggregates(t *testing.T) {
	pool := startPG(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		INSERT INTO vote_results (poll_id, choice, count) VALUES
		('default','burritos',17),
		('default','tacos',42),
		('other','x',5)`)
	require.NoError(t, err)

	r := store.NewResults(pool)
	got, err := r.Get(ctx, "default")
	require.NoError(t, err)
	require.Len(t, got.Results, 2)
	require.Equal(t, "tacos", got.Results[0].Choice)
	require.Equal(t, 42, got.Results[0].Count)
	require.Equal(t, "burritos", got.Results[1].Choice)
	require.Equal(t, 17, got.Results[1].Count)
	require.False(t, got.UpdatedAt.IsZero())
}

func TestResults_GetEmptyPoll(t *testing.T) {
	pool := startPG(t)
	r := store.NewResults(pool)
	got, err := r.Get(context.Background(), "nonexistent")
	require.NoError(t, err)
	require.Empty(t, got.Results)
}
```

- [ ] **Step 2: Run test — verify failure**

Run: `cd results-api && go test ./internal/store/ -v -timeout 120s`
Expected: build fails.

- [ ] **Step 3: Write `results-api/internal/store/results.go`**

```go
package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ChoiceCount struct {
	Choice string `json:"choice"`
	Count  int    `json:"count"`
}

type Aggregate struct {
	PollID    string        `json:"poll_id"`
	Results   []ChoiceCount `json:"results"`
	UpdatedAt time.Time     `json:"updated_at"`
}

type Results struct {
	pool *pgxpool.Pool
}

func NewResults(pool *pgxpool.Pool) *Results { return &Results{pool: pool} }

func (r *Results) Get(ctx context.Context, pollID string) (Aggregate, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT choice, count, updated_at FROM vote_results
		 WHERE poll_id = $1
		 ORDER BY count DESC, choice ASC`, pollID)
	if err != nil {
		return Aggregate{}, err
	}
	defer rows.Close()

	out := Aggregate{PollID: pollID}
	for rows.Next() {
		var c ChoiceCount
		var ts time.Time
		if err := rows.Scan(&c.Choice, &c.Count, &ts); err != nil {
			return Aggregate{}, err
		}
		out.Results = append(out.Results, c)
		if ts.After(out.UpdatedAt) {
			out.UpdatedAt = ts
		}
	}
	return out, rows.Err()
}

func (r *Results) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }
```

- [ ] **Step 4: Run test — verify pass**

Run: `cd results-api && go test ./internal/store/ -v -timeout 120s`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add results-api/internal/store/
git commit -m "feat(results-api): vote_results read store"
```

---

### Task 24: `GET /results` handler — TDD

**Files:**
- Create: `results-api/internal/handler/results.go`
- Test: `results-api/internal/handler/results_test.go`

Behaviour from §4.3:
- 400 if `poll_id` query param missing.
- 200 with the JSON shape from §4.3 on success.
- 500 on store error.

- [ ] **Step 1: Write the failing test**

```go
package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mharner33/voting-app/results-api/internal/handler"
	"github.com/mharner33/voting-app/results-api/internal/store"
	"github.com/mharner33/voting-app/shared/obs"
)

type fakeStore struct {
	out store.Aggregate
	err error
}

func (f fakeStore) Get(ctx context.Context, pollID string) (store.Aggregate, error) {
	return f.out, f.err
}

func newHandler(t *testing.T, s handler.ResultsStore) http.Handler {
	t.Helper()
	m, err := obs.NewMetrics(obs.MetricsConfig{Service: "results-api"})
	require.NoError(t, err)
	log := obs.NewLogger(obs.LoggerConfig{Service: "results-api", Writer: bytes.NewBuffer(nil)})
	return handler.NewResults(s, m, log)
}

func TestResults_MissingPollID400(t *testing.T) {
	h := newHandler(t, fakeStore{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/results", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestResults_ReturnsJSONShape(t *testing.T) {
	updated := time.Date(2026, 5, 11, 15, 0, 0, 0, time.UTC)
	h := newHandler(t, fakeStore{out: store.Aggregate{
		PollID: "default",
		Results: []store.ChoiceCount{
			{Choice: "tacos", Count: 42},
			{Choice: "burritos", Count: 17},
		},
		UpdatedAt: updated,
	}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/results?poll_id=default", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var got store.Aggregate
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "default", got.PollID)
	require.Len(t, got.Results, 2)
	require.Equal(t, "tacos", got.Results[0].Choice)
	require.Equal(t, updated.UTC(), got.UpdatedAt.UTC())
}

func TestResults_StoreError500(t *testing.T) {
	h := newHandler(t, fakeStore{err: errors.New("db down")})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/results?poll_id=p", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
```

- [ ] **Step 2: Run test — verify failure**

Run: `cd results-api && go test ./internal/handler/ -v`
Expected: build fails.

- [ ] **Step 3: Write `results-api/internal/handler/results.go`**

```go
package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/mharner33/voting-app/results-api/internal/store"
	"github.com/mharner33/voting-app/shared/obs"
)

type ResultsStore interface {
	Get(ctx context.Context, pollID string) (store.Aggregate, error)
}

type resultsHandler struct {
	store   ResultsStore
	metrics *obs.Metrics
	log     *obs.Logger
}

func NewResults(s ResultsStore, m *obs.Metrics, l *obs.Logger) http.Handler {
	return &resultsHandler{store: s, metrics: m, log: l}
}

func (h *resultsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	pollID := r.URL.Query().Get("poll_id")
	if pollID == "" {
		_ = h.metrics.Count("http_server_requests_total", 1,
			[]string{"handler:results", "status:400"})
		http.Error(w, "poll_id is required", http.StatusBadRequest)
		return
	}

	agg, err := h.store.Get(r.Context(), pollID)
	if err != nil {
		_ = h.metrics.Count("http_server_requests_total", 1,
			[]string{"handler:results", "status:500"})
		h.log.ErrorContext(r.Context(), "results get failed",
			"err", err.Error(), "poll_id", pollID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = h.metrics.Count("http_server_requests_total", 1,
		[]string{"handler:results", "status:200"})
	h.log.InfoContext(r.Context(), "results served",
		"poll_id", pollID, "choices", len(agg.Results))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(agg)
}
```

- [ ] **Step 4: Run test — verify pass**

Run: `cd results-api && go test ./internal/handler/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add results-api/internal/handler/
git commit -m "feat(results-api): GET /results handler"
```

---

### Task 25: `results-api` main + Dockerfile + compose

**Files:**
- Create: `results-api/cmd/results-api/main.go`
- Create: `results-api/Dockerfile`
- Modify: `docker-compose.yml`

- [ ] **Step 1: Write `results-api/cmd/results-api/main.go`**

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/mharner33/voting-app/results-api/internal/handler"
	"github.com/mharner33/voting-app/results-api/internal/store"
	"github.com/mharner33/voting-app/shared/httpx"
	"github.com/mharner33/voting-app/shared/obs"
	"github.com/mharner33/voting-app/shared/pgxdb"
)

var (
	buildVersion = "dev"
	buildGitSHA  = "unknown"
	buildDate    = "unknown"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "results-api:", err)
		os.Exit(1)
	}
}

func run() error {
	service := envDefault("DD_SERVICE", "results-api")
	env := envDefault("DD_ENV", "local")
	version := envDefault("DD_VERSION", buildVersion)
	addr := envDefault("HTTP_ADDR", ":8080")
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		return errors.New("POSTGRES_DSN is required")
	}
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxdb.Open(ctx, dsn)
	if err != nil {
		return err
	}

	results := store.NewResults(pool)

	mux := http.NewServeMux()
	mux.Handle("/results", otelhttp.NewHandler(handler.NewResults(results, metrics, logger), "GET /results"))
	httpx.RegisterHealth(mux, results, httpx.VersionInfo{
		Service: service, Version: version, GitSHA: buildGitSHA, BuildDate: buildDate,
	})

	srv := &http.Server{
		Addr:         addr,
		Handler:      httpx.CORS(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	logger.Info("starting", "addr", addr)
	runErr := obs.RunUntilSignal(context.Background(), 10*time.Second, func(ctx context.Context) error {
		errCh := make(chan error, 1)
		go func() { errCh <- srv.ListenAndServe() }()
		select {
		case <-ctx.Done():
			drainCtx, drainCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer drainCancel()
			_ = srv.Shutdown(drainCtx)
			return nil
		case err := <-errCh:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		}
	})

	pool.Close()
	_ = metrics.Close()
	_ = shutdownTracing(context.Background())
	logger.Info("stopped")
	return runErr
}

func envDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
```

- [ ] **Step 2: Write `results-api/Dockerfile`**

```dockerfile
# syntax=docker/dockerfile:1.7
FROM golang:1.22-alpine AS build
WORKDIR /src
COPY go.work go.work.sum* ./
COPY shared/ ./shared/
COPY vote-api/ ./vote-api/
COPY tally-worker/ ./tally-worker/
COPY results-api/ ./results-api/
RUN --mount=type=cache,target=/root/.cache/go-build \
    --mount=type=cache,target=/go/pkg/mod \
    cd results-api && go build -trimpath -ldflags="-s -w" \
      -o /out/results-api ./cmd/results-api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/results-api /results-api
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/results-api"]
```

- [ ] **Step 3: Append to `docker-compose.yml`**

```yaml
  results-api:
    build:
      context: .
      dockerfile: results-api/Dockerfile
    depends_on:
      postgres:
        condition: service_healthy
      migrate:
        condition: service_completed_successfully
    environment:
      HTTP_ADDR: ":8080"
      POSTGRES_DSN: "postgres://${POSTGRES_USER}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB}?sslmode=disable"
      DD_SERVICE: "results-api"
      DD_ENV: "${DD_ENV}"
      DD_VERSION: "${DD_VERSION}"
      DD_AGENT_HOST: "datadog-agent"
      DD_TRACE_AGENT_PORT: "8126"
      DD_DOGSTATSD_PORT: "8125"
    ports:
      - "8082:8080"
    labels:
      com.datadoghq.ad.logs: '[{"source":"go","service":"results-api"}]'
```

- [ ] **Step 4: Build + smoke test**

Run:
```bash
podman compose up -d postgres
podman compose run --rm migrate
podman compose up -d vote-api tally-worker results-api
sleep 2
for c in tacos tacos burritos; do
  curl -sS -X POST http://localhost:8081/vote -H 'content-type: application/json' \
       -d "{\"poll_id\":\"default\",\"choice\":\"$c\",\"user_id\":\"u\"}" >/dev/null
done
sleep 7
curl -sS 'http://localhost:8082/results?poll_id=default' | tee /dev/stderr
```
Expected: JSON with `tacos:2`, `burritos:1`.

- [ ] **Step 5: Tear down + commit**

```bash
podman compose down -v
git add results-api/cmd results-api/Dockerfile docker-compose.yml
git commit -m "feat(results-api): main, Dockerfile, compose entry"
```

---

## Phase 6 — `frontend`

### Task 26: Static HTML/CSS/JS

**Files:**
- Create: `frontend/public/index.html`
- Create: `frontend/public/style.css`
- Create: `frontend/public/app.js`

Two fixed choices for the `default` poll. Submitting calls `vote-api`; results are polled every 2 seconds from `results-api`. Backend URLs are read from `window.VOTING_CONFIG` so we can swap them at deploy time via an injected `<script>`.

- [ ] **Step 1: Write `frontend/public/index.html`**

```html
<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Voting App</title>
  <link rel="stylesheet" href="/style.css">
</head>
<body>
  <main>
    <h1>Vote</h1>
    <p>Default poll: choose one.</p>
    <div class="choices">
      <button data-choice="tacos">tacos</button>
      <button data-choice="burritos">burritos</button>
    </div>
    <p id="status" role="status"></p>

    <h2>Live results</h2>
    <ul id="results"><li>loading...</li></ul>
    <p id="updated" class="muted"></p>
  </main>

  <script>
    window.VOTING_CONFIG = {
      voteApi:    "http://localhost:8081",
      resultsApi: "http://localhost:8082",
      pollId:     "default"
    };
  </script>
  <script src="/app.js"></script>
</body>
</html>
```

- [ ] **Step 2: Write `frontend/public/style.css`**

```css
:root { font-family: system-ui, sans-serif; }
body  { max-width: 32rem; margin: 2rem auto; padding: 0 1rem; }
.choices button {
  font-size: 1.1rem; padding: 0.6rem 1.2rem; margin-right: 0.5rem; cursor: pointer;
}
#status { min-height: 1.2em; }
.muted  { color: #666; font-size: 0.85rem; }
ul#results { padding-left: 1.2rem; }
```

- [ ] **Step 3: Write `frontend/public/app.js`**

```javascript
(function () {
  const cfg = window.VOTING_CONFIG;
  const statusEl = document.getElementById("status");
  const resultsEl = document.getElementById("results");
  const updatedEl = document.getElementById("updated");

  function userId() {
    let id = localStorage.getItem("voting-user-id");
    if (!id) {
      id = crypto.randomUUID();
      localStorage.setItem("voting-user-id", id);
    }
    return id;
  }

  async function castVote(choice) {
    statusEl.textContent = "submitting...";
    try {
      const r = await fetch(cfg.voteApi + "/vote", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ poll_id: cfg.pollId, choice, user_id: userId() }),
      });
      if (!r.ok) throw new Error("HTTP " + r.status);
      statusEl.textContent = "vote recorded for " + choice;
    } catch (e) {
      statusEl.textContent = "error: " + e.message;
    }
  }

  async function refreshResults() {
    try {
      const r = await fetch(cfg.resultsApi + "/results?poll_id=" + encodeURIComponent(cfg.pollId));
      if (!r.ok) throw new Error("HTTP " + r.status);
      const data = await r.json();
      resultsEl.innerHTML = "";
      if (!data.results || data.results.length === 0) {
        resultsEl.innerHTML = "<li>no votes yet</li>";
      } else {
        data.results.forEach(({ choice, count }) => {
          const li = document.createElement("li");
          li.textContent = `${choice}: ${count}`;
          resultsEl.appendChild(li);
        });
      }
      updatedEl.textContent = "updated: " + (data.updated_at || "—");
    } catch (e) {
      updatedEl.textContent = "results error: " + e.message;
    }
  }

  document.querySelectorAll(".choices button").forEach((btn) => {
    btn.addEventListener("click", () => castVote(btn.dataset.choice));
  });

  refreshResults();
  setInterval(refreshResults, 2000);
})();
```

- [ ] **Step 4: Commit**

```bash
git add frontend/public/
git commit -m "feat(frontend): static voting UI"
```

---

### Task 27: nginx Dockerfile + compose entry

**Files:**
- Create: `frontend/nginx.conf`
- Create: `frontend/Dockerfile`
- Modify: `docker-compose.yml`

- [ ] **Step 1: Write `frontend/nginx.conf`**

```nginx
server {
  listen 8080 default_server;
  root /usr/share/nginx/html;
  index index.html;
  location / {
    try_files $uri $uri/ =404;
  }
}
```

- [ ] **Step 2: Write `frontend/Dockerfile`**

```dockerfile
FROM nginx:1.27-alpine
COPY frontend/nginx.conf /etc/nginx/conf.d/default.conf
COPY frontend/public/ /usr/share/nginx/html/
EXPOSE 8080
```

- [ ] **Step 3: Append to `docker-compose.yml`**

```yaml
  frontend:
    build:
      context: .
      dockerfile: frontend/Dockerfile
    ports:
      - "8080:8080"
    depends_on:
      - vote-api
      - results-api
```

- [ ] **Step 4: Smoke-test in a browser**

Run:
```bash
podman compose up -d --build
sleep 5
# Browser: http://localhost:8080
```
Expected: page loads, clicking buttons updates results within ~5s (tally interval).

- [ ] **Step 5: Tear down + commit**

```bash
podman compose down -v
git add frontend/Dockerfile frontend/nginx.conf docker-compose.yml
git commit -m "feat(frontend): nginx container and compose entry"
```

---

## Phase 7 — Datadog Agent integration

### Task 28: Add Datadog Agent to compose

**Files:**
- Modify: `docker-compose.yml`

The Agent receives traces on `8126`, metrics on `8125`, and tails container logs via the Docker socket. The API key comes from `.env`.

- [ ] **Step 1: Append to `docker-compose.yml`**

```yaml
  datadog-agent:
    image: gcr.io/datadoghq/agent:7
    environment:
      DD_API_KEY: "${DD_API_KEY}"
      DD_SITE: "${DD_SITE}"
      DD_APM_ENABLED: "true"
      DD_APM_NON_LOCAL_TRAFFIC: "true"
      DD_DOGSTATSD_NON_LOCAL_TRAFFIC: "true"
      DD_LOGS_ENABLED: "true"
      DD_LOGS_CONFIG_CONTAINER_COLLECT_ALL: "true"
      DD_CONTAINER_EXCLUDE: "name:datadog-agent"
      DD_ENV: "${DD_ENV}"
      DD_TAGS: "demo:voting-app env:${DD_ENV}"
    volumes:
      # Rootless podman: expose the user socket at the path the agent expects.
      - /run/user/1000/podman/podman.sock:/var/run/docker.sock:ro
      - /proc/:/host/proc/:ro
      - /sys/fs/cgroup/:/host/sys/fs/cgroup:ro
    ports:
      - "8125:8125/udp"
      - "8126:8126"
```

Also add a `depends_on: [datadog-agent]` block to `vote-api`, `tally-worker`, `results-api` so they don't try to push to the agent before it's listening. Use this fragment for each service:

```yaml
    depends_on:
      postgres:
        condition: service_healthy
      migrate:
        condition: service_completed_successfully
      datadog-agent:
        condition: service_started
```

- [ ] **Step 2: Bring up the full stack**

Run:
```bash
# Ensure DD_API_KEY is set in .env to a real key
podman compose up -d --build
sleep 8
podman compose ps
```
Expected: `postgres`, `vote-api`, `results-api`, `tally-worker`, `frontend`, `datadog-agent` all `running`. `migrate` shows `Exit 0`.

- [ ] **Step 3: Drive traffic and check Datadog**

Run:
```bash
for i in $(seq 1 10); do
  curl -sS -X POST http://localhost:8081/vote -H 'content-type: application/json' \
       -d "{\"poll_id\":\"default\",\"choice\":\"tacos\",\"user_id\":\"u$i\"}" >/dev/null
  curl -sS 'http://localhost:8082/results?poll_id=default' >/dev/null
done
podman compose logs --tail=20 datadog-agent | grep -i 'trace\|stats\|logs' || true
```

Then in Datadog UI:
- **APM > Traces:** should see spans for `POST /vote`, `GET /results`, and `tally.run`.
- **APM > Service Catalog:** should list `vote-api`, `results-api`, `tally-worker` for env `local`.
- **Metrics Explorer:** search `voting.vote_submissions_total`, `voting.tally_runs_total`, `voting.tally_last_success_timestamp`.
- **Logs Explorer:** search `service:vote-api` — should see JSON log lines with `trace_id` populated.

Expected: all three signal types arrive within ~1 minute.

- [ ] **Step 4: Commit**

```bash
git add docker-compose.yml
git commit -m "feat(infra): Datadog Agent in compose, services depend on it"
```

---

## Phase 8 — End-to-end verification & docs

### Task 29: Smoke test script

**Files:**
- Create: `scripts/smoke.sh`

A repeatable end-to-end check: clean up, bring up, vote, wait, verify results, tear down. Used by `make smoke` and CI.

- [ ] **Step 1: Write `scripts/smoke.sh`**

```bash
#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

# Make testcontainers + compose talk to rootless podman.
export DOCKER_HOST="${DOCKER_HOST:-unix:///run/user/$(id -u)/podman/podman.sock}"
export TESTCONTAINERS_RYUK_DISABLED=true

cleanup() { podman compose down -v >/dev/null 2>&1 || true; }
trap cleanup EXIT

cleanup
podman compose up -d --build postgres
podman compose run --rm migrate
podman compose up -d --build vote-api tally-worker results-api

# wait for vote-api readiness
for i in $(seq 1 30); do
  if curl -fsS http://localhost:8081/readyz >/dev/null 2>&1; then break; fi
  sleep 1
done

# 5 tacos, 2 burritos
for i in 1 2 3 4 5; do
  curl -fsS -X POST http://localhost:8081/vote -H 'content-type: application/json' \
       -d "{\"poll_id\":\"smoke\",\"choice\":\"tacos\",\"user_id\":\"u$i\"}" >/dev/null
done
for i in 1 2; do
  curl -fsS -X POST http://localhost:8081/vote -H 'content-type: application/json' \
       -d "{\"poll_id\":\"smoke\",\"choice\":\"burritos\",\"user_id\":\"v$i\"}" >/dev/null
done

# wait for at least one tally interval + slack
sleep 8

OUT="$(curl -fsS 'http://localhost:8082/results?poll_id=smoke')"
echo "$OUT"

echo "$OUT" | grep -q '"choice":"tacos","count":5' || { echo "FAIL: tacos != 5"; exit 1; }
echo "$OUT" | grep -q '"choice":"burritos","count":2' || { echo "FAIL: burritos != 2"; exit 1; }
echo "smoke OK"
```

- [ ] **Step 2: Make it executable and run**

Run:
```bash
chmod +x scripts/smoke.sh
make smoke
```
Expected: ends with `smoke OK`.

- [ ] **Step 3: Commit**

```bash
git add scripts/smoke.sh
git commit -m "test: end-to-end smoke script"
```

---

### Task 30: Update CLAUDE.md to reflect the implemented state

**Files:**
- Modify: `CLAUDE.md`

Per CLAUDE.md's own "Updating this file" section, the "Repository status" section should be replaced with real commands once code lands.

- [ ] **Step 1: Replace the "Repository status" section**

In `CLAUDE.md`, replace:

```markdown
## Repository status

This repository is **pre-implementation**. The only file is `voting-app-architecture.md`, a design document for a microservices voting app intended to demonstrate observability patterns (OpenTelemetry + Datadog, optional Temporal). There is no source code, no build system, no tests, and no `docker-compose.yml` yet.

Before writing code or inventing commands (build/test/run), read `voting-app-architecture.md` — it is the source of truth for service boundaries, APIs, schema, and observability requirements.
```

with:

```markdown
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

Integration tests for the data path (`vote-api/internal/store`, `tally-worker/internal/tally`, `results-api/internal/store`) use `testcontainers-go/postgres` and **require a running Docker daemon**. Pure-handler tests (`vote-api/internal/handler`, `results-api/internal/handler`) and `shared/` tests don't.

Temporal variant (§4.4.2) is **not implemented** — see `docs/superpowers/plans/` for that follow-up plan when it lands.
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: replace pre-implementation status with real build/test/run table"
```

---

## Self-Review Checklist

Spec coverage:
- §3.1 frontend, vote-api, results-api, tally-worker, postgres, observability stack → Tasks 1, 3, 12–16, 17–21, 22–25, 26–27, 28
- §4.1 frontend HTTP calls → Task 26 (Datadog RUM in the browser is explicitly out of scope)
- §4.2 vote-api API + observability → Tasks 13–16
- §4.3 results-api API + observability → Tasks 22–25
- §4.4.1 baseline tally-worker (single tx, idempotency, interval, span+metrics) → Tasks 18–21
- §4.4.2 Temporal variant → **deferred to a separate plan** (called out at top)
- §4.5 schema + intentional vote duplication → Task 2; tested in Task 13
- §5.1 tracing + graceful shutdown → Tasks 5, 8, 11, 15, 20, 25
- §5.2 metrics via dogstatsd → Tasks 7, 14, 19, 24
- §5.3 structured JSON logs with trace_id → Task 6, used in 14, 19, 24
- §5.4 dashboards/alerts → manual verification step in Task 28 (formal dashboards/alerts are post-baseline ops work, not code)
- §6 docker-compose + env vars → Tasks 3, 16, 21, 25, 27, 28

Non-spec checks:
- TDD discipline: every business-logic file has a failing-test step before implementation.
- Each task ends with a commit step.
- Type/method consistency: `Aggregator.Run` / `AggregatorRunner` / `Runner.Run` all match. `VoteStore` / `ResultsStore` interfaces have a single method each, matching their store impls. `httpx.RegisterHealth(mux, pinger, version)` signature is consistent across vote-api and results-api callers. `obs.Metrics.Count/Histogram/Gauge` signatures match what handlers/runners call.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-11-voting-app-baseline.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
