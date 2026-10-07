package webhook_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/degoke/tronvent/internal/config"
	internaldb "github.com/degoke/tronvent/internal/db"
	"github.com/degoke/tronvent/internal/store"
	"github.com/degoke/tronvent/internal/webhook"
	"github.com/degoke/tronvent/internal/webhookpayload"
	"github.com/degoke/tronvent/internal/webhookspec"
)

type lifecycleDB struct {
	event            internaldb.WebhookEvent
	claimed          bool
	deactivatedID    string
	deactivateReason string
	attemptNumber    int
	dbEndpoint       *internaldb.WebhookEndpoint
}

func (d *lifecycleDB) ClaimPendingWebhookEvents(_ context.Context, _ int) ([]internaldb.WebhookEvent, error) {
	if d.claimed {
		return nil, nil
	}
	d.claimed = true
	return []internaldb.WebhookEvent{d.event}, nil
}

func (d *lifecycleDB) MarkWebhookEventDelivered(_ context.Context, _ string, _ int) error {
	return nil
}

func (d *lifecycleDB) MarkWebhookEventFailed(_ context.Context, _ string, attemptNumber int, _ *int, _ string, _ time.Time, maxAttempts int) error {
	d.attemptNumber = attemptNumber
	return nil
}

func (d *lifecycleDB) RecordWebhookDeliveryAttempt(_ context.Context, _ string, _ int, _, _ json.RawMessage, _ *int, _, _ string, _ int) error {
	return nil
}

func (d *lifecycleDB) DeactivateWebhook(_ context.Context, endpointID, reason string) error {
	d.deactivatedID = endpointID
	d.deactivateReason = reason
	return nil
}

func (d *lifecycleDB) GetWebhookEndpoint(_ context.Context, endpointID string) (*internaldb.WebhookEndpoint, error) {
	if d.dbEndpoint != nil && d.dbEndpoint.ID == endpointID {
		ep := *d.dbEndpoint
		return &ep, nil
	}
	return nil, nil
}

func (d *lifecycleDB) ListWebhookEndpoints(_ context.Context) ([]internaldb.WebhookEndpoint, error) {
	if d.dbEndpoint != nil {
		return []internaldb.WebhookEndpoint{*d.dbEndpoint}, nil
	}
	return nil, nil
}

func TestWorker410DisablesEndpointWithoutEndpointIDOnEvent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()

	secret := testSigningSecret(t)
	cfgStore := store.NewWebhookConfigStore(nil)
	cfgStore.UpsertEndpoint(internaldb.WebhookEndpoint{
		ID: "ep-primary", WebhookURL: srv.URL, SigningSecret: secret, IsActive: true,
	})
	payload, _ := json.Marshal(webhookpayload.NewDirectedTransactionEnvelope("TRX", webhookpayload.DirectionReceived, 1710000000000, webhookpayload.TransactionData{ID: "evt-410", TxHash: "gone"}))
	db := &lifecycleDB{event: internaldb.WebhookEvent{
		ID: "evt-410", EventType: "TRX", Scope: "TRX", TxHash: "gone", Payload: payload, CreatedAt: time.Now(),
		// endpoint_id empty — delivery uses primary; disable must still target ep-primary.
	}}

	policy := webhookspec.URLPolicy{AllowHTTP: true, AllowPrivateHosts: true}
	worker := webhook.NewWorker(&config.Config{WebhookMaxAttempts: 10}, db, cfgStore, policy)
	if err := worker.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if db.deactivatedID != "ep-primary" {
		t.Fatalf("expected endpoint ep-primary deactivated, got %q", db.deactivatedID)
	}
	if db.attemptNumber != 10 {
		t.Fatalf("expected event marked dead at max attempts, got attempt %d", db.attemptNumber)
	}
}

func TestWorker4xxMarksDeadWithoutDisablingEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	secret := testSigningSecret(t)
	cfgStore := store.NewWebhookConfigStore(nil)
	cfgStore.UpsertEndpoint(internaldb.WebhookEndpoint{
		ID: "ep-1", WebhookURL: srv.URL, SigningSecret: secret, IsActive: true,
	})
	payload, _ := json.Marshal(webhookpayload.NewDirectedTransactionEnvelope("TRX", webhookpayload.DirectionReceived, 1710000000000, webhookpayload.TransactionData{ID: "evt-404", TxHash: "nf"}))
	db := &lifecycleDB{event: internaldb.WebhookEvent{
		ID: "evt-404", EndpointID: "ep-1", EventType: "TRX", Scope: "TRX", TxHash: "nf", Payload: payload, CreatedAt: time.Now(),
	}}

	policy := webhookspec.URLPolicy{AllowHTTP: true, AllowPrivateHosts: true}
	worker := webhook.NewWorker(&config.Config{WebhookMaxAttempts: 10}, db, cfgStore, policy)
	if err := worker.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if db.deactivatedID != "" {
		t.Fatalf("expected no endpoint disable on 404, got deactivate %q", db.deactivatedID)
	}
	if db.attemptNumber != 10 {
		t.Fatalf("expected single-shot dead (attempt=max), got %d", db.attemptNumber)
	}
}

func TestWorkerLoadsEndpointFromDBWhenStoreCacheMiss(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	secret := testSigningSecret(t)
	cfgStore := store.NewWebhookConfigStore(nil)
	ep := internaldb.WebhookEndpoint{
		ID: "ep-db", WebhookURL: srv.URL, SigningSecret: secret, IsActive: true,
	}
	payload, _ := json.Marshal(webhookpayload.NewDirectedTransactionEnvelope("TRX", webhookpayload.DirectionReceived, 1710000000000, webhookpayload.TransactionData{ID: "evt-db", TxHash: "db"}))
	db := &lifecycleDB{
		event: internaldb.WebhookEvent{
			ID: "evt-db", EndpointID: "ep-db", EventType: "TRX", Scope: "TRX", TxHash: "db", Payload: payload, CreatedAt: time.Now(),
		},
		dbEndpoint: &ep,
	}

	policy := webhookspec.URLPolicy{AllowHTTP: true, AllowPrivateHosts: true}
	worker := webhook.NewWorker(&config.Config{WebhookMaxAttempts: 10}, db, cfgStore, policy)
	if err := worker.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected delivery via DB-loaded endpoint, calls=%d", calls)
	}
	if cfgStore.GetEndpoint("ep-db") == nil {
		t.Fatal("expected endpoint cached in store after DB load")
	}
}

func TestWorkerChronicFailureDisablesEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	secret := testSigningSecret(t)
	cfgStore := store.NewWebhookConfigStore(nil)
	cfgStore.UpsertEndpoint(internaldb.WebhookEndpoint{
		ID: "ep-chronic", WebhookURL: srv.URL, SigningSecret: secret, IsActive: true,
	})
	payload, _ := json.Marshal(webhookpayload.NewDirectedTransactionEnvelope("TRX", webhookpayload.DirectionReceived, 1710000000000, webhookpayload.TransactionData{ID: "evt-chronic", TxHash: "c"}))
	db := &lifecycleDB{event: internaldb.WebhookEvent{
		ID: "evt-chronic", EndpointID: "ep-chronic", EventType: "TRX", Scope: "TRX", TxHash: "c",
		Payload: payload, AttemptCount: 1, CreatedAt: time.Now(),
	}}

	policy := webhookspec.URLPolicy{AllowHTTP: true, AllowPrivateHosts: true}
	worker := webhook.NewWorker(&config.Config{WebhookMaxAttempts: 2}, db, cfgStore, policy)
	if err := worker.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if db.deactivatedID != "ep-chronic" {
		t.Fatalf("expected chronic failure to disable endpoint, got %q", db.deactivatedID)
	}
	if db.attemptNumber != 2 {
		t.Fatalf("expected attempt 2 on final failure, got %d", db.attemptNumber)
	}
}

func TestWorkerInactiveEndpointMarksDeadWithoutHTTP(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	secret := testSigningSecret(t)
	cfgStore := store.NewWebhookConfigStore(nil)
	cfgStore.UpsertEndpoint(internaldb.WebhookEndpoint{
		ID: "ep-off", WebhookURL: srv.URL, SigningSecret: secret, IsActive: false,
	})
	payload, _ := json.Marshal(webhookpayload.NewDirectedTransactionEnvelope("TRX", webhookpayload.DirectionReceived, 1710000000000, webhookpayload.TransactionData{ID: "evt-off", TxHash: "off"}))
	db := &lifecycleDB{event: internaldb.WebhookEvent{
		ID: "evt-off", EndpointID: "ep-off", EventType: "TRX", Scope: "TRX", TxHash: "off", Payload: payload, CreatedAt: time.Now(),
	}}

	policy := webhookspec.URLPolicy{AllowHTTP: true, AllowPrivateHosts: true}
	worker := webhook.NewWorker(&config.Config{WebhookMaxAttempts: 10}, db, cfgStore, policy)
	if err := worker.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("expected no HTTP call for inactive endpoint, got %d", calls)
	}
	if db.attemptNumber != 10 {
		t.Fatalf("expected dead immediately, attempt=%d", db.attemptNumber)
	}
}
