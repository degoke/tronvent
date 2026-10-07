package webhook

import (
	"context"
	"fmt"
	"log/slog"

	internaldb "github.com/degoke/tronvent/internal/db"
)

func (w *Worker) disableEndpointAndNotify(ctx context.Context, endpoint *internaldb.WebhookEndpoint, endpointID string, reason string) {
	if endpointID == "" {
		return
	}
	if err := w.db.DeactivateWebhook(ctx, endpointID, reason); err != nil {
		slog.Error("deactivate webhook endpoint", "endpointId", endpointID, "err", err)
		return
	}
	slog.Warn("webhook endpoint disabled", "endpointId", endpointID, "reason", reason)
	if endpoint != nil {
		ep := *endpoint
		ep.IsActive = false
		w.config.UpsertEndpoint(ep)
		w.sendEndpointFailureEmail(&ep, reason)
	}
}

func (w *Worker) sendEndpointFailureEmail(endpoint *internaldb.WebhookEndpoint, reason string) {
	if endpoint == nil {
		return
	}
	subject := "Tronvent webhook endpoint disabled"
	body := fmt.Sprintf("Webhook endpoint %s (%s) was disabled.\n\nReason: %s\n", endpoint.ID, endpoint.WebhookURL, reason)
	if err := SendFailureNotification(w.notifySMTP, endpoint.FailureNotifyEmail, subject, body); err != nil {
		slog.Error("webhook failure notification email", "endpointId", endpoint.ID, "err", err)
	}
}
