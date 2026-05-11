package obs

import (
	"context"
	"fmt"
	"sync"

	ddotel "github.com/DataDog/dd-trace-go/v2/ddtrace/opentelemetry"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"go.opentelemetry.io/otel"
)

// TracingConfig holds the configuration for the OTel TracerProvider.
type TracingConfig struct {
	Service string
	Env     string
	Version string
	// AgentHost / AgentPort default to env (DD_AGENT_HOST / DD_TRACE_AGENT_PORT).
	AgentHost string
	AgentPort string
}

// ShutdownFunc flushes spans and stops the tracer. Safe to call multiple times.
type ShutdownFunc func(context.Context) error

// StartTracing registers the dd-trace-go OTel provider as the global TracerProvider
// and returns a shutdown function that flushes buffered spans on exit.
// Forgetting to call the shutdown func silently drops the final batch of spans —
// always defer it immediately after calling StartTracing.
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
