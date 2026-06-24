package store

import (
	"context"
	"log/slog"
	"sync"

	"github.com/degoke/tronvent/internal/metrics"
	"github.com/degoke/tronvent/internal/scanner"
)

// AddressLoader loads active watched addresses from Postgres.
type AddressLoader interface {
	ListActiveAddresses(ctx context.Context) ([]string, error)
}

// AddressStore wraps a BloomFilter with full-reload sync from Postgres.
type AddressStore struct {
	mu     sync.RWMutex
	filter scanner.AddressSet
	loader AddressLoader
}

// NewAddressStore creates an empty AddressStore.
func NewAddressStore(loader AddressLoader) *AddressStore {
	return &AddressStore{
		filter: scanner.NewBloomFilter(),
		loader: loader,
	}
}

// Reload replaces the in-memory filter from Postgres.
func (s *AddressStore) Reload(ctx context.Context) error {
	addrs, err := s.loader.ListActiveAddresses(ctx)
	if err != nil {
		return err
	}
	bf := scanner.NewBloomFilter()
	bf.AddBatch(addrs)
	s.mu.Lock()
	s.filter = bf
	s.mu.Unlock()
	metrics.WatchlistSize.Set(float64(bf.Len()))
	slog.Info("address store reloaded", "count", bf.Len())
	return nil
}

// Add inserts one address into the live filter after a successful DB write.
func (s *AddressStore) Add(address string) {
	s.mu.Lock()
	s.filter.Add(address)
	s.mu.Unlock()
	metrics.WatchlistSize.Set(float64(s.Len()))
}

// Contains reports whether address is probably watched.
func (s *AddressStore) Contains(address string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.filter.Contains(address)
}

// Len returns the tracked address count.
func (s *AddressStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.filter.Len()
}
