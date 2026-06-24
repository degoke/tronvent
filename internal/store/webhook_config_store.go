package store

import (
	"context"
	"sync"

	internaldb "github.com/degoke/tronvent/internal/db"
)

// WebhookLoader loads webhook config from Postgres.
type WebhookLoader interface {
	GetWebhookConfig(ctx context.Context) (*internaldb.WebhookConfig, error)
}

// WebhookConfigStore holds the active webhook config in memory.
type WebhookConfigStore struct {
	mu   sync.RWMutex
	cfg  *internaldb.WebhookConfig
	load WebhookLoader
}

// NewWebhookConfigStore creates an empty WebhookConfigStore.
func NewWebhookConfigStore(loader WebhookLoader) *WebhookConfigStore {
	return &WebhookConfigStore{load: loader}
}

// Reload replaces the in-memory webhook config from Postgres.
func (s *WebhookConfigStore) Reload(ctx context.Context) error {
	cfg, err := s.load.GetWebhookConfig(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
	return nil
}

// Set updates the in-memory config after a successful DB write.
func (s *WebhookConfigStore) Set(cfg *internaldb.WebhookConfig) {
	s.mu.Lock()
	s.cfg = cfg
	s.mu.Unlock()
}

// Get returns a copy of the current webhook config, or nil if unset.
func (s *WebhookConfigStore) Get() *internaldb.WebhookConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil {
		return nil
	}
	cp := *s.cfg
	return &cp
}

// PublicView returns webhook config without the signing secret.
func (s *WebhookConfigStore) PublicView() (webhookURL string, isActive bool, updatedAt string, ok bool) {
	cfg := s.Get()
	if cfg == nil {
		return "", false, "", false
	}
	return cfg.WebhookURL, cfg.IsActive, cfg.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"), true
}
