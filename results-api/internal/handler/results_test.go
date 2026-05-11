package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mharner33/voting-app/results-api/internal/handler"
	"github.com/mharner33/voting-app/results-api/internal/store"
	"github.com/mharner33/voting-app/shared/obs"
)

type fakeStore struct {
	out store.Aggregate
	err error
}

func (f fakeStore) Get(ctx context.Context, pollID string) (store.Aggregate, error) {
	return f.out, f.err
}

func newHandler(t *testing.T, s handler.ResultsStore) http.Handler {
	t.Helper()
	m, err := obs.NewMetrics(obs.MetricsConfig{Service: "results-api"})
	require.NoError(t, err)
	log := obs.NewLogger(obs.LoggerConfig{Service: "results-api", Writer: bytes.NewBuffer(nil)})
	return handler.NewResults(s, m, log)
}

func TestResults_MissingPollID400(t *testing.T) {
	h := newHandler(t, fakeStore{})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/results", nil))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestResults_ReturnsJSONShape(t *testing.T) {
	updated := time.Date(2026, 5, 11, 15, 0, 0, 0, time.UTC)
	h := newHandler(t, fakeStore{out: store.Aggregate{
		PollID: "default",
		Results: []store.ChoiceCount{
			{Choice: "tacos", Count: 42},
			{Choice: "burritos", Count: 17},
		},
		UpdatedAt: updated,
	}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/results?poll_id=default", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var got store.Aggregate
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "default", got.PollID)
	require.Len(t, got.Results, 2)
	require.Equal(t, "tacos", got.Results[0].Choice)
	require.Equal(t, updated.UTC(), got.UpdatedAt.UTC())
}

func TestResults_StoreError500(t *testing.T) {
	h := newHandler(t, fakeStore{err: errors.New("db down")})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/results?poll_id=p", nil))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
