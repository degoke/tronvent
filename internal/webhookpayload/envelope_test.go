package webhookpayload_test

import (
	"testing"

	"github.com/degoke/tronvent/internal/webhookpayload"
)

func TestTransactionType(t *testing.T) {
	if webhookpayload.TransactionType("TRX") != webhookpayload.TypeTransactionTRX {
		t.Fatal("TRX type")
	}
	if webhookpayload.TransactionType("trc20") != webhookpayload.TypeTransactionTRC20 {
		t.Fatal("TRC20 type")
	}
}

func TestNewTransactionEnvelope(t *testing.T) {
	env := webhookpayload.NewTransactionEnvelope("TRX", 1719234567000, webhookpayload.TransactionData{
		TxHash: "abc",
	})
	if env.Type != webhookpayload.TypeTransactionTRX {
		t.Fatalf("type: %q", env.Type)
	}
	if env.Timestamp == "" {
		t.Fatal("missing timestamp")
	}
	if env.Data.TxHash != "abc" {
		t.Fatal("data")
	}
}
