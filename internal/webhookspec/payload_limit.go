package webhookspec

import "fmt"

// MaxWebhookPayloadBytes is the Standard Webhooks recommended maximum payload size.
const MaxWebhookPayloadBytes = 20 * 1024

// ValidatePayloadSize rejects payloads larger than the spec recommendation.
func ValidatePayloadSize(body []byte) error {
	if len(body) > MaxWebhookPayloadBytes {
		return fmt.Errorf("webhook payload exceeds %d bytes (got %d)", MaxWebhookPayloadBytes, len(body))
	}
	return nil
}
