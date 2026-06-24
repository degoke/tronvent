package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"
)

const (
	HeaderEventID   = "X-Tronvent-Event-Id"
	HeaderEventType = "X-Tronvent-Event-Type"
	HeaderTimestamp = "X-Tronvent-Timestamp"
	HeaderSignature = "X-Tronvent-Signature"
)

// Sign computes the HMAC-SHA256 signature over timestamp + "." + raw payload body.
func Sign(secret string, timestamp int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// BuildHeaders returns the webhook delivery headers for a signed payload.
func BuildHeaders(eventID, eventType string, timestamp int64, signature string) map[string]string {
	return map[string]string{
		"Content-Type":  "application/json",
		HeaderEventID:   eventID,
		HeaderEventType: eventType,
		HeaderTimestamp: strconv.FormatInt(timestamp, 10),
		HeaderSignature: signature,
	}
}

// RetrySchedule returns the backoff duration for the given attempt number (1-based).
// Schedule: 1m, 2m, 5m, 10m, 30m, 1h, 2h, then 2h for subsequent attempts.
func RetrySchedule(attempt int) time.Duration {
	schedule := []time.Duration{
		time.Minute,
		2 * time.Minute,
		5 * time.Minute,
		10 * time.Minute,
		30 * time.Minute,
		time.Hour,
		2 * time.Hour,
	}
	if attempt <= 0 {
		return schedule[0]
	}
	if attempt > len(schedule) {
		return schedule[len(schedule)-1]
	}
	return schedule[attempt-1]
}

// ShouldRetry reports whether a delivery should be retried for the given HTTP status or error.
func ShouldRetry(statusCode int, err error) bool {
	if err != nil {
		return true
	}
	if statusCode == 429 {
		return true
	}
	return statusCode >= 500
}

// FormatAttemptError builds a concise error message for logging/storage.
func FormatAttemptError(statusCode int, err error) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("HTTP %d", statusCode)
}
