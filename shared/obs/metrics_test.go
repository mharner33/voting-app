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
