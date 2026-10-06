package webhookspec

import (
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SpecDeliveryAttempts is the number of delivery attempts in the Standard Webhooks retry schedule.
const SpecDeliveryAttempts = 10

// retryOffsetsFromStart is the spec-recommended time since the first attempt for each attempt (1-based index).
// Times since the first delivery attempt per the Standard Webhooks retry table.
var retryOffsetsFromStart = []time.Duration{
	0,
	5 * time.Second,
	305 * time.Second,
	2105 * time.Second,
	9305 * time.Second,
	27305 * time.Second,
	63305 * time.Second,
	113705 * time.Second,
	185705 * time.Second,
	272105 * time.Second,
}

// NextAttemptTime schedules the next delivery attempt using the spec offsets, jitter, and optional Retry-After.
func NextAttemptTime(firstAttemptAt time.Time, completedAttempt int, resp *http.Response) time.Time {
	if completedAttempt < 1 {
		completedAttempt = 1
	}
	idx := completedAttempt
	if idx >= len(retryOffsetsFromStart) {
		idx = len(retryOffsetsFromStart) - 1
	}
	base := firstAttemptAt.Add(retryOffsetsFromStart[idx])
	base = addJitter(base, retryOffsetsFromStart[idx])
	if resp != nil {
		if after := parseRetryAfter(resp.Header); after.After(base) {
			return after
		}
	}
	return base
}

func addJitter(scheduled time.Time, offset time.Duration) time.Time {
	if offset <= 0 {
		return scheduled
	}
	// Up to 10% jitter on the offset window.
	maxJitter := offset / 10
	if maxJitter <= 0 {
		return scheduled
	}
	j := time.Duration(rand.Int63n(int64(maxJitter) + 1))
	return scheduled.Add(j)
}

func parseRetryAfter(h http.Header) time.Time {
	raw := strings.TrimSpace(h.Get("Retry-After"))
	if raw == "" {
		return time.Time{}
	}
	if secs, err := strconv.ParseInt(raw, 10, 64); err == nil && secs >= 0 {
		return time.Now().Add(time.Duration(secs) * time.Second)
	}
	if t, err := http.ParseTime(raw); err == nil {
		return t
	}
	return time.Time{}
}

// DeliveryOutcome classifies an HTTP delivery result per Standard Webhooks guidance.
type DeliveryOutcome int

const (
	OutcomeSuccess DeliveryOutcome = iota
	OutcomeRetry
	OutcomeFail
	OutcomeGone
)

// ClassifyDelivery maps status codes and transport errors to success, retry, terminal failure, or 410 Gone.
func ClassifyDelivery(statusCode int, err error) DeliveryOutcome {
	if err != nil {
		return OutcomeRetry
	}
	if statusCode >= 200 && statusCode < 300 {
		return OutcomeSuccess
	}
	if statusCode == http.StatusGone {
		return OutcomeGone
	}
	if statusCode == http.StatusTooManyRequests {
		return OutcomeRetry
	}
	if statusCode == http.StatusBadGateway || statusCode == http.StatusGatewayTimeout {
		return OutcomeRetry
	}
	if statusCode >= 500 {
		return OutcomeRetry
	}
	if statusCode >= 300 && statusCode < 400 {
		return OutcomeFail
	}
	return OutcomeFail
}
