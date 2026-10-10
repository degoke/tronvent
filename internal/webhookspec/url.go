package webhookspec

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

// ErrHostLookup marks a DNS failure that does not prove the URL is bad (timeout,
// resolver outage), so callers can retry instead of reporting a client error.
var ErrHostLookup = errors.New("webhook URL host lookup failed")

// lookupIP is net.LookupIP, replaced in tests.
var lookupIP = net.LookupIP

// URLPolicy validates subscriber webhook URLs before delivery or persistence.
type URLPolicy struct {
	AllowHTTP         bool
	AllowPrivateHosts bool // for unit tests only
}

// ValidateWebhookURL enforces HTTPS (unless AllowHTTP) and rejects obvious SSRF targets.
func (p URLPolicy) ValidateWebhookURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("webhook URL is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("webhook URL must be absolute")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && (scheme != "http" || !p.AllowHTTP) {
		return fmt.Errorf("webhook URL must use https")
	}
	host := strings.ToLower(u.Hostname())
	if !p.AllowPrivateHosts {
		if host == "localhost" || strings.HasSuffix(host, ".localhost") {
			return fmt.Errorf("webhook URL must not target localhost")
		}
		if err := rejectPrivateHost(host); err != nil {
			return err
		}
	}
	return nil
}

func rejectPrivateHost(host string) error {
	if ip, err := netip.ParseAddr(host); err == nil {
		return rejectPrivateAddr(ip)
	}
	ips, err := lookupIP(host)
	if err != nil {
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
			return fmt.Errorf("webhook URL host %q does not resolve", host)
		}
		// The raw error names the resolver (e.g. "lookup h on 10.0.0.2:53: ..."); keep it out of the message.
		return fmt.Errorf("%w for %q", ErrHostLookup, host)
	}
	if len(ips) == 0 {
		return fmt.Errorf("webhook URL host has no addresses")
	}
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip)
		if !ok {
			continue
		}
		if err := rejectPrivateAddr(addr); err != nil {
			return err
		}
	}
	return nil
}

func rejectPrivateAddr(ip netip.Addr) error {
	if !ip.IsValid() {
		return fmt.Errorf("invalid webhook URL address")
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return fmt.Errorf("webhook URL must not target private or link-local addresses")
	}
	// Block cloud metadata endpoints commonly abused in SSRF.
	if ip.Is4() && ip.As4()[0] == 169 && ip.As4()[1] == 254 {
		return fmt.Errorf("webhook URL must not target link-local metadata ranges")
	}
	return nil
}
