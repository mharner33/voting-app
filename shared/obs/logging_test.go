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
