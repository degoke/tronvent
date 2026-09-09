package api

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	internaldb "github.com/degoke/tronvent/internal/db"
)

var webhookEventStatuses = map[string]struct{}{
	"pending":    {},
	"delivering": {},
	"failed":     {},
	"delivered":  {},
	"dead":       {},
}

func (s *Server) handleGetWebhookEvents(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = "failed"
	}
	queryStatus := status
	if status == "all" {
		queryStatus = ""
	}
	if queryStatus != "" {
		if _, ok := webhookEventStatuses[queryStatus]; !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid webhook event status"})
			return
		}
	}

	events, err := s.db.ListWebhookEvents(r.Context(), queryStatus, queryInt(r, "limit", 50))
	if err != nil {
		slog.Error("list webhook events", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list webhook events"})
		return
	}

	items := make([]map[string]any, 0, len(events))
	for _, event := range events {
		items = append(items, webhookEventResponse(event))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":  items,
		"status": status,
	})
}

func (s *Server) handleGetWebhookEventAttempts(w http.ResponseWriter, r *http.Request) {
	eventID := strings.TrimSpace(r.PathValue("eventID"))
	if eventID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "event ID is required"})
		return
	}

	attempts, err := s.db.ListWebhookDeliveryAttempts(r.Context(), eventID)
	if err != nil {
		slog.Error("list webhook delivery attempts", "eventId", eventID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list webhook delivery attempts"})
		return
	}

	items := make([]map[string]any, 0, len(attempts))
	for _, attempt := range attempts {
		item := map[string]any{
			"id":            attempt.ID,
			"attemptNumber": attempt.AttemptNumber,
			"createdAt":     attempt.CreatedAt.UTC().Format(time.RFC3339),
		}
		if attempt.ResponseCode != nil {
			item["responseCode"] = *attempt.ResponseCode
		}
		if attempt.ErrorMessage != nil {
			item["errorMessage"] = *attempt.ErrorMessage
		}
		if attempt.DurationMs != nil {
			item["durationMs"] = *attempt.DurationMs
		}
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"eventId": eventID,
		"items":   items,
	})
}

func (s *Server) handleRetryWebhookEventAPI(w http.ResponseWriter, r *http.Request) {
	eventID := strings.TrimSpace(r.PathValue("eventID"))
	if eventID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "event ID is required"})
		return
	}

	if err := s.db.RetryWebhookEvent(r.Context(), eventID); err != nil {
		if errors.Is(err, internaldb.ErrWebhookEventNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "webhook event not found or not retryable"})
			return
		}
		slog.Error("retry webhook event", "eventId", eventID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retry webhook event"})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"eventId": eventID,
		"status":  "pending",
	})
}

func (s *Server) handleRetryAllWebhookEventsAPI(w http.ResponseWriter, r *http.Request) {
	count, err := s.db.RetryAllFailedDeadWebhookEvents(r.Context())
	if err != nil {
		slog.Error("retry all webhook events", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retry webhook events"})
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"retried": count,
		"status":  "pending",
	})
}

func webhookEventResponse(event internaldb.DashboardWebhookEvent) map[string]any {
	item := map[string]any{
		"id":            event.ID,
		"eventType":     event.EventType,
		"scope":         event.Scope,
		"txHash":        event.TxHash,
		"blockNumber":   event.BlockNumber,
		"status":        event.Status,
		"attemptCount":  event.AttemptCount,
		"createdAt":     event.CreatedAt.UTC().Format(time.RFC3339),
		"updatedAt":     event.UpdatedAt.UTC().Format(time.RFC3339),
		"nextAttemptAt": event.NextAttemptAt.UTC().Format(time.RFC3339),
	}
	if event.LastError != nil {
		item["lastError"] = *event.LastError
	}
	if event.LastResponseCode != nil {
		item["lastResponseCode"] = *event.LastResponseCode
	}
	return item
}
