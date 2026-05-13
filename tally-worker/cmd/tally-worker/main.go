package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/mharner33/voting-app/shared/obs"
	"github.com/mharner33/voting-app/shared/pgxdb"
	"github.com/mharner33/voting-app/tally-worker/tally"
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
