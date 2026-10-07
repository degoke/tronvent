package webhookpayload_test

import (
	"encoding/json"
	"testing"

	"github.com/degoke/tronvent/internal/webhookpayload"
)

func TestEventSchemaJSON(t *testing.T) {
	raw, err := webhookpayload.EventSchemaJSON(webhookpayload.TypeTransactionTRXReceived)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatal("invalid json schema")
	}
	all, err := webhookpayload.AllEventSchemas()
	if err != nil || len(all) != 4 {
		t.Fatalf("all schemas: %v len=%d", err, len(all))
	}
}
