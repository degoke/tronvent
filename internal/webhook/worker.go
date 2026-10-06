package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/degoke/tronvent/internal/config"
	internaldb "github.com/degoke/tronvent/internal/db"
	"github.com/degoke/tronvent/internal/store"
	"github.com/degoke/tronvent/internal/webhookspec"
)

// webhookDB is the subset of db.Client used by the delivery worker.
type webhookDB interface {
	ClaimPendingWebhookEvents(ctx context.Context, limit int) ([]internaldb.WebhookEvent, error)
	MarkWebhookEventDelivered(ctx context.Context, id string, responseCode int) error
	MarkWebhookEventFailed(ctx context.Context, id string, attemptNumber int, responseCode *int, errMsg string, nextAttempt time.Time, maxAttempts int) error
	RecordWebhookDeliveryAttempt(ctx context.Context, eventID string, attemptNumber int, reqHeaders, reqBody json.RawMessage, responseCode *int, responseBody, errMsg string, durationMs int) error
	DeactivateWebhook(ctx context.Context, endpointID, reason string) error
}

// Worker delivers webhook events from the Postgres outbox.
type Worker struct {
	cfg         *config.Config
	db          webhookDB
	config      *store.WebhookConfigStore
	client      *http.Client
	urlPolicy   webhookspec.URLPolicy
	maxAttempts int
	notifySMTP  SMTPConfig
}

// NewWorker creates a webhook delivery worker.
func NewWorker(cfg *config.Config, db webhookDB, configStore *store.WebhookConfigStore, urlPolicy webhookspec.URLPolicy) *Worker {
	timeout := time.Duration(cfg.WebhookHTTPTimeoutSeconds) * time.Second
	maxAttempts := cfg.WebhookMaxAttempts
	if maxAttempts <= 0 || maxAttempts > webhookspec.SpecDeliveryAttempts {
		maxAttempts = webhookspec.SpecDeliveryAttempts
	}
	return &Worker{
		cfg:         cfg,
		db:          db,
		config:      configStore,
		client:      webhookspec.NewDeliveryHTTPClient(timeout, urlPolicy),
		urlPolicy:   urlPolicy,
		maxAttempts: maxAttempts,
		notifySMTP: SMTPConfig{
			Host: cfg.WebhookNotifySMTPHost, Port: cfg.WebhookNotifySMTPPort,
			Username: cfg.WebhookNotifySMTPUser, Password: cfg.WebhookNotifySMTPPass,
			From: cfg.WebhookNotifySMTPFrom,
		},
	}
}

// Run polls the outbox and delivers pending events until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	slog.Info("webhook delivery worker started", "pollIntervalMs", w.cfg.WebhookPollIntervalMs)
	interval := time.Duration(w.cfg.WebhookPollIntervalMs) * time.Millisecond
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("webhook delivery worker stopping")
			return
		case <-ticker.C:
			if err := w.dispatchBatch(ctx); err != nil && ctx.Err() == nil {
				slog.Error("webhook dispatch batch error", "err", err)
			}
		}
	}
}

func (w *Worker) dispatchBatch(ctx context.Context) error {
	events, err := w.db.ClaimPendingWebhookEvents(ctx, 10)
	if err != nil {
		return err
	}
	for _, ev := range events {
		if err := w.deliverOne(ctx, ev); err != nil && ctx.Err() == nil {
			slog.Error("webhook delivery error", "eventId", ev.ID, "err", err)
		}
	}
	return nil
}

// DispatchOnce processes one batch of pending webhook events (for tests).
func (w *Worker) DispatchOnce(ctx context.Context) error {
	return w.dispatchBatch(ctx)
}

func (w *Worker) deliverOne(ctx context.Context, ev internaldb.WebhookEvent) error {
	var endpoint *internaldb.WebhookEndpoint
	if ev.EndpointID != "" {
		endpoint = w.config.GetEndpoint(ev.EndpointID)
	} else {
		endpoint = w.config.GetPrimaryEndpoint()
	}
	attemptNumber := ev.AttemptCount + 1

	firstAttemptAt := ev.CreatedAt
	if firstAttemptAt.IsZero() {
		firstAttemptAt = time.Now()
	}

	if endpoint == nil || !endpoint.IsActive || endpoint.WebhookURL == "" {
		next := webhookspec.NextAttemptTime(firstAttemptAt, attemptNumber, nil)
		return w.db.MarkWebhookEventFailed(ctx, ev.ID, attemptNumber, nil, "webhook endpoint not active", next, w.maxAttempts)
	}

	if err := w.urlPolicy.ValidateWebhookURL(endpoint.WebhookURL); err != nil {
		next := webhookspec.NextAttemptTime(firstAttemptAt, attemptNumber, nil)
		return w.db.MarkWebhookEventFailed(ctx, ev.ID, attemptNumber, nil, err.Error(), next, w.maxAttempts)
	}

	body := ev.Payload
	if err := webhookspec.ValidatePayloadSize(body); err != nil {
		return w.db.MarkWebhookEventFailed(ctx, ev.ID, attemptNumber, nil, err.Error(), time.Now(), w.maxAttempts)
	}
	timestamp := time.Now().Unix()
	headers, err := webhookspec.BuildHeaders(ev.ID, timestamp, body, endpoint.SigningSecrets())
	if err != nil {
		next := webhookspec.NextAttemptTime(firstAttemptAt, attemptNumber, nil)
		return w.db.MarkWebhookEventFailed(ctx, ev.ID, attemptNumber, nil, err.Error(), next, w.maxAttempts)
	}

	start := time.Now()
	statusCode, respBody, resp, deliverErr := w.post(ctx, endpoint.WebhookURL, body, headers)
	durationMs := int(time.Since(start).Milliseconds())

	headerJSON, _ := json.Marshal(headers)
	var respCodePtr *int
	if statusCode > 0 {
		respCodePtr = &statusCode
	}
	errMsg := ""
	if deliverErr != nil {
		errMsg = deliverErr.Error()
	} else if webhookspec.IsRedirectResponse(statusCode) {
		errMsg = webhookspec.FormatAttemptError(statusCode, nil)
	} else if webhookspec.ClassifyDelivery(statusCode, nil) == webhookspec.OutcomeFail {
		errMsg = webhookspec.FormatAttemptError(statusCode, nil)
	}
	if recErr := w.db.RecordWebhookDeliveryAttempt(ctx, ev.ID, attemptNumber, headerJSON, body, respCodePtr, respBody, errMsg, durationMs); recErr != nil {
		slog.Error("record delivery attempt", "eventId", ev.ID, "err", recErr)
	}

	outcome := webhookspec.ClassifyDelivery(statusCode, deliverErr)
	if outcome == webhookspec.OutcomeSuccess {
		return w.db.MarkWebhookEventDelivered(ctx, ev.ID, statusCode)
	}

	if outcome == webhookspec.OutcomeGone {
		reason := "subscriber returned 410 Gone"
		w.disableEndpointAndNotify(ctx, endpoint, ev.EndpointID, reason)
		return w.db.MarkWebhookEventFailed(ctx, ev.ID, attemptNumber, respCodePtr, reason, time.Now(), w.maxAttempts)
	}

	if outcome == webhookspec.OutcomeFail {
		return w.db.MarkWebhookEventFailed(ctx, ev.ID, attemptNumber, respCodePtr, errMsg, time.Now(), w.maxAttempts)
	}

	next := webhookspec.NextAttemptTime(firstAttemptAt, attemptNumber, resp)
	if deliverErr != nil {
		errMsg = deliverErr.Error()
	} else {
		errMsg = webhookspec.FormatAttemptError(statusCode, nil)
	}
	if err := w.db.MarkWebhookEventFailed(ctx, ev.ID, attemptNumber, respCodePtr, errMsg, next, w.maxAttempts); err != nil {
		return err
	}
	if attemptNumber >= w.maxAttempts {
		reason := fmt.Sprintf("delivery failed after %d attempts", w.maxAttempts)
		slog.Warn("webhook delivery exhausted retries", "eventId", ev.ID, "url", endpoint.WebhookURL)
		w.disableEndpointAndNotify(ctx, endpoint, ev.EndpointID, reason)
	}
	return nil
}

func (w *Worker) post(ctx context.Context, url string, body []byte, headers map[string]string) (int, string, *http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, "", nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if readErr != nil {
		return resp.StatusCode, "", resp, readErr
	}
	return resp.StatusCode, string(raw), resp, nil
}
