package webhookspec_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/degoke/tronvent/internal/webhookspec"
	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

func TestBuildHeadersStandardWebhooks(t *testing.T) {
	secret, err := webhookspec.GenerateSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"type":"transaction.trx","timestamp":"2024-01-01T00:00:00Z","data":{"id":"x","txHash":"abc"}}`)
	const (
		eventID = "550e8400-e29b-41d4-a716-446655440000"
		ts      = int64(1710000000)
	)

	headers, err := webhookspec.BuildHeaders(eventID, ts, body, []string{secret})
	if err != nil {
		t.Fatal(err)
	}
	if headers[standardwebhooks.HeaderWebhookSignature] == "" {
		t.Fatal("missing webhook-signature")
	}

	wh, err := standardwebhooks.NewWebhook(secret)
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set(standardwebhooks.HeaderWebhookID, headers[standardwebhooks.HeaderWebhookID])
	h.Set(standardwebhooks.HeaderWebhookTimestamp, headers[standardwebhooks.HeaderWebhookTimestamp])
	h.Set(standardwebhooks.HeaderWebhookSignature, headers[standardwebhooks.HeaderWebhookSignature])
	if err := wh.VerifyIgnoringTimestamp(body, h); err != nil {
		t.Fatalf("standard signature verify: %v", err)
	}
}

func TestBuildHeadersRejectsDottedID(t *testing.T) {
	secret, _ := webhookspec.GenerateSigningSecret()
	_, err := webhookspec.BuildHeaders("bad.id", time.Now().Unix(), []byte("{}"), []string{secret})
	if err == nil {
		t.Fatal("expected error for dotted webhook id")
	}
}
