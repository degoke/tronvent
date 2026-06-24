package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/degoke/tronvent/internal/config"
	internaldb "github.com/degoke/tronvent/internal/db"
	"github.com/degoke/tronvent/internal/store"
	"github.com/degoke/tronvent/internal/validate"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// DB is the persistence surface used by admin handlers.
type DB interface {
	AddWatchedAddress(ctx context.Context, address, source string) (internaldb.WatchedAddress, bool, error)
	DeactivateWatchedAddress(ctx context.Context, address string) (internaldb.WatchedAddress, error)
	ListAddresses(ctx context.Context, status string, limit int, afterAddress string) ([]internaldb.WatchedAddress, error)
	AddWatchedContract(ctx context.Context, contractAddress, tokenSymbol, source string) (internaldb.WatchedContract, bool, error)
	DeactivateWatchedContract(ctx context.Context, contractAddress string) (internaldb.WatchedContract, error)
	ListContracts(ctx context.Context, status string, limit int, afterContract string) ([]internaldb.WatchedContract, error)
	UpsertWebhookConfig(ctx context.Context, webhookURL, signingSecret string, isActive bool, source string) (*internaldb.WebhookConfig, error)
	ListCursors(ctx context.Context) ([]internaldb.CursorRow, error)
	EnqueueRetryJob(ctx context.Context, fromBlock, toBlock int64) (internaldb.EnqueueRetryResult, error)
	ListRetryJobs(ctx context.Context, status string, limit int) ([]internaldb.RetryJobRecord, error)
}

// Server exposes health, metrics, and admin API endpoints.
type Server struct {
	cfg           *config.Config
	db            DB
	addresses     *store.AddressStore
	contracts     *store.ContractStore
	webhookConfig *store.WebhookConfigStore
	srv           *http.Server
}

// New creates an API server on the given port.
func New(
	cfg *config.Config,
	db DB,
	addresses *store.AddressStore,
	contracts *store.ContractStore,
	webhookConfig *store.WebhookConfigStore,
) *Server {
	s := &Server{
		cfg:           cfg,
		db:            db,
		addresses:     addresses,
		contracts:     contracts,
		webhookConfig: webhookConfig,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("POST /api/v1/addresses", s.requireAuth(s.handlePostAddress))
	mux.HandleFunc("GET /api/v1/addresses", s.requireAuth(s.handleGetAddresses))
	mux.HandleFunc("DELETE /api/v1/addresses/{address}", s.requireAuth(s.handleDeleteAddress))
	mux.HandleFunc("POST /api/v1/contracts", s.requireAuth(s.handlePostContract))
	mux.HandleFunc("GET /api/v1/contracts", s.requireAuth(s.handleGetContracts))
	mux.HandleFunc("DELETE /api/v1/contracts/{contractAddress}", s.requireAuth(s.handleDeleteContract))
	mux.HandleFunc("PUT /api/v1/webhook", s.requireAuth(s.handlePutWebhook))
	mux.HandleFunc("GET /api/v1/webhook", s.requireAuth(s.handleGetWebhook))
	mux.HandleFunc("GET /api/v1/runtime", s.requireAuth(s.handleGetRuntime))
	mux.HandleFunc("POST /api/v1/retries/block", s.requireAuth(s.handlePostRetryBlock))
	mux.HandleFunc("POST /api/v1/retries/range", s.requireAuth(s.handlePostRetryRange))
	mux.HandleFunc("GET /api/v1/retries", s.requireAuth(s.handleGetRetries))

	s.srv = &http.Server{
		Addr:         net.JoinHostPort("", cfg.HealthPort),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	return s
}

// Start begins listening in a background goroutine.
func (s *Server) Start() {
	go func() {
		slog.Info("api server listening", "addr", s.srv.Addr)
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("api server error", "err", err)
		}
	}()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("api server shutdown: %w", err)
	}
	return nil
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.AdminAPIToken == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "admin API not configured"})
			return
		}
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) || strings.TrimPrefix(auth, prefix) != s.cfg.AdminAPIToken {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (s *Server) handlePostAddress(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address string `json:"address"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	req.Address = strings.TrimSpace(req.Address)
	if !validate.TronAddress(req.Address) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Tron address"})
		return
	}

	row, created, err := s.db.AddWatchedAddress(r.Context(), req.Address, "api")
	if err != nil {
		slog.Error("add watched address", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save address"})
		return
	}
	s.addresses.Add(req.Address)

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{
		"id":        row.ID,
		"address":   row.Address,
		"status":    row.Status,
		"createdAt": row.CreatedAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleGetAddresses(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit := queryInt(r, "limit", 50)
	cursor := r.URL.Query().Get("cursor")

	rows, err := s.db.ListAddresses(r.Context(), status, limit, cursor)
	if err != nil {
		slog.Error("list addresses", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list addresses"})
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"id":        row.ID,
			"address":   row.Address,
			"status":    row.Status,
			"createdAt": row.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) handlePostContract(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ContractAddress string `json:"contractAddress"`
		TokenSymbol     string `json:"tokenSymbol"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	req.ContractAddress = strings.TrimSpace(req.ContractAddress)
	if !validate.TronAddress(req.ContractAddress) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid Tron contract address"})
		return
	}

	row, created, err := s.db.AddWatchedContract(r.Context(), req.ContractAddress, strings.TrimSpace(req.TokenSymbol), "api")
	if err != nil {
		slog.Error("add watched contract", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save contract"})
		return
	}
	s.contracts.Add(req.ContractAddress)

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	resp := map[string]any{
		"id":              row.ID,
		"contractAddress": row.ContractAddress,
		"status":          row.Status,
		"createdAt":       row.CreatedAt.UTC().Format(time.RFC3339),
	}
	if row.TokenSymbol != nil {
		resp["tokenSymbol"] = *row.TokenSymbol
	}
	writeJSON(w, status, resp)
}

func (s *Server) handleGetContracts(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit := queryInt(r, "limit", 50)
	cursor := r.URL.Query().Get("cursor")

	rows, err := s.db.ListContracts(r.Context(), status, limit, cursor)
	if err != nil {
		slog.Error("list contracts", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list contracts"})
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		item := map[string]any{
			"id":              row.ID,
			"contractAddress": row.ContractAddress,
			"status":          row.Status,
			"createdAt":       row.CreatedAt.UTC().Format(time.RFC3339),
		}
		if row.TokenSymbol != nil {
			item["tokenSymbol"] = *row.TokenSymbol
		}
		out = append(out, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (s *Server) handlePutWebhook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WebhookURL    string `json:"webhookUrl"`
		SigningSecret string `json:"signingSecret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	req.WebhookURL = strings.TrimSpace(req.WebhookURL)
	req.SigningSecret = strings.TrimSpace(req.SigningSecret)
	if req.WebhookURL == "" || req.SigningSecret == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "webhookUrl and signingSecret are required"})
		return
	}

	cfg, err := s.db.UpsertWebhookConfig(r.Context(), req.WebhookURL, req.SigningSecret, true, "api")
	if err != nil {
		slog.Error("upsert webhook config", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to save webhook config"})
		return
	}
	s.webhookConfig.Set(cfg)
	writeJSON(w, http.StatusOK, map[string]any{
		"webhookUrl": cfg.WebhookURL,
		"isActive":   cfg.IsActive,
		"updatedAt":  cfg.UpdatedAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleGetWebhook(w http.ResponseWriter, r *http.Request) {
	url, active, updatedAt, ok := s.webhookConfig.PublicView()
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "webhook not configured"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"webhookUrl": url,
		"isActive":   active,
		"updatedAt":  updatedAt,
	})
}

func (s *Server) handleGetRuntime(w http.ResponseWriter, r *http.Request) {
	cursors, err := s.db.ListCursors(r.Context())
	if err != nil {
		slog.Error("list cursors", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load runtime state"})
		return
	}
	cursorOut := make([]map[string]any, 0, len(cursors))
	for _, c := range cursors {
		cursorOut = append(cursorOut, map[string]any{
			"scope":        c.Scope,
			"highestBlock": c.HighestBlock,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"tronGridBaseUrl":      s.cfg.TronGridBaseURL,
		"watchedAddressCount":  s.addresses.Len(),
		"watchedContractCount": s.contracts.Len(),
		"contracts":            s.contracts.List(),
		"cursors":              cursorOut,
	})
}

// Handler returns the HTTP handler (for tests).
func (s *Server) Handler() http.Handler {
	return s.srv.Handler
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func queryInt(r *http.Request, key string, def int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	var v int
	if _, err := fmt.Sscanf(raw, "%d", &v); err != nil || v <= 0 {
		return def
	}
	return v
}
