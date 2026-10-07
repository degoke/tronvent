package webhookpayload_test

import (
	"testing"

	"github.com/degoke/tronvent/internal/webhookpayload"
)

func TestTransferEventType(t *testing.T) {
	if webhookpayload.TransferEventType("TRX", webhookpayload.DirectionReceived) != webhookpayload.TypeTransactionTRXReceived {
		t.Fatal("trx received")
	}
	if webhookpayload.TransferEventType("TRC20", webhookpayload.DirectionBroadcasted) != webhookpayload.TypeTransactionTRC20Broadcasted {
		t.Fatal("trc20 broadcasted")
	}
	if webhookpayload.TransferEventType("TRX", "invalid") != "" {
		t.Fatal("invalid direction")
	}
}

func TestEndpointSubscribesEmptyUsesDefaults(t *testing.T) {
	if !webhookpayload.EndpointSubscribes(nil, webhookpayload.TypeTransactionTRXReceived) {
		t.Fatal("empty subscriptions should default to all types")
	}
	if webhookpayload.EndpointSubscribes([]string{webhookpayload.TypeTransactionTRXReceived}, webhookpayload.TypeTransactionTRXBroadcasted) {
		t.Fatal("partial subscription must not include broadcasted")
	}
}

func TestValidateEventTypes(t *testing.T) {
	if err := webhookpayload.ValidateEventTypes([]string{"transaction.trx"}); err == nil {
		t.Fatal("legacy type must be rejected")
	}
	if err := webhookpayload.ValidateEventTypes(webhookpayload.DefaultEventTypes()); err != nil {
		t.Fatal(err)
	}
}

func TestNewDirectedTransactionEnvelope(t *testing.T) {
	env := webhookpayload.NewDirectedTransactionEnvelope("TRX", webhookpayload.DirectionReceived, 1719234567000, webhookpayload.TransactionData{
		TxHash: "abc",
	})
	if env.Type != webhookpayload.TypeTransactionTRXReceived {
		t.Fatalf("type: %q", env.Type)
	}
	if env.Timestamp == "" {
		t.Fatal("missing timestamp")
	}
	if env.Data.TxHash != "abc" {
		t.Fatal("data")
	}
}
