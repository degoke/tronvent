package webhookpayload

import (
	"strings"
	"time"
)

const (
	TypeTransactionTRX   = "transaction.trx"
	TypeTransactionTRC20 = "transaction.trc20"
)

// DefaultEventTypes is the default subscription list for new webhook endpoints.
func DefaultEventTypes() []string {
	return []string{TypeTransactionTRX, TypeTransactionTRC20}
}

// Envelope is the Standard Webhooks JSON body (type, timestamp, data).
type Envelope struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Data      TransactionData `json:"data"`
}

// TransactionData is the on-chain transfer fields carried in envelope data.
type TransactionData struct {
	ID                   string `json:"id"`
	TxHash               string `json:"txHash"`
	FromAddress          string `json:"fromAddress"`
	ToAddress            string `json:"toAddress"`
	Amount               string `json:"amount"`
	TokenContractAddress string `json:"tokenContractAddress,omitempty"`
	BlockNumber          int64  `json:"blockNumber"`
	BlockTimestamp       int64  `json:"blockTimestamp"`
	Confirmations        int64  `json:"confirmations"`
}

// TransactionType maps scanner event kinds to hierarchical Standard Webhooks event types.
func TransactionType(kind string) string {
	switch strings.ToUpper(strings.TrimSpace(kind)) {
	case "TRX":
		return TypeTransactionTRX
	case "TRC20":
		return TypeTransactionTRC20
	default:
		return "transaction." + strings.ToLower(kind)
	}
}

// EventOccurredAt formats the chain block time as an ISO 8601 UTC timestamp.
func EventOccurredAt(blockTimestampMs int64) string {
	return time.UnixMilli(blockTimestampMs).UTC().Format(time.RFC3339Nano)
}

// NewTransactionEnvelope builds a delivery payload before the outbox assigns an event id.
func NewTransactionEnvelope(kind string, blockTimestampMs int64, data TransactionData) Envelope {
	return Envelope{
		Type:      TransactionType(kind),
		Timestamp: EventOccurredAt(blockTimestampMs),
		Data:      data,
	}
}
