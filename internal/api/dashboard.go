package api

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	internaldb "github.com/degoke/tronvent/internal/db"
	"github.com/degoke/tronvent/internal/validate"
)

//go:embed templates/*.html
var templateFS embed.FS

type watchlistAddressItem struct {
	Address   string
	Status    string
	CreatedAt string
}

type watchlistContractItem struct {
	ContractAddress string
	TokenSymbol     string
	Status          string
	CreatedAt       string
}

type webhookEventItem struct {
	ID           string
	Scope        string
	TxHash       string
	Status       string
	AttemptCount int
}

type webhookAttemptItem struct {
	AttemptNumber int
	ResponseCode  string
	ErrorMessage  string
	DurationMs    string
	CreatedAt     string
}

type queueJobItem struct {
	Queue       string
	FromBlock   int64
	ToBlock     int64
	Status      string
	Attempts    int
	MaxAttempts int
	LastError   string
	CreatedAt   string
	UpdatedAt   string
	CompletedAt string
}

func watchlistRefreshURL(pane, status, search, cursor string) string {
	path := "/dashboard/partial/watchlist/addresses"
	if pane == "contracts" {
		path = "/dashboard/partial/watchlist/contracts"
	}
	q := url.Values{}
	if status != "" {
		q.Set("status", status)
	}
	if search != "" {
		q.Set("search", search)
	}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if encoded := q.Encode(); encoded != "" {
		return path + "?" + encoded
	}
	return path
}

func (s *Server) initTemplates() error {
	tmpl, err := template.New("dashboard").ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return err
	}
	s.templates = tmpl
	return nil
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.templates.ExecuteTemplate(w, name, data); err != nil {
		slog.Error("render template", "name", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
	}
}

func (s *Server) handleDashboardLoginGET(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdminAPIToken == "" {
		http.Error(w, "dashboard not configured", http.StatusServiceUnavailable)
		return
	}
	if s.hasValidSession(r) {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}
	s.render(w, "login", map[string]any{})
}

func (s *Server) handleDashboardLoginPOST(w http.ResponseWriter, r *http.Request) {
	if s.cfg.AdminAPIToken == "" {
		http.Error(w, "dashboard not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.render(w, "login", map[string]any{"Error": "invalid form"})
		return
	}
	token := strings.TrimSpace(r.FormValue("token"))
	if token != s.cfg.AdminAPIToken {
		s.render(w, "login", map[string]any{"Error": "invalid token"})
		return
	}
	s.setSessionCookie(w, r)
	s.ensureCSRFCookie(w, r)
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func (s *Server) handleDashboardLogout(w http.ResponseWriter, r *http.Request) {
	s.clearSessionCookie(w, r)
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookieName, Value: "", Path: "/dashboard", MaxAge: -1,
	})
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", "/dashboard/login")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/dashboard/login", http.StatusFound)
}

func (s *Server) handleDashboardShell(w http.ResponseWriter, r *http.Request) {
	csrf := s.ensureCSRFCookie(w, r)
	snap := s.buildRuntimeSnapshot(r.Context())
	var overview bytes.Buffer
	if err := s.templates.ExecuteTemplate(&overview, "overview", map[string]any{
		"Snapshot":       snap,
		"AddressCount":   s.addresses.Len(),
		"ContractCount":  s.contracts.Len(),
		"PollIntervalMs": s.cfg.PollIntervalMs,
	}); err != nil {
		slog.Error("render initial overview", "err", err)
	}
	s.render(w, "shell", map[string]any{
		"CSRFToken":      csrf,
		"InitialContent": template.HTML(overview.String()),
	})
}

func (s *Server) handleDashboardOverview(w http.ResponseWriter, r *http.Request) {
	snap := s.buildRuntimeSnapshot(r.Context())
	s.render(w, "overview", map[string]any{
		"Snapshot":       snap,
		"AddressCount":   s.addresses.Len(),
		"ContractCount":  s.contracts.Len(),
		"PollIntervalMs": s.cfg.PollIntervalMs,
	})
}

func (s *Server) handleDashboardWatchlistAddresses(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	cursor := r.URL.Query().Get("cursor")
	limit := 25

	rows, err := s.db.ListAddresses(r.Context(), status, limit+1, cursor, search)
	if err != nil {
		s.render(w, "watchlist_addresses", map[string]any{"Error": "failed to load addresses"})
		return
	}

	nextCursor := ""
	if len(rows) > limit {
		nextCursor = rows[limit-1].Address
		rows = rows[:limit]
	}

	items := make([]watchlistAddressItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, watchlistAddressItem{
			Address:   row.Address,
			Status:    row.Status,
			CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	s.render(w, "watchlist_addresses", map[string]any{
		"Items":      items,
		"Status":     status,
		"Search":     search,
		"NextCursor": nextCursor,
		"RefreshURL": watchlistRefreshURL("addresses", status, search, cursor),
		"Flash":      r.URL.Query().Get("flash"),
		"Error":      r.URL.Query().Get("error"),
	})
}

func (s *Server) handleDashboardWatchlistContracts(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	cursor := r.URL.Query().Get("cursor")
	limit := 25

	rows, err := s.db.ListContracts(r.Context(), status, limit+1, cursor, search)
	if err != nil {
		s.render(w, "watchlist_contracts", map[string]any{"Error": "failed to load contracts"})
		return
	}

	nextCursor := ""
	if len(rows) > limit {
		nextCursor = rows[limit-1].ContractAddress
		rows = rows[:limit]
	}

	items := make([]watchlistContractItem, 0, len(rows))
	for _, row := range rows {
		sym := ""
		if row.TokenSymbol != nil {
			sym = *row.TokenSymbol
		}
		items = append(items, watchlistContractItem{
			ContractAddress: row.ContractAddress,
			TokenSymbol:     sym,
			Status:          row.Status,
			CreatedAt:       row.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	s.render(w, "watchlist_contracts", map[string]any{
		"Items":      items,
		"Status":     status,
		"Search":     search,
		"NextCursor": nextCursor,
		"RefreshURL": watchlistRefreshURL("contracts", status, search, cursor),
		"Flash":      r.URL.Query().Get("flash"),
		"Error":      r.URL.Query().Get("error"),
	})
}

func (s *Server) handleDashboardWebhooks(w http.ResponseWriter, r *http.Request) {
	eventStatus := r.URL.Query().Get("status")
	selectedEvent := r.URL.Query().Get("event")

	url, active, updatedAt, ok := s.webhookConfig.PublicView()
	webhookURL := ""
	webhookUpdated := ""
	if ok {
		webhookURL = url
		webhookUpdated = updatedAt
	}

	events, err := s.db.ListWebhookEvents(r.Context(), eventStatus, 50)
	if err != nil {
		s.render(w, "webhooks", map[string]any{"Error": "failed to load webhook events"})
		return
	}

	eventItems := make([]webhookEventItem, 0, len(events))
	for _, ev := range events {
		eventItems = append(eventItems, webhookEventItem{
			ID: ev.ID, Scope: ev.Scope, TxHash: ev.TxHash,
			Status: ev.Status, AttemptCount: ev.AttemptCount,
		})
	}

	s.render(w, "webhooks", map[string]any{
		"WebhookURL":       webhookURL,
		"WebhookActive":    active,
		"WebhookUpdatedAt": webhookUpdated,
		"Events":           eventItems,
		"EventStatus":      eventStatus,
		"SelectedEventID":  selectedEvent,
		"Flash":            r.URL.Query().Get("flash"),
		"Error":            r.URL.Query().Get("error"),
	})
}

func (s *Server) handleDashboardWebhookAttempts(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventID")
	attempts, err := s.db.ListWebhookDeliveryAttempts(r.Context(), eventID)
	if err != nil {
		s.render(w, "webhook_attempts", map[string]any{})
		return
	}

	items := make([]webhookAttemptItem, 0, len(attempts))
	for _, att := range attempts {
		item := webhookAttemptItem{
			AttemptNumber: att.AttemptNumber,
			CreatedAt:     att.CreatedAt.UTC().Format(time.RFC3339),
		}
		if att.ResponseCode != nil {
			item.ResponseCode = strconv.Itoa(*att.ResponseCode)
		}
		if att.ErrorMessage != nil {
			item.ErrorMessage = *att.ErrorMessage
		}
		if att.DurationMs != nil {
			item.DurationMs = strconv.Itoa(*att.DurationMs)
		}
		items = append(items, item)
	}
	s.render(w, "webhook_attempts", map[string]any{"Attempts": items})
}

func (s *Server) handleDashboardRetries(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")
	jobs, err := s.db.ListQueueJobs(r.Context(), nil, statusFilter, 50)
	if err != nil {
		s.render(w, "retries", map[string]any{"Error": "failed to load retry jobs"})
		return
	}

	items := make([]queueJobItem, 0, len(jobs))
	for _, job := range jobs {
		item := queueJobItem{
			Queue: job.Queue, FromBlock: job.FromBlock, ToBlock: job.ToBlock,
			Status: job.Status, Attempts: job.Attempts, MaxAttempts: job.MaxAttempts,
			CreatedAt: job.CreatedAt.UTC().Format(time.RFC3339),
			UpdatedAt: job.UpdatedAt.UTC().Format(time.RFC3339),
		}
		if job.LastError != nil {
			item.LastError = *job.LastError
		}
		if job.CompletedAt != nil {
			item.CompletedAt = job.CompletedAt.UTC().Format(time.RFC3339)
		}
		items = append(items, item)
	}

	s.render(w, "retries", map[string]any{
		"Jobs":         items,
		"StatusFilter": statusFilter,
		"Flash":        r.URL.Query().Get("flash"),
		"Error":        r.URL.Query().Get("error"),
	})
}

func (s *Server) handleDashboardAddAddress(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.handleDashboardWatchlistAddresses(w, r)
		return
	}
	address := strings.TrimSpace(r.FormValue("address"))
	if !validate.TronAddress(address) {
		s.handleDashboardWatchlistAddresses(w, r)
		return
	}
	row, _, err := s.db.AddWatchedAddress(r.Context(), address, "dashboard")
	if err != nil {
		s.handleDashboardWatchlistAddresses(w, r)
		return
	}
	s.addresses.Add(row.Address)
	s.handleDashboardWatchlistAddresses(w, r)
}

func (s *Server) handleDashboardDeactivateAddress(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.handleDashboardWatchlistAddresses(w, r)
		return
	}
	address := strings.TrimSpace(r.FormValue("address"))
	if _, err := s.db.DeactivateWatchedAddress(r.Context(), address); err != nil {
		s.handleDashboardWatchlistAddresses(w, r)
		return
	}
	_ = s.addresses.Reload(r.Context())
	s.handleDashboardWatchlistAddresses(w, r)
}

func (s *Server) handleDashboardReactivateAddress(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.handleDashboardWatchlistAddresses(w, r)
		return
	}
	address := strings.TrimSpace(r.FormValue("address"))
	row, _, err := s.db.AddWatchedAddress(r.Context(), address, "dashboard")
	if err != nil {
		s.handleDashboardWatchlistAddresses(w, r)
		return
	}
	s.addresses.Add(row.Address)
	s.handleDashboardWatchlistAddresses(w, r)
}

func (s *Server) handleDashboardAddContract(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.handleDashboardWatchlistContracts(w, r)
		return
	}
	contract := strings.TrimSpace(r.FormValue("contractAddress"))
	symbol := strings.TrimSpace(r.FormValue("tokenSymbol"))
	if !validate.TronAddress(contract) {
		s.handleDashboardWatchlistContracts(w, r)
		return
	}
	row, _, err := s.db.AddWatchedContract(r.Context(), contract, symbol, "dashboard")
	if err != nil {
		s.handleDashboardWatchlistContracts(w, r)
		return
	}
	s.contracts.Add(row.ContractAddress)
	s.handleDashboardWatchlistContracts(w, r)
}

func (s *Server) handleDashboardDeactivateContract(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.handleDashboardWatchlistContracts(w, r)
		return
	}
	contract := strings.TrimSpace(r.FormValue("contractAddress"))
	if _, err := s.db.DeactivateWatchedContract(r.Context(), contract); err != nil {
		s.handleDashboardWatchlistContracts(w, r)
		return
	}
	_ = s.contracts.Reload(r.Context())
	s.handleDashboardWatchlistContracts(w, r)
}

func (s *Server) handleDashboardReactivateContract(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.handleDashboardWatchlistContracts(w, r)
		return
	}
	contract := strings.TrimSpace(r.FormValue("contractAddress"))
	symbol := strings.TrimSpace(r.FormValue("tokenSymbol"))
	row, _, err := s.db.AddWatchedContract(r.Context(), contract, symbol, "dashboard")
	if err != nil {
		s.handleDashboardWatchlistContracts(w, r)
		return
	}
	s.contracts.Add(row.ContractAddress)
	s.handleDashboardWatchlistContracts(w, r)
}

func (s *Server) handleDashboardWebhookSettings(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.handleDashboardWebhooks(w, r)
		return
	}
	webhookURL := strings.TrimSpace(r.FormValue("webhookUrl"))
	signingSecret := strings.TrimSpace(r.FormValue("signingSecret"))
	isActive := r.FormValue("isActive") == "on"
	if webhookURL == "" {
		s.handleDashboardWebhooks(w, r)
		return
	}

	cfg, err := s.db.UpsertWebhookConfigPreserveSecret(r.Context(), webhookURL, signingSecret, isActive, "dashboard")
	if err != nil {
		s.handleDashboardWebhooks(w, r)
		return
	}
	s.webhookConfig.Set(cfg)
	s.handleDashboardWebhooks(w, r)
}

func (s *Server) handleDashboardRetryWebhookEvent(w http.ResponseWriter, r *http.Request) {
	eventID := r.PathValue("eventID")
	if err := s.db.RetryWebhookEvent(r.Context(), eventID); err != nil {
		if errors.Is(err, internaldb.ErrWebhookEventNotFound) {
			s.handleDashboardWebhooks(w, r)
			return
		}
		s.handleDashboardWebhooks(w, r)
		return
	}
	s.handleDashboardWebhooks(w, r)
}

func (s *Server) handleDashboardRetryAllWebhooks(w http.ResponseWriter, r *http.Request) {
	if _, err := s.db.RetryAllFailedDeadWebhookEvents(r.Context()); err != nil {
		s.handleDashboardWebhooks(w, r)
		return
	}
	s.handleDashboardWebhooks(w, r)
}

func (s *Server) handleDashboardRetryBlock(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.handleDashboardRetries(w, r)
		return
	}
	blockNum, err := strconv.ParseInt(strings.TrimSpace(r.FormValue("blockNumber")), 10, 64)
	if err != nil || blockNum <= 0 {
		s.handleDashboardRetries(w, r)
		return
	}
	if _, err := s.db.EnqueueRetryJob(r.Context(), blockNum, blockNum); err != nil {
		s.handleDashboardRetries(w, r)
		return
	}
	s.handleDashboardRetries(w, r)
}

func (s *Server) handleDashboardRetryRange(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.handleDashboardRetries(w, r)
		return
	}
	fromBlock, err1 := strconv.ParseInt(strings.TrimSpace(r.FormValue("fromBlock")), 10, 64)
	toBlock, err2 := strconv.ParseInt(strings.TrimSpace(r.FormValue("toBlock")), 10, 64)
	if err1 != nil || err2 != nil {
		s.handleDashboardRetries(w, r)
		return
	}
	if _, err := s.db.EnqueueRetryJob(r.Context(), fromBlock, toBlock); err != nil {
		s.handleDashboardRetries(w, r)
		return
	}
	s.handleDashboardRetries(w, r)
}
