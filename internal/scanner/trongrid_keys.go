package scanner

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type tronGridAPIKey struct {
	value         string
	cooldownUntil time.Time
}

type tronGridAPIKeyPool struct {
	mu   sync.Mutex
	keys []tronGridAPIKey
	next int
}

func newTronGridAPIKeyPool(raw string) *tronGridAPIKeyPool {
	seen := make(map[string]struct{})
	keys := make([]tronGridAPIKey, 0)
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		keys = append(keys, tronGridAPIKey{value: value})
	}
	return &tronGridAPIKeyPool{keys: keys}
}

func (p *tronGridAPIKeyPool) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.keys)
}

// nextAvailable returns the next key in round-robin order. If every key is
// cooling down, it waits until the earliest key becomes available.
func (p *tronGridAPIKeyPool) nextAvailable(ctx context.Context) (int, string, error) {
	for {
		p.mu.Lock()
		if len(p.keys) == 0 {
			p.mu.Unlock()
			return -1, "", nil
		}

		now := time.Now()
		var earliest time.Time
		for offset := range p.keys {
			index := (p.next + offset) % len(p.keys)
			key := &p.keys[index]
			if !now.Before(key.cooldownUntil) {
				p.next = (index + 1) % len(p.keys)
				value := key.value
				p.mu.Unlock()
				return index, value, nil
			}
			if earliest.IsZero() || key.cooldownUntil.Before(earliest) {
				earliest = key.cooldownUntil
			}
		}
		p.mu.Unlock()

		timer := time.NewTimer(time.Until(earliest))
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return -1, "", ctx.Err()
		}
	}
}

func (p *tronGridAPIKeyPool) cooldown(index int, duration time.Duration) {
	if index < 0 || duration <= 0 {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if index >= len(p.keys) {
		return
	}

	until := time.Now().Add(duration)
	if until.After(p.keys[index].cooldownUntil) {
		p.keys[index].cooldownUntil = until
	}
}

func (p *tronGridAPIKeyPool) label(index int) string {
	if index < 0 {
		return "none"
	}
	return "key-" + strconv.Itoa(index+1)
}

func retryAfterDuration(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		if delay := retryAt.Sub(now); delay > 0 {
			return delay
		}
	}
	return 0
}
