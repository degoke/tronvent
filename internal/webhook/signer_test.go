package webhook_test

import (
	"testing"
	"time"

	"github.com/degoke/tronvent/internal/webhook"
)

func TestSign(t *testing.T) {
	body := []byte(`{"id":"abc","type":"TRX"}`)
	sig := webhook.Sign("secret", 1710000000, body)
	if sig == "" || sig[:7] != "sha256=" {
		t.Fatalf("unexpected signature format: %q", sig)
	}
	if webhook.Sign("secret", 1710000000, body) != sig {
		t.Fatal("signature should be deterministic")
	}
	if webhook.Sign("other", 1710000000, body) == sig {
		t.Fatal("signature should change with secret")
	}
}

func TestShouldRetry(t *testing.T) {
	if !webhook.ShouldRetry(500, nil) {
		t.Fatal("expected retry on 500")
	}
	if !webhook.ShouldRetry(429, nil) {
		t.Fatal("expected retry on 429")
	}
	if webhook.ShouldRetry(404, nil) {
		t.Fatal("did not expect retry on 404")
	}
	if !webhook.ShouldRetry(0, contextCanceled()) {
		t.Fatal("expected retry on network error")
	}
}

func TestRetrySchedule(t *testing.T) {
	if webhook.RetrySchedule(1) != time.Minute {
		t.Fatalf("attempt 1 = %v", webhook.RetrySchedule(1))
	}
	if webhook.RetrySchedule(99) != 2*time.Hour {
		t.Fatalf("attempt 99 = %v", webhook.RetrySchedule(99))
	}
}

type errCanceled struct{}

func (errCanceled) Error() string { return "canceled" }

func contextCanceled() error { return errCanceled{} }
