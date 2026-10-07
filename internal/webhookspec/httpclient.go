package webhookspec

import (
	"context"
	"net"
	"net/http"
	"time"
)

// NewDeliveryHTTPClient builds an HTTP client for webhook delivery: no redirects, SSRF-safe dial, timeout.
func NewDeliveryHTTPClient(timeout time.Duration, policy URLPolicy) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if !policy.AllowPrivateHosts {
				host, _, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				if err := rejectPrivateHost(host); err != nil {
					return nil, err
				}
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// IsRedirectResponse reports whether the status is a redirect (treated as delivery failure).
func IsRedirectResponse(statusCode int) bool {
	return statusCode >= 300 && statusCode < 400
}
