package webhookpayload_test

import (
	"encoding/json"
	"testing"

	"github.com/degoke/tronvent/internal/webhookpayload"
)

func TestEventSchemaJSON(t *testing.T) {
	raw, err := webhookpayload.EventSchemaJSON(webhookpayload.TypeTransactionTRX)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatal("invalid json schema")
	}
	all, err := webhookpayload.AllEventSchemas()
	if err != nil || len(all) < 2 {
		t.Fatalf("all schemas: %v", err)
	}
}
