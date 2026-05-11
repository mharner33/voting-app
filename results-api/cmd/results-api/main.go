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
