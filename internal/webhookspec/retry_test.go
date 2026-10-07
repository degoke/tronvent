package webhookspec_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/degoke/tronvent/internal/webhookspec"
)

func TestClassifyDelivery(t *testing.T) {
	if webhookspec.ClassifyDelivery(200, nil) != webhookspec.OutcomeSuccess {
		t.Fatal("2xx success")
	}
	if webhookspec.ClassifyDelivery(410, nil) != webhookspec.OutcomeGone {
		t.Fatal("410 gone")
	}
	if webhookspec.ClassifyDelivery(404, nil) != webhookspec.OutcomeFail {
		t.Fatal("expected 404 to fail without retry")
	}
	if webhookspec.ClassifyDelivery(302, nil) != webhookspec.OutcomeFail {
		t.Fatal("3xx fail")
	}
	if webhookspec.ClassifyDelivery(429, nil) != webhookspec.OutcomeRetry {
		t.Fatal("429 retry")
	}
	if webhookspec.ClassifyDelivery(0, testNetErr{}) != webhookspec.OutcomeRetry {
		t.Fatal("network retry")
	}
}

type testNetErr struct{}

func (testNetErr) Error() string { return "network" }

func TestNextAttemptTimeUsesSpecOffsets(t *testing.T) {
	start := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	next := webhookspec.NextAttemptTime(start, 1, nil)
	if !next.Equal(start.Add(5*time.Second)) && next.Sub(start) < 5*time.Second {
		t.Fatalf("expected ~5s after first failure, got %v", next.Sub(start))
	}
}

func TestParseRetryAfter(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "120")
	start := time.Now()
	next := webhookspec.NextAttemptTime(start, 1, &http.Response{StatusCode: 503, Header: h})
	if next.Before(start.Add(119 * time.Second)) {
		t.Fatal("expected retry-after to push schedule")
	}
}
