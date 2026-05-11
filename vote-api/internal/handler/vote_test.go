package handler_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mharner33/voting-app/shared/obs"
	"github.com/mharner33/voting-app/vote-api/internal/handler"
	"github.com/mharner33/voting-app/vote-api/internal/store"
)

type fakeStore struct {
	called int
	err    error
	last   store.Vote
}

func (f *fakeStore) Insert(ctx context.Context, v store.Vote) error {
	f.called++
	f.last = v
	return f.err
}

func newHandler(t *testing.T, s handler.VoteStore) http.Handler {
	t.Helper()
	m, err := obs.NewMetrics(obs.MetricsConfig{Service: "vote-api"})
	require.NoError(t, err)
	log := obs.NewLogger(obs.LoggerConfig{Service: "vote-api", Writer: bytes.NewBuffer(nil)})
	return handler.NewVote(s, m, log)
}

func TestVote_RejectsNonPost(t *testing.T) {
	s := &fakeStore{}
	h := newHandler(t, s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/vote", nil))
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	require.Equal(t, 0, s.called)
}

func TestVote_RejectsMalformedJSON(t *testing.T) {
	s := &fakeStore{}
	h := newHandler(t, s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/vote", strings.NewReader("{not json")))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, 0, s.called)
}

func TestVote_RejectsMissingFields(t *testing.T) {
	s := &fakeStore{}
	h := newHandler(t, s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/vote",
		strings.NewReader(`{"poll_id":"p"}`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, 0, s.called)
}

func TestVote_PersistsAndReturns200(t *testing.T) {
	s := &fakeStore{}
	h := newHandler(t, s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/vote",
		strings.NewReader(`{"poll_id":"default","choice":"tacos","user_id":"u1"}`)))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, s.called)
	require.Equal(t, store.Vote{PollID: "default", Choice: "tacos", UserID: "u1"}, s.last)
}

func TestVote_StoreError500(t *testing.T) {
	s := &fakeStore{err: errors.New("boom")}
	h := newHandler(t, s)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/vote",
		strings.NewReader(`{"poll_id":"p","choice":"c"}`)))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}
