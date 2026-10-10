package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	internaldb "github.com/degoke/tronvent/internal/db"
	"github.com/degoke/tronvent/internal/webhookpayload"
	"github.com/degoke/tronvent/internal/webhookspec"
)

func TestScannerRepositoryIntegration(t *testing.T) {
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

	addr := "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	row, created, err := client.AddWatchedAddress(ctx, addr, "test")
	if err != nil {
		t.Fatal(err)
	}
	if !created && row.Address != addr {
		t.Fatalf("unexpected row: %+v", row)
	}

	if err := client.SetScannedBlock(ctx, "TRX", 12345, ""); err != nil {
		t.Fatal(err)
	}
	block, err := client.GetScannedBlock(ctx, "TRX")
	if err != nil || block != 12345 {
		t.Fatalf("cursor = %d err=%v", block, err)
	}

	contract := "TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf"
	if _, _, err := client.AddWatchedContract(ctx, contract, "TEST", "test"); err != nil {
		t.Fatal(err)
	}
	contracts, err := client.ListActiveContracts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range contracts {
		if c == contract {
			found = true
		}
	}
	if !found {
		t.Fatal("contract not in active list")
	}

	whsec, err := webhookspec.GenerateSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	ep, err := client.UpsertPrimaryWebhookEndpointPreserveSecret(ctx, "https://example.com/hook", whsec, true, "test", nil)
	if err != nil || ep.WebhookURL == "" {
		t.Fatalf("webhook upsert: %+v err=%v", ep, err)
	}

	data := map[string]any{
		"txHash": "hash-1", "fromAddress": "TSenderXXX", "toAddress": "TReceiverXXX", "amount": "1.000000",
	}
	envelope := map[string]any{
		"type":      "transaction.trx.received",
		"timestamp": "2024-06-01T12:00:00Z",
		"data":      data,
	}
	evID, err := client.EnqueueWebhookEvent(ctx, webhookpayload.TypeTransactionTRXReceived, "TRX", "hash-1", 100, time.Now().UnixMilli(), envelope)
	if err != nil || evID == "" {
		t.Fatalf("enqueue event: id=%q err=%v", evID, err)
	}
	dupID, err := client.EnqueueWebhookEvent(ctx, webhookpayload.TypeTransactionTRXReceived, "TRX", "hash-1", 100, time.Now().UnixMilli(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if dupID != "" {
		t.Fatal("expected duplicate enqueue to be ignored")
	}
}

func TestWatchedAddressActiveIntegration(t *testing.T) {
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

	watchAddr := "TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf"
	if _, _, err := client.AddWatchedAddress(ctx, watchAddr, "test-active-check"); err != nil {
		t.Fatal(err)
	}

	active, err := client.IsWatchedAddressActive(ctx, watchAddr)
	if err != nil || !active {
		t.Fatalf("expected active watchlist row, active=%v err=%v", active, err)
	}
	batch, err := client.ActiveWatchedAddresses(ctx, []string{watchAddr, "TNotOnWatchlistXXXXXXXXXXXXXXXXXXXXXXX"})
	if err != nil {
		t.Fatal(err)
	}
	if !batch[watchAddr] || batch["TNotOnWatchlistXXXXXXXXXXXXXXXXXXXXXXX"] {
		t.Fatalf("unexpected batch active map: %+v", batch)
	}

	if _, err := client.DeactivateWatchedAddress(ctx, watchAddr); err != nil {
		t.Fatal(err)
	}
	active, err = client.IsWatchedAddressActive(ctx, watchAddr)
	if err != nil || active {
		t.Fatalf("expected inactive after deactivate, active=%v err=%v", active, err)
	}
}

func TestEnqueueWebhookEventRespectsPartialSubscriptions(t *testing.T) {
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

	whsec, err := webhookspec.GenerateSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.UpsertPrimaryWebhookEndpointPreserveSecret(ctx, "https://example.com/hook-partial", whsec, true, "test",
		[]string{webhookpayload.TypeTransactionTRXReceived})
	if err != nil {
		t.Fatal(err)
	}

	received := map[string]any{
		"type": "transaction.trx.received", "timestamp": "2024-06-01T12:00:00Z",
		"data": map[string]any{"txHash": "hash-partial", "fromAddress": "TA", "toAddress": "TB", "amount": "1"},
	}
	broadcasted := map[string]any{
		"type": "transaction.trx.broadcasted", "timestamp": "2024-06-01T12:00:00Z",
		"data": map[string]any{"txHash": "hash-partial", "fromAddress": "TA", "toAddress": "TB", "amount": "1"},
	}
	if id, err := client.EnqueueWebhookEvent(ctx, webhookpayload.TypeTransactionTRXReceived, "TRX", "hash-partial", 100, time.Now().UnixMilli(), received); err != nil || id == "" {
		t.Fatalf("received enqueue: id=%q err=%v", id, err)
	}
	if id, err := client.EnqueueWebhookEvent(ctx, webhookpayload.TypeTransactionTRXBroadcasted, "TRX", "hash-partial", 100, time.Now().UnixMilli(), broadcasted); err != nil || id != "" {
		t.Fatalf("broadcasted should be filtered out: id=%q err=%v", id, err)
	}
}

func TestEnqueueWebhookEventDistinctTransfersSameTx(t *testing.T) {
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

	whsec, err := webhookspec.GenerateSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.UpsertPrimaryWebhookEndpointPreserveSecret(ctx, "https://example.com/hook-multi", whsec, true, "test", webhookpayload.DefaultEventTypes())
	if err != nil {
		t.Fatal(err)
	}

	txHash := "hash-multi-leg"
	for i, from := range []string{"TSenderA", "TSenderB"} {
		env := map[string]any{
			"type": webhookpayload.TypeTransactionTRC20Received, "timestamp": "2024-06-01T12:00:00Z",
			"data": map[string]any{
				"txHash": txHash, "fromAddress": from, "toAddress": "TReceiver", "amount": fmt.Sprintf("%d", i+1),
			},
		}
		if id, err := client.EnqueueWebhookEvent(ctx, webhookpayload.TypeTransactionTRC20Received, "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t", txHash, 100, time.Now().UnixMilli(), env); err != nil || id == "" {
			t.Fatalf("leg %d enqueue: id=%q err=%v", i, id, err)
		}
	}
}

func TestEnqueueWebhookEventRespectsLegacyDedupeKey(t *testing.T) {
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

	whsec, err := webhookspec.GenerateSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	ep, err := client.UpsertPrimaryWebhookEndpointPreserveSecret(ctx, "https://example.com/hook-legacy-dedupe", whsec, true, "test", webhookpayload.DefaultEventTypes())
	if err != nil {
		t.Fatal(err)
	}

	scope := "TRX"
	txHash := "legacy-dedupe-tx"
	from, to, amount := "TSenderXXX", "TReceiverXXX", "1.000000"
	legacyKey := scope + ":" + txHash + ":" + ep.ID
	payload := map[string]any{
		"type": webhookpayload.TypeTransactionTRXReceived, "timestamp": "2024-06-01T12:00:00Z",
		"data": map[string]any{"txHash": txHash, "fromAddress": from, "toAddress": to, "amount": amount},
	}
	raw, _ := json.Marshal(payload)
	_, err = client.Pool.Exec(ctx, `
		INSERT INTO webhook_events (id, event_type, scope, tx_hash, block_number, block_timestamp, payload, dedupe_key, endpoint_id, status)
		VALUES (gen_random_uuid(), $1, $2, $3, 1, 1, $4, $5, $6, 'delivered')
	`, webhookpayload.TypeTransactionTRXReceived, scope, txHash, raw, legacyKey, ep.ID)
	if err != nil {
		t.Fatal(err)
	}

	dupID, err := client.EnqueueWebhookEvent(ctx, webhookpayload.TypeTransactionTRXReceived, scope, txHash, 100, time.Now().UnixMilli(), payload)
	if err != nil {
		t.Fatal(err)
	}
	if dupID != "" {
		t.Fatal("expected legacy dedupe row to block re-enqueue")
	}
}

func TestEnqueueWebhookEventRejectsPayloadTypeMismatch(t *testing.T) {
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

	whsec, err := webhookspec.GenerateSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.UpsertPrimaryWebhookEndpointPreserveSecret(ctx, "https://example.com/hook-mismatch", whsec, true, "test", webhookpayload.DefaultEventTypes())
	if err != nil {
		t.Fatal(err)
	}

	env := map[string]any{
		"type": "transaction.trx.broadcasted", "timestamp": "2024-06-01T12:00:00Z",
		"data": map[string]any{"txHash": "x", "fromAddress": "A", "toAddress": "B", "amount": "1"},
	}
	_, err = client.EnqueueWebhookEvent(ctx, webhookpayload.TypeTransactionTRXReceived, "TRX", "x", 1, time.Now().UnixMilli(), env)
	if err == nil {
		t.Fatal("expected type mismatch error")
	}
}

func TestUpsertWebhookEndpointRejectsLegacyEventTypes(t *testing.T) {
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

	whsec, err := webhookspec.GenerateSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.UpsertWebhookEndpoint(ctx, internaldb.WebhookEndpoint{
		WebhookURL: "https://example.com/legacy-types", SigningSecret: whsec,
		EventTypes: []string{"transaction.trx"}, IsActive: true, Source: "test",
	})
	if err == nil {
		t.Fatal("expected legacy event type rejection")
	}
}

func TestScannerScopeLease(t *testing.T) {
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

	scope := "lease-test-" + fmt.Sprintf("%d", time.Now().UnixNano())
	lease := 30 * time.Second

	_, claimed, err := client.TryClaimScannerScope(ctx, scope, "worker-a", lease)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("worker-a should claim new scope")
	}
	if err := client.SetScannedBlock(ctx, scope, 42, "worker-a"); err != nil {
		t.Fatal(err)
	}

	_, claimed, err = client.TryClaimScannerScope(ctx, scope, "worker-b", lease)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("worker-b should not claim while worker-a holds lease")
	}

	block, err := client.GetScannedBlock(ctx, scope)
	if err != nil || block != 42 {
		t.Fatalf("cursor = %d err=%v", block, err)
	}

	if _, err := client.ReleaseScannerScope(ctx, scope, "worker-a"); err != nil {
		t.Fatal(err)
	}

	highest, claimed, err := client.TryClaimScannerScope(ctx, scope, "worker-b", lease)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed || highest != 42 {
		t.Fatalf("worker-b claim: claimed=%v highest=%d", claimed, highest)
	}
	if _, err := client.ReleaseScannerScope(ctx, scope, "worker-b"); err != nil {
		t.Fatal(err)
	}

	_, _ = client.Pool.Exec(ctx, `DELETE FROM scanner_cursors WHERE scope = $1`, scope)
}

func TestScannerScopeLeaseRenew(t *testing.T) {
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

	scope := "lease-renew-" + fmt.Sprintf("%d", time.Now().UnixNano())
	lease := 30 * time.Second

	_, claimed, err := client.TryClaimScannerScope(ctx, scope, "worker-a", lease)
	if err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	if err := client.RenewScannerScopeLease(ctx, scope, "worker-a", lease); err != nil {
		t.Fatal(err)
	}
	_, claimed, err = client.TryClaimScannerScope(ctx, scope, "worker-b", lease)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("worker-b should not take lease while worker-a holds it after renew")
	}
	if _, err := client.ReleaseScannerScope(ctx, scope, "worker-a"); err != nil {
		t.Fatal(err)
	}
	_, _ = client.Pool.Exec(ctx, `DELETE FROM scanner_cursors WHERE scope = $1`, scope)
}

func TestScannerScopeLeaseExpiry(t *testing.T) {
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

	scope := "lease-expiry-" + fmt.Sprintf("%d", time.Now().UnixNano())
	shortLease := 2 * time.Second

	_, claimed, err := client.TryClaimScannerScope(ctx, scope, "worker-a", shortLease)
	if err != nil || !claimed {
		t.Fatalf("claim: claimed=%v err=%v", claimed, err)
	}
	time.Sleep(3 * time.Second)

	_, claimed, err = client.TryClaimScannerScope(ctx, scope, "worker-b", shortLease)
	if err != nil {
		t.Fatal(err)
	}
	if !claimed {
		t.Fatal("worker-b should claim after worker-a lease expired")
	}
	if _, err := client.ReleaseScannerScope(ctx, scope, "worker-b"); err != nil {
		t.Fatal(err)
	}
	_, _ = client.Pool.Exec(ctx, `DELETE FROM scanner_cursors WHERE scope = $1`, scope)
}

func TestScannerCursorLeasesEnabled(t *testing.T) {
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

	ok, err := client.ScannerCursorLeasesEnabled(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected scanner cursor lease columns")
	}
}

func TestSetScannedBlockLeaseConflict(t *testing.T) {
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

	scope := "lease-conflict-" + fmt.Sprintf("%d", time.Now().UnixNano())
	lease := 30 * time.Second

	_, claimed, err := client.TryClaimScannerScope(ctx, scope, "worker-a", lease)
	if err != nil || !claimed {
		t.Fatalf("claim worker-a: claimed=%v err=%v", claimed, err)
	}
	if err := client.SetScannedBlock(ctx, scope, 10, "worker-a"); err != nil {
		t.Fatal(err)
	}
	if err := client.SetScannedBlock(ctx, scope, 11, "worker-b"); !errors.Is(err, internaldb.ErrCursorLeaseConflict) {
		t.Fatalf("expected lease conflict, got %v", err)
	}
	if _, err := client.ReleaseScannerScope(ctx, scope, "worker-a"); err != nil {
		t.Fatal(err)
	}
	_, _ = client.Pool.Exec(ctx, `DELETE FROM scanner_cursors WHERE scope = $1`, scope)
}

func TestRequireScannerCursorLeases(t *testing.T) {
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

	if err := client.RequireScannerCursorLeases(ctx); err != nil {
		t.Fatal(err)
	}
}
