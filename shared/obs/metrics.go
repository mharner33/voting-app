package obs

import (
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
