package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/degoke/tronvent/internal/config"
	internaldb "github.com/degoke/tronvent/internal/db"
	"github.com/degoke/tronvent/internal/store"
)

// webhookDB is the subset of db.Client used by the delivery worker.
type webhookDB interface {
	ClaimPendingWebhookEvents(ctx context.Context, limit int) ([]internaldb.WebhookEvent, error)
	MarkWebhookEventDelivered(ctx context.Context, id string, responseCode int) error
	MarkWebhookEventFailed(ctx context.Context, id string, attemptNumber int, responseCode *int, errMsg string, nextAttempt time.Time, maxAttempts int) error
	RecordWebhookDeliveryAttempt(ctx context.Context, eventID string, attemptNumber int, reqHeaders, reqBody json.RawMessage, responseCode *int, responseBody, errMsg string, durationMs int) error
}

// Worker delivers webhook events from the Postgres outbox.
type Worker struct {
	cfg    *config.Config
	db     webhookDB
	config *store.WebhookConfigStore
	client *http.Client
}

// NewWorker creates a webhook delivery worker.
func NewWorker(cfg *config.Config, db webhookDB, configStore *store.WebhookConfigStore) *Worker {
	timeout := time.Duration(cfg.WebhookHTTPTimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Worker{
		cfg:    cfg,
		db:     db,
		config: configStore,
		client: &http.Client{Timeout: timeout},
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

// dispatchBatch claims and delivers a batch of pending events.
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
	cfg := w.config.Get()
	if cfg == nil || !cfg.IsActive || cfg.WebhookURL == "" {
		next := time.Now().Add(RetrySchedule(ev.AttemptCount + 1))
		return w.db.MarkWebhookEventFailed(ctx, ev.ID, ev.AttemptCount+1, nil, "webhook config not active", next, w.cfg.WebhookMaxAttempts)
	}

	attemptNumber := ev.AttemptCount + 1
	body := ev.Payload
	timestamp := time.Now().Unix()
	signature := Sign(cfg.SigningSecret, timestamp, body)
	headers := BuildHeaders(ev.ID, ev.EventType, timestamp, signature)

	start := time.Now()
	statusCode, respBody, deliverErr := w.post(ctx, cfg.WebhookURL, body, headers)
	durationMs := int(time.Since(start).Milliseconds())

	headerJSON, _ := json.Marshal(headers)
	var respCodePtr *int
	if statusCode > 0 {
		respCodePtr = &statusCode
	}
	errMsg := ""
	if deliverErr != nil {
		errMsg = deliverErr.Error()
	} else if !ShouldRetry(statusCode, nil) && statusCode >= 400 {
		errMsg = FormatAttemptError(statusCode, nil)
	}
	if recErr := w.db.RecordWebhookDeliveryAttempt(ctx, ev.ID, attemptNumber, headerJSON, body, respCodePtr, respBody, errMsg, durationMs); recErr != nil {
		slog.Error("record delivery attempt", "eventId", ev.ID, "err", recErr)
	}

	if deliverErr == nil && statusCode >= 200 && statusCode < 300 {
		return w.db.MarkWebhookEventDelivered(ctx, ev.ID, statusCode)
	}

	if deliverErr == nil && !ShouldRetry(statusCode, nil) {
		return w.db.MarkWebhookEventFailed(ctx, ev.ID, attemptNumber, respCodePtr, errMsg, time.Now(), w.cfg.WebhookMaxAttempts)
	}

	next := time.Now().Add(RetrySchedule(attemptNumber))
	if deliverErr != nil {
		errMsg = deliverErr.Error()
	} else {
		errMsg = FormatAttemptError(statusCode, nil)
	}
	return w.db.MarkWebhookEventFailed(ctx, ev.ID, attemptNumber, respCodePtr, errMsg, next, w.cfg.WebhookMaxAttempts)
}

func (w *Worker) post(ctx context.Context, url string, body []byte, headers map[string]string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if readErr != nil {
		return resp.StatusCode, "", readErr
	}
	return resp.StatusCode, string(raw), nil
}
