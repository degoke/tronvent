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
	"github.com/degoke/tronvent/internal/webhookpayload"
	"github.com/degoke/tronvent/internal/webhookspec"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
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

func (w *workerDB) DeactivateWebhook(_ context.Context, _ string, _ string) error {
	return nil
}

func (w *workerDB) GetWebhookEndpoint(_ context.Context, _ string) (*internaldb.WebhookEndpoint, error) {
	return nil, nil
}

func (w *workerDB) ListWebhookEndpoints(_ context.Context) ([]internaldb.WebhookEndpoint, error) {
	return nil, nil
}

func testSigningSecret(t *testing.T) string {
	secret, _, err := webhookspec.GenerateSigningKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	return secret
}

func TestWorkerDeliversSignedWebhook(t *testing.T) {
	var received atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(standardwebhooks.HeaderWebhookSignature) == "" {
			t.Error("missing webhook-signature header")
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			t.Error("empty body")
		}
		received.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	secret := testSigningSecret(t)
	cfgStore := store.NewWebhookConfigStore(nil)
	cfgStore.UpsertEndpoint(internaldb.WebhookEndpoint{
		ID: "ep-1", WebhookURL: srv.URL, SigningSecret: secret, IsActive: true,
	})

	payload, _ := json.Marshal(webhookpayload.Envelope{
		Type:      webhookpayload.TypeTransactionTRXReceived,
		Timestamp: webhookpayload.EventOccurredAt(1710000000000),
		Data:      webhookpayload.TransactionData{ID: "evt-1", TxHash: "abc"},
	})
	db := &workerDB{events: []internaldb.WebhookEvent{{
		ID: "evt-1", EventType: "TRX", Scope: "TRX", TxHash: "abc",
		Payload: payload, AttemptCount: 0, CreatedAt: time.Now(),
	}}}

	policy := webhookspec.URLPolicy{AllowHTTP: true, AllowPrivateHosts: true}
	worker := webhook.NewWorker(&config.Config{WebhookMaxAttempts: 3, WebhookHTTPTimeoutSeconds: 5}, db, cfgStore, policy)
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

	secret := testSigningSecret(t)
	cfgStore := store.NewWebhookConfigStore(nil)
	cfgStore.UpsertEndpoint(internaldb.WebhookEndpoint{ID: "ep-1", WebhookURL: srv.URL, SigningSecret: secret, IsActive: true})

	payload, _ := json.Marshal(webhookpayload.NewDirectedTransactionEnvelope("TRX", webhookpayload.DirectionReceived, 1710000000000, webhookpayload.TransactionData{ID: "evt-2", TxHash: "x"}))
	failed := false
	db := &retryDB{
		event:  internaldb.WebhookEvent{ID: "evt-2", EventType: "TRX", Scope: "TRX", TxHash: "x", Payload: payload, CreatedAt: time.Now()},
		onFail: func() { failed = true },
	}

	policy := webhookspec.URLPolicy{AllowHTTP: true, AllowPrivateHosts: true}
	worker := webhook.NewWorker(&config.Config{WebhookMaxAttempts: 8}, db, cfgStore, policy)
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
	event         internaldb.WebhookEvent
	claimed       bool
	onFail        func()
	attemptNumber int
	maxAttempts   int
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
	r.attemptNumber = attemptNumber
	r.maxAttempts = maxAttempts
	if r.onFail != nil {
		r.onFail()
	}
	return nil
}

func (r *retryDB) RecordWebhookDeliveryAttempt(_ context.Context, eventID string, attemptNumber int, reqHeaders, reqBody json.RawMessage, responseCode *int, responseBody, errMsg string, durationMs int) error {
	return nil
}

func (r *retryDB) DeactivateWebhook(_ context.Context, _ string, _ string) error {
	return nil
}

func (r *retryDB) GetWebhookEndpoint(_ context.Context, _ string) (*internaldb.WebhookEndpoint, error) {
	return nil, nil
}

func (r *retryDB) ListWebhookEndpoints(_ context.Context) ([]internaldb.WebhookEndpoint, error) {
	return nil, nil
}

func TestWorkerManualRetryAfterMaximumUsesNextAttemptOnce(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	secret := testSigningSecret(t)
	cfgStore := store.NewWebhookConfigStore(nil)
	cfgStore.UpsertEndpoint(internaldb.WebhookEndpoint{ID: "ep-1", WebhookURL: srv.URL, SigningSecret: secret, IsActive: true})
	payload, _ := json.Marshal(webhookpayload.NewDirectedTransactionEnvelope("TRX", webhookpayload.DirectionReceived, 1710000000000, webhookpayload.TransactionData{ID: "evt-dead", TxHash: "dead"}))
	db := &retryDB{
		event: internaldb.WebhookEvent{
			ID: "evt-dead", EventType: "TRX", Scope: "TRX", TxHash: "dead", Payload: payload,
			AttemptCount: 8, CreatedAt: time.Now(),
		},
	}

	policy := webhookspec.URLPolicy{AllowHTTP: true, AllowPrivateHosts: true}
	worker := webhook.NewWorker(&config.Config{WebhookMaxAttempts: 8}, db, cfgStore, policy)
	if err := worker.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if db.attemptNumber != 9 || db.maxAttempts != 8 {
		t.Fatalf("expected one forced attempt at number 9 with max 8, got attempt=%d max=%d", db.attemptNumber, db.maxAttempts)
	}
}
