package store

import (
	"context"
	"log/slog"
	"sync"
)

// ContractLoader loads active watched TRC-20 contracts from Postgres.
type ContractLoader interface {
	ListActiveContracts(ctx context.Context) ([]string, error)
}

// ContractStore holds the in-memory contract list for polling.
type ContractStore struct {
	mu        sync.RWMutex
	contracts []string
	loader    ContractLoader
}

// NewContractStore creates an empty ContractStore.
func NewContractStore(loader ContractLoader) *ContractStore {
	return &ContractStore{loader: loader}
}

// Reload replaces the in-memory contract list from Postgres.
func (s *ContractStore) Reload(ctx context.Context) error {
	contracts, err := s.loader.ListActiveContracts(ctx)
	if err != nil {
		return err
	}
	cp := append([]string(nil), contracts...)
	s.mu.Lock()
	s.contracts = cp
	s.mu.Unlock()
	slog.Info("contract store reloaded", "count", len(cp))
	return nil
}

// Add inserts a contract into the live list after a successful DB write.
func (s *ContractStore) Add(contract string) {
	s.mu.Lock()
	for _, c := range s.contracts {
		if c == contract {
			s.mu.Unlock()
			return
		}
	}
	s.contracts = append(s.contracts, contract)
	s.mu.Unlock()
}

// List returns a snapshot of active contracts.
func (s *ContractStore) List() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.contracts...)
}

// Len returns the number of watched contracts.
func (s *ContractStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.contracts)
}
