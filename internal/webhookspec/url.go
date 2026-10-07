package webhookspec

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

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
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("webhook URL host lookup failed: %w", err)
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
