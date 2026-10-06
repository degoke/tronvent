package db_test

import (
	"context"
	"os"
	"testing"
	"time"

	internaldb "github.com/degoke/tronvent/internal/db"
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

	if err := client.SetScannedBlock(ctx, "TRX", 12345); err != nil {
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

	envelope := map[string]any{
		"type":      "transaction.trx",
		"timestamp": "2024-06-01T12:00:00Z",
		"data":      map[string]any{"txHash": "hash-1"},
	}
	evID, err := client.EnqueueWebhookEvent(ctx, "TRX", "TRX", "hash-1", 100, time.Now().UnixMilli(), envelope)
	if err != nil || evID == "" {
		t.Fatalf("enqueue event: id=%q err=%v", evID, err)
	}
	dupID, err := client.EnqueueWebhookEvent(ctx, "TRX", "TRX", "hash-1", 100, time.Now().UnixMilli(), envelope)
	if err != nil {
		t.Fatal(err)
	}
	if dupID != "" {
		t.Fatal("expected duplicate enqueue to be ignored")
	}
}
