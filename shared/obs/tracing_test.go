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
