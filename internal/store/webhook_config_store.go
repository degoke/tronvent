package store

import (
	"context"
	"sync"

	internaldb "github.com/degoke/tronvent/internal/db"
)

// WebhookLoader loads webhook endpoints from Postgres.
type WebhookLoader interface {
	ListWebhookEndpoints(ctx context.Context) ([]internaldb.WebhookEndpoint, error)
}

// WebhookConfigStore holds webhook endpoints in memory.
type WebhookConfigStore struct {
	mu      sync.RWMutex
	byID    map[string]internaldb.WebhookEndpoint
	ordered []internaldb.WebhookEndpoint
	load    WebhookLoader
}

// NewWebhookConfigStore creates an empty WebhookConfigStore.
func NewWebhookConfigStore(loader WebhookLoader) *WebhookConfigStore {
	return &WebhookConfigStore{load: loader, byID: map[string]internaldb.WebhookEndpoint{}}
}

// Reload replaces in-memory webhook endpoints from Postgres.
func (s *WebhookConfigStore) Reload(ctx context.Context) error {
	eps, err := s.load.ListWebhookEndpoints(ctx)
	if err != nil {
		return err
	}
	byID := make(map[string]internaldb.WebhookEndpoint, len(eps))
	for _, ep := range eps {
		byID[ep.ID] = ep
	}
	s.mu.Lock()
	s.byID = byID
	s.ordered = eps
	s.mu.Unlock()
	return nil
}

// UpsertEndpoint updates memory for one endpoint.
func (s *WebhookConfigStore) UpsertEndpoint(ep internaldb.WebhookEndpoint) {
	s.mu.Lock()
	s.byID[ep.ID] = ep
	found := false
	for i, cur := range s.ordered {
		if cur.ID == ep.ID {
			s.ordered[i] = ep
			found = true
			break
		}
	}
	if !found {
		s.ordered = append(s.ordered, ep)
	}
	s.mu.Unlock()
}

// GetPrimaryEndpoint returns the oldest configured endpoint (first in list order), or nil.
func (s *WebhookConfigStore) GetPrimaryEndpoint() *internaldb.WebhookEndpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.ordered) == 0 {
		return nil
	}
	ep := s.ordered[0]
	return &ep
}

// GetEndpoint returns a configured endpoint by id.
func (s *WebhookConfigStore) GetEndpoint(id string) *internaldb.WebhookEndpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ep, ok := s.byID[id]
	if !ok {
		return nil
	}
	cp := ep
	return &cp
}

// RemoveEndpoint drops an endpoint from memory after deletion.
func (s *WebhookConfigStore) RemoveEndpoint(id string) {
	s.mu.Lock()
	delete(s.byID, id)
	filtered := make([]internaldb.WebhookEndpoint, 0, len(s.ordered))
	for _, ep := range s.ordered {
		if ep.ID != id {
			filtered = append(filtered, ep)
		}
	}
	s.ordered = filtered
	s.mu.Unlock()
}

// PublicView returns the primary endpoint without the signing secret.
func (s *WebhookConfigStore) PublicView() (webhookURL string, isActive bool, updatedAt string, ok bool) {
	ep := s.GetPrimaryEndpoint()
	if ep == nil {
		return "", false, "", false
	}
	return ep.WebhookURL, ep.IsActive, ep.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"), true
}
