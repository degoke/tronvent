package webhookspec

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
)

func newStandardWebhook(secret string) (*standardwebhooks.Webhook, error) {
	if err := ValidateSigningSecret(secret); err != nil {
		return nil, err
	}
	return standardwebhooks.NewWebhook(secret)
}

// BuildHeaders returns Standard Webhooks delivery headers, signing with all configured keys for rotation.
func BuildHeaders(eventID string, attemptTimestamp int64, body []byte, signingKeys []string) (map[string]string, error) {
	if strings.Contains(eventID, ".") {
		return nil, fmt.Errorf("webhook id must not contain '.'")
	}
	keys := compactSecrets(signingKeys)
	if len(keys) == 0 {
		return nil, fmt.Errorf("at least one signing key is required")
	}
	ts := time.Unix(attemptTimestamp, 0)
	sigs := make([]string, 0, len(keys))
	for _, key := range keys {
		sig, err := signWithKey(key, eventID, ts, body)
		if err != nil {
			return nil, err
		}
		sigs = append(sigs, sig)
	}
	return map[string]string{
		"Content-Type":                          "application/json",
		standardwebhooks.HeaderWebhookID:        eventID,
		standardwebhooks.HeaderWebhookTimestamp: strconv.FormatInt(attemptTimestamp, 10),
		standardwebhooks.HeaderWebhookSignature: strings.Join(sigs, " "),
	}, nil
}

func compactSecrets(secrets []string) []string {
	out := make([]string, 0, len(secrets))
	seen := map[string]struct{}{}
	for _, s := range secrets {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// FormatAttemptError builds a concise error message for logging/storage.
func FormatAttemptError(statusCode int, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("HTTP %d", statusCode)
}
