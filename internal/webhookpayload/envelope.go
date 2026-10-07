package webhookpayload

import (
	"fmt"
	"strings"
	"time"
)

const (
	DirectionReceived    = "received"
	DirectionBroadcasted = "broadcasted"

	TypeTransactionTRXReceived      = "transaction.trx.received"
	TypeTransactionTRXBroadcasted   = "transaction.trx.broadcasted"
	TypeTransactionTRC20Received    = "transaction.trc20.received"
	TypeTransactionTRC20Broadcasted = "transaction.trc20.broadcasted"
)

// DefaultEventTypes is the default subscription list for new webhook endpoints.
func DefaultEventTypes() []string {
	return []string{
		TypeTransactionTRXReceived,
		TypeTransactionTRXBroadcasted,
		TypeTransactionTRC20Received,
		TypeTransactionTRC20Broadcasted,
	}
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

// TransferEventType maps a scanner kind and watchlist match direction to a webhook type id.
func TransferEventType(kind, direction string) string {
	dir := strings.ToLower(strings.TrimSpace(direction))
	switch strings.ToUpper(strings.TrimSpace(kind)) {
	case "TRX":
		switch dir {
		case DirectionReceived:
			return TypeTransactionTRXReceived
		case DirectionBroadcasted:
			return TypeTransactionTRXBroadcasted
		}
	case "TRC20":
		switch dir {
		case DirectionReceived:
			return TypeTransactionTRC20Received
		case DirectionBroadcasted:
			return TypeTransactionTRC20Broadcasted
		}
	}
	return ""
}

// IsKnownEventType reports whether eventType is a supported webhook subscription and payload type.
func IsKnownEventType(eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	for _, t := range ListEventSchemaTypes() {
		if t == eventType {
			return true
		}
	}
	return false
}

// ValidateEventTypes returns an error when any id is missing or not a known event type.
func ValidateEventTypes(types []string) error {
	if len(types) == 0 {
		return nil
	}
	for _, t := range types {
		t = strings.TrimSpace(t)
		if t == "" {
			return fmt.Errorf("eventTypes must not contain empty strings")
		}
		if !IsKnownEventType(t) {
			return fmt.Errorf("unknown event type %q", t)
		}
	}
	return nil
}

// EffectiveEventSubscriptions returns the types used for fanout filtering.
// An empty stored list means the default subscription set (all four direction types).
func EffectiveEventSubscriptions(subscriptions []string) []string {
	if len(subscriptions) == 0 {
		return DefaultEventTypes()
	}
	return subscriptions
}

// EndpointSubscribes reports whether any configured subscription includes eventType.
func EndpointSubscribes(subscriptions []string, eventType string) bool {
	for _, t := range EffectiveEventSubscriptions(subscriptions) {
		if strings.TrimSpace(t) == eventType {
			return true
		}
	}
	return false
}

// NormalizeEventType returns the trimmed Standard Webhooks type id.
func NormalizeEventType(eventType string) string {
	return strings.TrimSpace(eventType)
}

// EventOccurredAt formats the chain block time as an ISO 8601 UTC timestamp.
func EventOccurredAt(blockTimestampMs int64) string {
	return time.UnixMilli(blockTimestampMs).UTC().Format(time.RFC3339Nano)
}

// NewDirectedTransactionEnvelope builds an envelope with a received/broadcasted type.
func NewDirectedTransactionEnvelope(kind, direction string, blockTimestampMs int64, data TransactionData) Envelope {
	return Envelope{
		Type:      TransferEventType(kind, direction),
		Timestamp: EventOccurredAt(blockTimestampMs),
		Data:      data,
	}
}
