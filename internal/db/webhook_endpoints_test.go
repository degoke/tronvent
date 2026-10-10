package db_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	internaldb "github.com/degoke/tronvent/internal/db"
	"github.com/degoke/tronvent/internal/webhookspec"
)

func TestUpsertWebhookEndpointPreservesPreviousSigningKey(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	client, err := internaldb.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	secret1, err := webhookspec.GenerateSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	secret2, err := webhookspec.GenerateSigningSecret()
	if err != nil {
		t.Fatal(err)
	}

	ep, err := client.UpsertWebhookEndpoint(ctx, internaldb.WebhookEndpoint{
		WebhookURL:    "https://example.com/rotation-test",
		SigningSecret: secret1,
		IsActive:      true,
		Source:        "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	ep.SigningSecret = secret2
	rotated, err := client.UpsertWebhookEndpoint(ctx, *ep)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.SigningSecretPrevious == "" {
		t.Fatal("expected previous key after rotation")
	}

	ep = rotated
	ep.IsActive = false
	updated, err := client.UpsertWebhookEndpoint(ctx, *ep)
	if err != nil {
		t.Fatal(err)
	}
	if updated.SigningSecretPrevious != rotated.SigningSecretPrevious {
		t.Fatalf("expected previous key preserved on unrelated update, got %q want %q", updated.SigningSecretPrevious, rotated.SigningSecretPrevious)
	}
}

func TestListWebhookEndpointsAllowsShortMigratedWhsec(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	client, err := internaldb.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	short := "this-is-a-long-legacy-secret-key!!"
	normalized, err := webhookspec.NormalizeSigningKey(short)
	if err != nil {
		t.Fatal(err)
	}

	ep, err := client.UpsertWebhookEndpoint(ctx, internaldb.WebhookEndpoint{
		WebhookURL:    "https://example.com/short-migrated",
		SigningSecret: normalized,
		IsActive:      true,
		Source:        "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := client.GetWebhookEndpoint(ctx, ep.ID)
	if err != nil || loaded == nil {
		t.Fatalf("load endpoint: %v", err)
	}
	if loaded.SigningSecret == "" {
		t.Fatal("expected signing secret on load")
	}
}

func TestUpsertWebhookEndpointValidationErrorsAreTyped(t *testing.T) {
	// Validation runs before any query, so a Client without a pool is enough.
	client := &internaldb.Client{}
	secret, err := webhookspec.GenerateSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]internaldb.WebhookEndpoint{
		"http url":     {WebhookURL: "http://203.0.113.10/hook", SigningSecret: secret},
		"private host": {WebhookURL: "https://10.0.0.5/hook", SigningSecret: secret},
		// Built at runtime: invalid on purpose, and no secret-shaped literals for scanners to flag.
		"malformed prefixed key": {WebhookURL: "https://203.0.113.10/hook", SigningSecret: "whsec_" + strings.Repeat("!", 8)},
		"too-short legacy key":   {WebhookURL: "https://203.0.113.10/hook", SigningSecret: strings.Repeat("a", 4)},
		"unknown event":          {WebhookURL: "https://203.0.113.10/hook", SigningSecret: secret, EventTypes: []string{"transaction.trx"}},
	}
	for name, ep := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := client.UpsertWebhookEndpoint(context.Background(), ep)
			if !errors.Is(err, internaldb.ErrInvalidWebhookEndpoint) {
				t.Fatalf("expected ErrInvalidWebhookEndpoint, got %v", err)
			}
			if strings.HasPrefix(err.Error(), "invalid webhook endpoint") {
				t.Fatalf("message should not repeat the sentinel prefix: %q", err)
			}
		})
	}
}

func TestDeleteWebhookEndpointWithHistory(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	client, err := internaldb.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	secret, err := webhookspec.GenerateSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	ep, err := client.UpsertWebhookEndpoint(ctx, internaldb.WebhookEndpoint{
		WebhookURL:    "https://example.com/delete-with-history",
		SigningSecret: secret,
		IsActive:      true,
		Source:        "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	eventID := insertEndpointEvent(t, client, ep.ID, "delivered")
	code := 500
	if err := client.RecordWebhookDeliveryAttempt(ctx, eventID, 1, []byte(`{}`), []byte(`{}`), &code, "", "boom", 10); err != nil {
		t.Fatal(err)
	}

	if err := client.DeleteWebhookEndpoint(ctx, ep.ID, false); err != nil {
		t.Fatalf("delete endpoint with history: %v", err)
	}
	var events, attempts int
	if err := client.Pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE id = $1`, eventID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := client.Pool.QueryRow(ctx, `SELECT count(*) FROM webhook_delivery_attempts WHERE webhook_event_id = $1`, eventID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if events != 0 || attempts != 0 {
		t.Fatalf("expected endpoint history removed, got %d events and %d attempts", events, attempts)
	}
	if err := client.DeleteWebhookEndpoint(ctx, ep.ID, false); !errors.Is(err, internaldb.ErrWebhookEndpointNotFound) {
		t.Fatalf("expected not found on second delete, got %v", err)
	}
}

// insertEndpointEvent adds an outbox row owned only by endpointID (EnqueueWebhookEvent fans out to every endpoint).
func insertEndpointEvent(t *testing.T, client *internaldb.Client, endpointID, status string) string {
	t.Helper()
	var eventID string
	dedupe := fmt.Sprintf("endpoint-event:%s:%d", status, time.Now().UnixNano())
	if err := client.Pool.QueryRow(context.Background(), `
		INSERT INTO webhook_events (event_type, scope, tx_hash, block_number, block_timestamp, payload, dedupe_key, endpoint_id, status)
		VALUES ('transaction.trx.received', 'TRX', $1, 1, 1, '{}', $1, $2, $3)
		RETURNING id::text
	`, dedupe, endpointID, status).Scan(&eventID); err != nil {
		t.Fatal(err)
	}
	return eventID
}

func newTestEndpoint(t *testing.T, client *internaldb.Client, url string) *internaldb.WebhookEndpoint {
	t.Helper()
	secret, err := webhookspec.GenerateSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	ep, err := client.UpsertWebhookEndpoint(context.Background(), internaldb.WebhookEndpoint{
		WebhookURL: url, SigningSecret: secret, IsActive: true, Source: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	return ep
}

func TestDeleteWebhookEndpointRefusesUndeliveredEvents(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	client, err := internaldb.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	for _, status := range []string{"pending", "delivering", "failed"} {
		ep := newTestEndpoint(t, client, "https://example.com/undelivered-"+status)
		eventID := insertEndpointEvent(t, client, ep.ID, status)
		if err := client.DeleteWebhookEndpoint(ctx, ep.ID, false); !errors.Is(err, internaldb.ErrWebhookEndpointHasUndeliveredEvents) {
			t.Fatalf("%s: expected ErrWebhookEndpointHasUndeliveredEvents, got %v", status, err)
		}
		var events int
		if err := client.Pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE id = $1`, eventID).Scan(&events); err != nil || events != 1 {
			t.Fatalf("%s: refused delete must keep the event, got %d (err %v)", status, events, err)
		}
		if err := client.DeleteWebhookEndpoint(ctx, ep.ID, true); err != nil {
			t.Fatalf("%s: forced delete: %v", status, err)
		}
		if err := client.Pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE id = $1`, eventID).Scan(&events); err != nil || events != 0 {
			t.Fatalf("%s: forced delete must remove the event, got %d (err %v)", status, events, err)
		}
	}

	// A dead event is terminal, so it does not block a normal delete.
	ep := newTestEndpoint(t, client, "https://example.com/undelivered-dead")
	insertEndpointEvent(t, client, ep.ID, "dead")
	if err := client.DeleteWebhookEndpoint(ctx, ep.ID, false); err != nil {
		t.Fatalf("dead events must not block delete: %v", err)
	}
}

// An enqueue that already listed endpoint B must not lose A's row when B is deleted
// before the INSERT: the FK failure used to roll back the whole fan-out.
func TestEnqueueWebhookEventSurvivesConcurrentEndpointDelete(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	client, err := internaldb.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	keep := newTestEndpoint(t, client, "https://example.com/race-keep")
	gone := newTestEndpoint(t, client, "https://example.com/race-gone")
	defer func() { _ = client.DeleteWebhookEndpoint(ctx, keep.ID, true) }()
	defer func() { _ = client.DeleteWebhookEndpoint(ctx, gone.ID, true) }()

	// Hold an uncommitted delete of "gone" so the enqueue still lists it but must wait on its row.
	delTx, err := client.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = delTx.Rollback(ctx) }()
	if _, err := delTx.Exec(ctx, `DELETE FROM webhook_endpoints WHERE id = $1`, gone.ID); err != nil {
		t.Fatal(err)
	}

	txHash := fmt.Sprintf("race-%d", time.Now().UnixNano())
	payload := map[string]any{
		"type": "transaction.trx.received", "timestamp": "2024-06-01T12:00:00Z",
		"data": map[string]any{"txHash": txHash, "fromAddress": "TA", "toAddress": "TB", "amount": "1"},
	}
	done := make(chan error, 1)
	go func() {
		_, err := client.EnqueueWebhookEvent(ctx, "transaction.trx.received", "TRX", txHash, 1, time.Now().UnixMilli(), payload)
		done <- err
	}()

	// Wait until the enqueue is blocked on the lock held by the pending delete.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := client.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database()`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("enqueue finished before the delete committed (err %v); race not exercised", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("enqueue never blocked on the endpoint lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := delTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("enqueue must survive the concurrent endpoint delete: %v", err)
	}

	var forKeep, forGone int
	if err := client.Pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE tx_hash = $1 AND endpoint_id = $2`, txHash, keep.ID).Scan(&forKeep); err != nil {
		t.Fatal(err)
	}
	if err := client.Pool.QueryRow(ctx, `SELECT count(*) FROM webhook_events WHERE tx_hash = $1 AND endpoint_id = $2`, txHash, gone.ID).Scan(&forGone); err != nil {
		t.Fatal(err)
	}
	if forKeep != 1 || forGone != 0 {
		t.Fatalf("expected one row for the surviving endpoint and none for the deleted one, got %d and %d", forKeep, forGone)
	}
}
