package webhookspec_test

import (
	"testing"

	"github.com/degoke/tronvent/internal/webhookspec"
)

func TestValidatePayloadSize(t *testing.T) {
	if err := webhookspec.ValidatePayloadSize(make([]byte, webhookspec.MaxWebhookPayloadBytes)); err != nil {
		t.Fatal(err)
	}
	if err := webhookspec.ValidatePayloadSize(make([]byte, webhookspec.MaxWebhookPayloadBytes+1)); err == nil {
		t.Fatal("expected error for oversized payload")
	}
}
