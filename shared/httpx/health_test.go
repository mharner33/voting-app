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
