package obs

import (
	"fmt"
	"os"
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"
)

// MetricsConfig holds configuration for the dogstatsd metrics client.
type MetricsConfig struct {
	Address string // host:port, e.g. "datadog-agent:8125"; empty → no-op
	Service string
	Env     string
	Version string
}

// Metrics wraps a dogstatsd client with a thin, testable interface.
type Metrics struct {
	client statsd.ClientInterface
}

// NewMetrics returns a Metrics backed by a real dogstatsd client when
// cfg.Address is non-empty, or a no-op client otherwise.
// If the agent address is set but unreachable at startup (e.g. agent not yet
// running), NewMetrics logs to stderr and falls back to the no-op client so
// the service can still start. Metrics will be silently dropped until the
// process is restarted with a reachable agent.
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
		// Agent unreachable at startup — degrade gracefully to no-op.
		// Traces will also be missing; add a Datadog Agent to restore both.
		fmt.Fprintf(os.Stderr, "obs: metrics agent unreachable (%s), using no-op client: %v\n", cfg.Address, err)
		return &Metrics{client: &statsd.NoOpClient{}}, nil
	}
	return &Metrics{client: c}, nil
}

// Count increments a counter metric by value.
func (m *Metrics) Count(name string, value int64, tags []string) error {
	return m.client.Count(name, value, tags, 1)
}

// Histogram records a histogram observation.
func (m *Metrics) Histogram(name string, value float64, tags []string) error {
	return m.client.Histogram(name, value, tags, 1)
}

// Gauge records a gauge value.
func (m *Metrics) Gauge(name string, value float64, tags []string) error {
	return m.client.Gauge(name, value, tags, 1)
}

// Timing records a duration observation.
func (m *Metrics) Timing(name string, d time.Duration, tags []string) error {
	return m.client.Timing(name, d, tags, 1)
}

// Flush forces any buffered metrics to be sent immediately.
func (m *Metrics) Flush() error { return m.client.Flush() }

// Close shuts down the metrics client and flushes remaining metrics.
func (m *Metrics) Close() error { return m.client.Close() }
