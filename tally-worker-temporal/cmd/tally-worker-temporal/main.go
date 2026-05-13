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
	"go.temporal.io/sdk/workflow"

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

	// OTel-based tracing interceptor routes through the OTel API that
	// shared/obs.StartTracing wired up to dd-trace-go.
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

	scheduleCtx, scheduleCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer scheduleCancel()
	if err := schedule.EnsureTallySchedule(scheduleCtx, temporalClient, taskQueue, workflowName, interval); err != nil {
		return fmt.Errorf("ensure schedule: %w", err)
	}

	w := worker.New(temporalClient, taskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(tw.TallyWorkflow, workflow.RegisterOptions{Name: workflowName})

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
		// worker.Run blocks until the channel is closed, then Stop()s the worker.
		return w.Run(ctxToInterruptCh(ctx))
	})

	temporalClient.Close()
	pool.Close()
	_ = metrics.Close()
	_ = shutdownTracing(context.Background())
	logger.Info("stopped")
	return runErr
}

// ctxToInterruptCh adapts context.Context into the <-chan interface{} that
// Temporal's worker.Run expects. When ctx is done the channel is closed,
// which triggers worker.Stop().
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
