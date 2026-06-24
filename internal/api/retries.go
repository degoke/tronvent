package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	internaldb "github.com/degoke/tronvent/internal/db"
)

func (s *Server) handlePostRetryBlock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BlockNumber int64 `json:"blockNumber"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if req.BlockNumber <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "blockNumber must be positive"})
		return
	}

	result, err := s.db.EnqueueRetryJob(r.Context(), req.BlockNumber, req.BlockNumber)
	if err != nil {
		writeRetryEnqueueError(w, err)
		return
	}
	writeRetryJobResponse(w, http.StatusAccepted, result.Job)
}

func (s *Server) handlePostRetryRange(w http.ResponseWriter, r *http.Request) {
	var req struct {
		FromBlock int64 `json:"fromBlock"`
		ToBlock   int64 `json:"toBlock"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	result, err := s.db.EnqueueRetryJob(r.Context(), req.FromBlock, req.ToBlock)
	if err != nil {
		writeRetryEnqueueError(w, err)
		return
	}
	writeRetryJobResponse(w, http.StatusAccepted, result.Job)
}

func (s *Server) handleGetRetries(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit := queryInt(r, "limit", 50)

	jobs, err := s.db.ListRetryJobs(r.Context(), status, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list retry jobs"})
		return
	}
	items := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		items = append(items, retryJobJSON(job))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func writeRetryEnqueueError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, internaldb.ErrNonPositiveBlock):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "block numbers must be positive"})
	case errors.Is(err, internaldb.ErrInvalidBlockRange):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "fromBlock must be <= toBlock"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to enqueue retry job"})
	}
}

func writeRetryJobResponse(w http.ResponseWriter, status int, job internaldb.RetryJobRecord) {
	writeJSON(w, status, retryJobJSON(job))
}

func retryJobJSON(job internaldb.RetryJobRecord) map[string]any {
	out := map[string]any{
		"jobId":     job.ID,
		"queue":     job.Queue,
		"fromBlock": job.FromBlock,
		"toBlock":   job.ToBlock,
		"status":    job.Status,
		"attempts":  job.Attempts,
		"createdAt": job.CreatedAt.UTC().Format(timeRFC3339),
		"updatedAt": job.UpdatedAt.UTC().Format(timeRFC3339),
	}
	if job.LastError != nil {
		out["lastError"] = *job.LastError
	}
	if job.CompletedAt != nil {
		out["completedAt"] = job.CompletedAt.UTC().Format(timeRFC3339)
	}
	return out
}

const timeRFC3339 = time.RFC3339

func decodeJSON(r *http.Request, dst any) error {
	return json.NewDecoder(r.Body).Decode(dst)
}
