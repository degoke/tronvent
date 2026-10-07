package scanner

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const (
	addressConfirmMaxAttempts = 3
	addressConfirmRetryBase   = 50 * time.Millisecond
)

// addressConfirmDB loads active watchlist membership from Postgres.
type addressConfirmDB interface {
	IsWatchedAddressActive(ctx context.Context, address string) (bool, error)
	ActiveWatchedAddresses(ctx context.Context, addresses []string) (map[string]bool, error)
}

// AddressConfirm caches Postgres confirmations for a scan batch and applies retry / fail-open policy.
type AddressConfirm struct {
	db    addressConfirmDB
	cache map[string]bool
}

// NewAddressConfirm creates an empty per-batch confirmation cache.
func NewAddressConfirm(db addressConfirmDB) *AddressConfirm {
	return &AddressConfirm{
		db:    db,
		cache: make(map[string]bool),
	}
}

// Prefetch loads active status for addresses not yet cached, using one batch query when possible.
func (c *AddressConfirm) Prefetch(ctx context.Context, addresses []string) {
	need := uniqueStringsNotCached(addresses, c.cache)
	if len(need) == 0 {
		return
	}
	active, err := c.loadActiveWithRetry(ctx, need)
	if err == nil {
		for _, addr := range need {
			c.cache[addr] = active[addr]
		}
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		c.failOpenAddresses(need)
		return
	}
	slog.Warn("batch confirm watched addresses failed, falling back to per-address lookup", "count", len(need), "err", err)
	for _, addr := range need {
		c.populateCache(ctx, addr)
	}
}

// IsActive reports whether address is actively watched. Uses cache, then single-row lookup with retry.
// On persistent errors, fails open (treats as active) so webhooks are not dropped.
func (c *AddressConfirm) IsActive(ctx context.Context, address string) bool {
	if active, ok := c.cache[address]; ok {
		return active
	}
	c.populateCache(ctx, address)
	active, ok := c.cache[address]
	if !ok {
		return false
	}
	return active
}

func (c *AddressConfirm) populateCache(ctx context.Context, address string) {
	if address == "" {
		return
	}
	if _, ok := c.cache[address]; ok {
		return
	}
	active, err := c.confirmOneWithRetry(ctx, address)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			c.cache[address] = true
			return
		}
		slog.Warn("confirm watched address failed, fail-open", "address", address, "err", err)
		c.cache[address] = true
		return
	}
	c.cache[address] = active
}

func (c *AddressConfirm) loadActiveWithRetry(ctx context.Context, addresses []string) (map[string]bool, error) {
	var lastErr error
	for attempt := 0; attempt < addressConfirmMaxAttempts; attempt++ {
		active, err := c.db.ActiveWatchedAddresses(ctx, addresses)
		if err == nil {
			return active, nil
		}
		lastErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		if attempt+1 < addressConfirmMaxAttempts {
			if err := sleepBetweenRetries(ctx, addressConfirmRetryBase*time.Duration(attempt+1)); err != nil {
				return nil, err
			}
		}
	}
	return nil, lastErr
}

func (c *AddressConfirm) confirmOneWithRetry(ctx context.Context, address string) (bool, error) {
	var lastErr error
	for attempt := 0; attempt < addressConfirmMaxAttempts; attempt++ {
		active, err := c.db.IsWatchedAddressActive(ctx, address)
		if err == nil {
			return active, nil
		}
		lastErr = err
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false, err
		}
		if attempt+1 < addressConfirmMaxAttempts {
			if err := sleepBetweenRetries(ctx, addressConfirmRetryBase*time.Duration(attempt+1)); err != nil {
				return false, err
			}
		}
	}
	return false, lastErr
}

func (c *AddressConfirm) failOpenAddresses(addresses []string) {
	for _, addr := range addresses {
		c.cache[addr] = true
	}
}

func sleepBetweenRetries(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// bloomConfirmCandidates returns unique from/to addresses that passed the Bloom prefilter.
func bloomConfirmCandidates(addresses AddressSet, fromAddr, toAddr string) []string {
	var out []string
	if addresses.Contains(toAddr) {
		out = append(out, toAddr)
	}
	if addresses.Contains(fromAddr) && fromAddr != toAddr {
		out = append(out, fromAddr)
	}
	return out
}

func uniqueStringsNotCached(addrs []string, cache map[string]bool) []string {
	seen := make(map[string]struct{}, len(addrs))
	var out []string
	for _, a := range addrs {
		if a == "" {
			continue
		}
		if cache != nil {
			if _, ok := cache[a]; ok {
				continue
			}
		}
		if _, dup := seen[a]; dup {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	return out
}
