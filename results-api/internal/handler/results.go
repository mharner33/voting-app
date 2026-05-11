package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/mharner33/voting-app/results-api/internal/store"
	"github.com/mharner33/voting-app/shared/obs"
)

type ResultsStore interface {
	Get(ctx context.Context, pollID string) (store.Aggregate, error)
}

type resultsHandler struct {
	store   ResultsStore
	metrics *obs.Metrics
	log     *obs.Logger
}

func NewResults(s ResultsStore, m *obs.Metrics, l *obs.Logger) http.Handler {
	return &resultsHandler{store: s, metrics: m, log: l}
}

func (h *resultsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	pollID := r.URL.Query().Get("poll_id")
	if pollID == "" {
		_ = h.metrics.Count("http_server_requests_total", 1,
			[]string{"handler:results", "status:400"})
		http.Error(w, "poll_id is required", http.StatusBadRequest)
		return
	}

	agg, err := h.store.Get(r.Context(), pollID)
	if err != nil {
		_ = h.metrics.Count("http_server_requests_total", 1,
			[]string{"handler:results", "status:500"})
		h.log.ErrorContext(r.Context(), "results get failed",
			"err", err.Error(), "poll_id", pollID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = h.metrics.Count("http_server_requests_total", 1,
		[]string{"handler:results", "status:200"})
	h.log.InfoContext(r.Context(), "results served",
		"poll_id", pollID, "choices", len(agg.Results))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(agg)
}
