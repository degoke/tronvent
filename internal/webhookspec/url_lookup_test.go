package webhookspec

import (
	"errors"
	"net"
	"testing"
)

func TestValidateWebhookURLHostLookupFailures(t *testing.T) {
	orig := lookupIP
	defer func() { lookupIP = orig }()

	lookupIP = func(string) ([]net.IP, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "hooks.example.com", Server: "10.0.0.2:53", IsNotFound: true}
	}
	err := URLPolicy{}.ValidateWebhookURL("https://hooks.example.com/hook")
	if err == nil || errors.Is(err, ErrHostLookup) {
		t.Fatalf("NXDOMAIN must be a plain validation error, got %v", err)
	}

	lookupIP = func(string) ([]net.IP, error) {
		return nil, &net.DNSError{Err: "i/o timeout", Name: "hooks.example.com", Server: "10.0.0.2:53", IsTimeout: true}
	}
	err = URLPolicy{}.ValidateWebhookURL("https://hooks.example.com/hook")
	if !errors.Is(err, ErrHostLookup) {
		t.Fatalf("timeout must wrap ErrHostLookup, got %v", err)
	}
	if got := err.Error(); got != `webhook URL host lookup failed for "hooks.example.com"` {
		t.Fatalf("unexpected message (must not leak resolver): %q", got)
	}
}
