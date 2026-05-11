package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/mharner33/voting-app/shared/obs"
	"github.com/mharner33/voting-app/vote-api/internal/store"
)

type VoteStore interface {
	Insert(ctx context.Context, v store.Vote) error
}

type voteHandler struct {
	store   VoteStore
	metrics *obs.Metrics
	log     *obs.Logger
}

func NewVote(s VoteStore, m *obs.Metrics, l *obs.Logger) http.Handler {
	return &voteHandler{store: s, metrics: m, log: l}
}

type voteRequest struct {
	PollID string `json:"poll_id"`
	Choice string `json:"choice"`
	UserID string `json:"user_id"`
}

func (h *voteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		_ = h.metrics.Count("http_server_requests_total", 1,
			[]string{"handler:vote", "status:405"})
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req voteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		_ = h.metrics.Count("http_server_requests_total", 1,
			[]string{"handler:vote", "status:400"})
		h.log.InfoContext(r.Context(), "invalid payload", "err", err.Error())
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	if req.PollID == "" || req.Choice == "" {
		_ = h.metrics.Count("http_server_requests_total", 1,
			[]string{"handler:vote", "status:400"})
		http.Error(w, "poll_id and choice are required", http.StatusBadRequest)
		return
	}

	err := h.store.Insert(r.Context(), store.Vote{
		PollID: req.PollID, Choice: req.Choice, UserID: req.UserID,
	})
	if err != nil {
		_ = h.metrics.Count("http_server_requests_total", 1,
			[]string{"handler:vote", "status:500"})
		h.log.ErrorContext(r.Context(), "store insert failed",
			"err", err.Error(),
			"poll_id", req.PollID, "choice", req.Choice, "user_id", req.UserID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	_ = h.metrics.Count("vote_submissions_total", 1,
		[]string{"poll:" + req.PollID, "choice:" + req.Choice})
	_ = h.metrics.Count("http_server_requests_total", 1,
		[]string{"handler:vote", "status:200"})
	h.log.InfoContext(r.Context(), "vote recorded",
		"poll_id", req.PollID, "choice", req.Choice, "user_id", req.UserID)

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
