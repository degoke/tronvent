package webhook_test

import (
	"context"
	"encoding/json"
	"net"
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
	nextAttempt      time.Time
	lastError        string
	recordedAttempts []int
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

func (d *lifecycleDB) MarkWebhookEventFailed(_ context.Context, _ string, attemptNumber int, _ *int, errMsg string, nextAttempt time.Time, maxAttempts int) error {
	d.attemptNumber = attemptNumber
	d.lastError = errMsg
	d.nextAttempt = nextAttempt
	return nil
}

func (d *lifecycleDB) RecordWebhookDeliveryAttempt(_ context.Context, _ string, attemptNumber int, _, _ json.RawMessage, _ *int, _, _ string, _ int) error {
	d.recordedAttempts = append(d.recordedAttempts, attemptNumber)
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

// dnsLifecycleDB builds a worker over an endpoint on a public-looking https URL whose DNS answers
// as given, delivering one event that has already been tried attemptCount times.
func dnsWorker(t *testing.T, lookup func(string) ([]net.IP, error), url string, attemptCount, maxAttempts int) (*lifecycleDB, *webhook.Worker) {
	t.Helper()
	cfgStore := store.NewWebhookConfigStore(nil)
	cfgStore.UpsertEndpoint(internaldb.WebhookEndpoint{
		ID: "ep-dns", WebhookURL: url, SigningSecret: testSigningSecret(t), IsActive: true,
	})
	payload, _ := json.Marshal(webhookpayload.NewDirectedTransactionEnvelope("TRX", webhookpayload.DirectionReceived, 1710000000000, webhookpayload.TransactionData{ID: "evt-dns", TxHash: "dns"}))
	db := &lifecycleDB{event: internaldb.WebhookEvent{
		ID: "evt-dns", EndpointID: "ep-dns", EventType: "TRX", Scope: "TRX", TxHash: "dns",
		Payload: payload, AttemptCount: attemptCount, CreatedAt: time.Now(),
	}}
	policy := webhookspec.URLPolicy{LookupIP: lookup}
	return db, webhook.NewWorker(&config.Config{WebhookMaxAttempts: maxAttempts}, db, cfgStore, policy)
}

func TestWorkerDNSFailureBacksOffInsteadOfMarkingDead(t *testing.T) {
	failures := map[string]func(string) ([]net.IP, error){
		"NXDOMAIN": func(string) ([]net.IP, error) {
			return nil, &net.DNSError{Err: "no such host", Name: "hooks.example.com", IsNotFound: true}
		},
		"resolver timeout": func(string) ([]net.IP, error) {
			return nil, &net.DNSError{Err: "i/o timeout", Name: "hooks.example.com", IsTimeout: true}
		},
	}
	for name, lookup := range failures {
		t.Run(name, func(t *testing.T) {
			db, worker := dnsWorker(t, lookup, "https://hooks.example.com/hook", 0, 10)
			before := time.Now()
			if err := worker.DispatchOnce(context.Background()); err != nil {
				t.Fatal(err)
			}

			// A dead event would have used up every attempt at once (attemptNumber == max).
			if db.attemptNumber != 1 {
				t.Fatalf("a DNS failure is attempt 1 of 10, got attempt %d (event ended early)", db.attemptNumber)
			}
			if !db.nextAttempt.After(before) {
				t.Fatalf("expected a retry scheduled after %v, got %v", before, db.nextAttempt)
			}
			if len(db.recordedAttempts) != 1 || db.recordedAttempts[0] != 1 {
				t.Fatalf("the attempt must be recorded, got %v", db.recordedAttempts)
			}
			if db.lastError == "" {
				t.Fatal("the failure reason must be kept for the delivery log")
			}
			if db.deactivatedID != "" {
				t.Fatalf("one DNS failure must not disable the endpoint, got %q", db.deactivatedID)
			}
		})
	}
}

func TestWorkerDNSFailureOnTheLastAttemptDisablesTheEndpoint(t *testing.T) {
	lookup := func(string) ([]net.IP, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "hooks.example.com", IsNotFound: true}
	}
	db, worker := dnsWorker(t, lookup, "https://hooks.example.com/hook", 1, 2)
	if err := worker.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if db.attemptNumber != 2 {
		t.Fatalf("expected the final attempt (2), got %d", db.attemptNumber)
	}
	if db.deactivatedID != "ep-dns" {
		t.Fatalf("exhausting the attempts must disable the endpoint and notify its owner, got %q", db.deactivatedID)
	}
}

func TestWorkerPolicyViolationStillMarksDead(t *testing.T) {
	lookup := func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("93.184.216.34")}, nil }
	// Plain http is not allowed by this policy: retrying can never make it valid.
	db, worker := dnsWorker(t, lookup, "http://hooks.example.com/hook", 0, 10)
	if err := worker.DispatchOnce(context.Background()); err != nil {
		t.Fatal(err)
	}

	if db.attemptNumber != 10 {
		t.Fatalf("a URL that breaks the policy must end the event at once, got attempt %d", db.attemptNumber)
	}
	if len(db.recordedAttempts) != 0 {
		t.Fatalf("nothing was sent, so no attempt is recorded, got %v", db.recordedAttempts)
	}
}
