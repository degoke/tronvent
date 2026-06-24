package webhook_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/degoke/tronvent/internal/config"
	internaldb "github.com/degoke/tronvent/internal/db"
	"github.com/degoke/tronvent/internal/store"
	"github.com/degoke/tronvent/internal/webhook"
)

type workerDB struct {
	events []internaldb.WebhookEvent
}

func (w *workerDB) ClaimPendingWebhookEvents(_ context.Context, limit int) ([]internaldb.WebhookEvent, error) {
	if len(w.events) == 0 {
		return nil, nil
	}
	out := w.events
	w.events = nil
	return out, nil
}

func (w *workerDB) MarkWebhookEventDelivered(_ context.Context, id string, responseCode int) error {
	return nil
}

func (w *workerDB) MarkWebhookEventFailed(_ context.Context, id string, attemptNumber int, responseCode *int, errMsg string, nextAttempt time.Time, maxAttempts int) error {
	return nil
}

func (w *workerDB) RecordWebhookDeliveryAttempt(_ context.Context, eventID string, attemptNumber int, reqHeaders, reqBody json.RawMessage, responseCode *int, responseBody, errMsg string, durationMs int) error {
	return nil
}

func TestWorkerDeliversSignedWebhook(t *testing.T) {
	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(webhook.HeaderSignature) == "" {
			t.Error("missing signature header")
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			t.Error("empty body")
		}
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfgStore := store.NewWebhookConfigStore(nil)
	cfgStore.Set(&internaldb.WebhookConfig{
		WebhookURL:    srv.URL,
		SigningSecret: "test-secret",
		IsActive:      true,
	})

	payload, _ := json.Marshal(map[string]string{"type": "TRX", "txHash": "abc"})
	db := &workerDB{events: []internaldb.WebhookEvent{{
		ID: "evt-1", EventType: "TRX", Scope: "TRX", TxHash: "abc",
		Payload: payload, AttemptCount: 0,
	}}}

	worker := webhook.NewWorker(&config.Config{WebhookMaxAttempts: 3, WebhookHTTPTimeoutSeconds: 5}, db, cfgStore)
	if err := worker.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if received.Load() != 1 {
		t.Fatalf("expected 1 delivery, got %d", received.Load())
	}
}

func TestWorkerRetriesOn5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	cfgStore := store.NewWebhookConfigStore(nil)
	cfgStore.Set(&internaldb.WebhookConfig{WebhookURL: srv.URL, SigningSecret: "secret", IsActive: true})

	payload, _ := json.Marshal(map[string]string{"type": "TRX"})
	failed := false
	db := &retryDB{
		event:  internaldb.WebhookEvent{ID: "evt-2", EventType: "TRX", Scope: "TRX", TxHash: "x", Payload: payload},
		onFail: func() { failed = true },
	}

	worker := webhook.NewWorker(&config.Config{WebhookMaxAttempts: 8}, db, cfgStore)
	if err := worker.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 HTTP call, got %d", calls.Load())
	}
	if !failed {
		t.Fatal("expected failed mark for 5xx response")
	}
}

type retryDB struct {
	event   internaldb.WebhookEvent
	claimed bool
	onFail  func()
}

func (r *retryDB) ClaimPendingWebhookEvents(_ context.Context, limit int) ([]internaldb.WebhookEvent, error) {
	if r.claimed {
		return nil, nil
	}
	r.claimed = true
	return []internaldb.WebhookEvent{r.event}, nil
}

func (r *retryDB) MarkWebhookEventDelivered(_ context.Context, id string, responseCode int) error {
	return nil
}

func (r *retryDB) MarkWebhookEventFailed(_ context.Context, id string, attemptNumber int, responseCode *int, errMsg string, nextAttempt time.Time, maxAttempts int) error {
	if r.onFail != nil {
		r.onFail()
	}
	return nil
}

func (r *retryDB) RecordWebhookDeliveryAttempt(_ context.Context, eventID string, attemptNumber int, reqHeaders, reqBody json.RawMessage, responseCode *int, responseBody, errMsg string, durationMs int) error {
	return nil
}
