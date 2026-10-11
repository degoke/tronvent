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

func TestIsRetryableURLError(t *testing.T) {
	notFound := func(string) ([]net.IP, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "hooks.example.com", IsNotFound: true}
	}
	timeout := func(string) ([]net.IP, error) {
		return nil, &net.DNSError{Err: "i/o timeout", Name: "hooks.example.com", IsTimeout: true}
	}
	publicIP := func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("93.184.216.34")}, nil }

	cases := []struct {
		name      string
		policy    URLPolicy
		url       string
		retryable bool
	}{
		{"NXDOMAIN", URLPolicy{LookupIP: notFound}, "https://hooks.example.com/hook", true},
		{"resolver timeout", URLPolicy{LookupIP: timeout}, "https://hooks.example.com/hook", true},
		{"plain http", URLPolicy{LookupIP: publicIP}, "http://hooks.example.com/hook", false},
		{"localhost", URLPolicy{LookupIP: publicIP}, "https://localhost/hook", false},
		{"private address", URLPolicy{LookupIP: publicIP}, "https://10.0.0.5/hook", false},
		{"resolves to a private address", URLPolicy{LookupIP: func(string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("192.168.1.10")}, nil
		}}, "https://hooks.example.com/hook", false},
		{"not absolute", URLPolicy{LookupIP: publicIP}, "/hook", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.policy.ValidateWebhookURL(tc.url)
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if got := IsRetryableURLError(err); got != tc.retryable {
				t.Fatalf("IsRetryableURLError(%q) = %v, want %v", err, got, tc.retryable)
			}
		})
	}

	if IsRetryableURLError(nil) {
		t.Fatal("nil is not a retryable error")
	}
}

func TestHostNotFoundKeepsItsMessageAndIdentity(t *testing.T) {
	policy := URLPolicy{LookupIP: func(string) ([]net.IP, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "hooks.example.com", IsNotFound: true}
	}}
	err := policy.ValidateWebhookURL("https://hooks.example.com/hook")

	if !errors.Is(err, ErrHostNotFound) {
		t.Fatalf("NXDOMAIN must match ErrHostNotFound, got %v", err)
	}
	if errors.Is(err, ErrHostLookup) {
		t.Fatal("NXDOMAIN must stay distinct from a resolver failure")
	}
	if got, want := err.Error(), `webhook URL host "hooks.example.com" does not resolve`; got != want {
		t.Fatalf("message changed: %q", got)
	}
}
