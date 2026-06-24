package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/degoke/tronvent/internal/api"
	"github.com/degoke/tronvent/internal/config"
	internaldb "github.com/degoke/tronvent/internal/db"
	"github.com/degoke/tronvent/internal/store"
)

type memDB struct {
	addresses []internaldb.WatchedAddress
	contracts []internaldb.WatchedContract
	webhook   *internaldb.WebhookConfig
	cursors   []internaldb.CursorRow
	retries   []internaldb.RetryJobRecord
}

func (m *memDB) ListActiveAddresses(_ context.Context) ([]string, error) {
	var out []string
	for _, a := range m.addresses {
		if a.Status == "active" {
			out = append(out, a.Address)
		}
	}
	return out, nil
}

func (m *memDB) ListActiveContracts(_ context.Context) ([]string, error) {
	var out []string
	for _, c := range m.contracts {
		if c.Status == "active" {
			out = append(out, c.ContractAddress)
		}
	}
	return out, nil
}

func (m *memDB) GetWebhookConfig(_ context.Context) (*internaldb.WebhookConfig, error) {
	return m.webhook, nil
}

func (m *memDB) AddWatchedAddress(_ context.Context, address, source string) (internaldb.WatchedAddress, bool, error) {
	for _, a := range m.addresses {
		if a.Address == address {
			return a, false, nil
		}
	}
	row := internaldb.WatchedAddress{
		ID: "id-1", Address: address, Status: "active", Source: source, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	m.addresses = append(m.addresses, row)
	return row, true, nil
}

func (m *memDB) ListAddresses(_ context.Context, status string, limit int, afterAddress string) ([]internaldb.WatchedAddress, error) {
	return m.addresses, nil
}

func (m *memDB) AddWatchedContract(_ context.Context, contractAddress, tokenSymbol, source string) (internaldb.WatchedContract, bool, error) {
	for _, c := range m.contracts {
		if c.ContractAddress == contractAddress {
			return c, false, nil
		}
	}
	sym := tokenSymbol
	row := internaldb.WatchedContract{
		ID: "c-1", ContractAddress: contractAddress, Status: "active", TokenSymbol: &sym, Source: source,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	m.contracts = append(m.contracts, row)
	return row, true, nil
}

func (m *memDB) ListContracts(_ context.Context, status string, limit int, afterContract string) ([]internaldb.WatchedContract, error) {
	return m.contracts, nil
}

func (m *memDB) UpsertWebhookConfig(_ context.Context, webhookURL, signingSecret string, isActive bool, source string) (*internaldb.WebhookConfig, error) {
	cfg := &internaldb.WebhookConfig{
		WebhookURL: webhookURL, SigningSecret: signingSecret, IsActive: isActive, Source: source, UpdatedAt: time.Now(),
	}
	m.webhook = cfg
	return cfg, nil
}

func (m *memDB) DeactivateWatchedAddress(_ context.Context, address string) (internaldb.WatchedAddress, error) {
	for i, a := range m.addresses {
		if a.Address == address {
			if a.Status == "inactive" {
				return a, nil
			}
			m.addresses[i].Status = "inactive"
			m.addresses[i].UpdatedAt = time.Now()
			return m.addresses[i], nil
		}
	}
	return internaldb.WatchedAddress{}, internaldb.ErrWatchedAddressNotFound
}

func (m *memDB) DeactivateWatchedContract(_ context.Context, contractAddress string) (internaldb.WatchedContract, error) {
	for i, c := range m.contracts {
		if c.ContractAddress == contractAddress {
			if c.Status == "inactive" {
				return c, nil
			}
			m.contracts[i].Status = "inactive"
			m.contracts[i].UpdatedAt = time.Now()
			return m.contracts[i], nil
		}
	}
	return internaldb.WatchedContract{}, internaldb.ErrWatchedContractNotFound
}

func (m *memDB) ListCursors(_ context.Context) ([]internaldb.CursorRow, error) {
	return m.cursors, nil
}

func (m *memDB) EnqueueRetryJob(_ context.Context, fromBlock, toBlock int64) (internaldb.EnqueueRetryResult, error) {
	if err := internaldb.ValidateBlockRange(fromBlock, toBlock); err != nil {
		return internaldb.EnqueueRetryResult{}, err
	}
	for _, job := range m.retries {
		if job.Status == "pending" || job.Status == "running" {
			if job.FromBlock == fromBlock && job.ToBlock == toBlock {
				return internaldb.EnqueueRetryResult{Job: job, Created: false}, nil
			}
		}
	}
	job := internaldb.RetryJobRecord{
		ID:        "retry-1",
		Queue:     internaldb.QueueTronAdminRetry,
		JobType:   internaldb.JobTypeBlockRange,
		FromBlock: fromBlock,
		ToBlock:   toBlock,
		Status:    "pending",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	m.retries = append([]internaldb.RetryJobRecord{job}, m.retries...)
	return internaldb.EnqueueRetryResult{Job: job, Created: true}, nil
}

func (m *memDB) ListRetryJobs(_ context.Context, status string, limit int) ([]internaldb.RetryJobRecord, error) {
	var out []internaldb.RetryJobRecord
	for _, job := range m.retries {
		if status != "" && job.Status != status {
			continue
		}
		out = append(out, job)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func newTestServer(t *testing.T, mem *memDB) *api.Server {
	t.Helper()
	addrStore := store.NewAddressStore(mem)
	contractStore := store.NewContractStore(mem)
	webhookStore := store.NewWebhookConfigStore(mem)
	cfg := &config.Config{HealthPort: "0", AdminAPIToken: "secret", TronGridBaseURL: "https://api.trongrid.io"}
	return api.New(cfg, mem, addrStore, contractStore, webhookStore)
}

func TestPostAddressRequiresAuth(t *testing.T) {
	mem := &memDB{}
	srv := newTestServer(t, mem)
	body, _ := json.Marshal(map[string]string{"address": "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/addresses", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestPostAddressCreatesRecord(t *testing.T) {
	mem := &memDB{}
	srv := newTestServer(t, mem)
	addr := "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	body, _ := json.Marshal(map[string]string{"address": addr})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/addresses", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestPutWebhookUpdatesConfig(t *testing.T) {
	mem := &memDB{}
	srv := newTestServer(t, mem)
	body, _ := json.Marshal(map[string]string{
		"webhookUrl":    "https://example.com/hook",
		"signingSecret": "supersecret",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/webhook", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if mem.webhook == nil || mem.webhook.WebhookURL != "https://example.com/hook" {
		t.Fatal("webhook config not saved")
	}
}

func TestPostRetryBlockEnqueuesJob(t *testing.T) {
	mem := &memDB{}
	srv := newTestServer(t, mem)
	body, _ := json.Marshal(map[string]int64{"blockNumber": 12345678})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/retries/block", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(mem.retries) != 1 {
		t.Fatalf("expected 1 retry job, got %d", len(mem.retries))
	}
	if mem.retries[0].FromBlock != 12345678 || mem.retries[0].ToBlock != 12345678 {
		t.Fatalf("unexpected range: %+v", mem.retries[0])
	}
}

func TestPostRetryRangeValidatesBounds(t *testing.T) {
	mem := &memDB{}
	srv := newTestServer(t, mem)
	body, _ := json.Marshal(map[string]int64{"fromBlock": 100, "toBlock": 50})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/retries/range", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestPostRetryRangeDedupesPendingJob(t *testing.T) {
	mem := &memDB{}
	srv := newTestServer(t, mem)
	body, _ := json.Marshal(map[string]int64{"fromBlock": 100, "toBlock": 200})

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/retries/range", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer secret")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("attempt %d: expected 202, got %d", i+1, rec.Code)
		}
	}
	if len(mem.retries) != 1 {
		t.Fatalf("expected deduped to 1 job, got %d", len(mem.retries))
	}
}

func TestGetRetriesListsJobs(t *testing.T) {
	mem := &memDB{retries: []internaldb.RetryJobRecord{{
		ID: "retry-1", Queue: internaldb.QueueTronAdminRetry, FromBlock: 1, ToBlock: 2, Status: "pending",
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}}}
	srv := newTestServer(t, mem)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/retries", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
}

func TestDeleteAddressDeactivatesAndReloads(t *testing.T) {
	addr := "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	mem := &memDB{addresses: []internaldb.WatchedAddress{{
		ID: "id-1", Address: addr, Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}}}
	srv := newTestServer(t, mem)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/addresses/"+addr, nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if mem.addresses[0].Status != "inactive" {
		t.Fatal("expected inactive status in db")
	}
}
