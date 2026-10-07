package db_test

import (
	"context"
	"os"
	"testing"

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
