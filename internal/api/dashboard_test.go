package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/degoke/tronvent/internal/api"
	"github.com/degoke/tronvent/internal/config"
	internaldb "github.com/degoke/tronvent/internal/db"
	"github.com/degoke/tronvent/internal/store"
)

func newDashboardServer(t *testing.T, mem *memDB) *api.Server {
	t.Helper()
	addrStore := store.NewAddressStore(mem)
	contractStore := store.NewContractStore(mem)
	webhookStore := store.NewWebhookConfigStore(mem)
	cfg := &config.Config{
		HealthPort:      "0",
		AdminAPIToken:   "secret",
		RequiredConfs:   20,
		Trc20EventConfs: 10,
	}
	mem.cursors = []internaldb.CursorRow{
		{Scope: "TRX", HighestBlock: 49_999_970},
	}
	return api.New(cfg, mem, addrStore, contractStore, webhookStore, stubChainTip{})
}

func dashboardSessionCookie(t *testing.T, srv *api.Server) *http.Cookie {
	t.Helper()
	loginReq := httptest.NewRequest(http.MethodPost, "/dashboard/login", strings.NewReader(url.Values{"token": {"secret"}}.Encode()))
	loginReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	loginRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusFound {
		t.Fatalf("login expected 302, got %d", loginRec.Code)
	}
	for _, c := range loginRec.Result().Cookies() {
		if c.Name == "tronvent_dashboard" {
			return c
		}
	}
	t.Fatal("session cookie not set")
	return nil
}

func dashboardCSRFCookie(t *testing.T, srv *api.Server, session *http.Cookie) *http.Cookie {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("dashboard expected 200, got %d", rec.Code)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "tronvent_csrf" {
			return c
		}
	}
	t.Fatal("csrf cookie not set")
	return nil
}

func dashboardPOST(t *testing.T, srv *api.Server, path, body string, session, csrf *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if csrf != nil {
		req.Header.Set("X-CSRF-Token", csrf.Value)
		req.AddCookie(csrf)
	}
	if session != nil {
		req.AddCookie(session)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func dashboardGET(t *testing.T, srv *api.Server, path string, session *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if session != nil {
		req.AddCookie(session)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestDashboardLoginSuccess(t *testing.T) {
	srv := newDashboardServer(t, &memDB{})
	cookie := dashboardSessionCookie(t, srv)
	if cookie.Value == "" {
		t.Fatal("expected non-empty session")
	}
}

func TestDashboardLoginFailure(t *testing.T) {
	srv := newDashboardServer(t, &memDB{})
	req := httptest.NewRequest(http.MethodPost, "/dashboard/login", strings.NewReader("token=wrong"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with error page, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid token") {
		t.Fatal("expected invalid token message")
	}
}

func TestDashboardRequiresSession(t *testing.T) {
	srv := newDashboardServer(t, &memDB{})
	req := httptest.NewRequest(http.MethodGet, "/dashboard", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("expected redirect, got %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Location"), "/dashboard/login") {
		t.Fatal("expected login redirect")
	}
}

func TestDashboardPOSTRejectsMissingCSRF(t *testing.T) {
	srv := newDashboardServer(t, &memDB{})
	session := dashboardSessionCookie(t, srv)
	req := httptest.NewRequest(http.MethodPost, "/dashboard/action/watchlist/address/add", strings.NewReader("address=TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(session)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestDashboardPOSTWithCSRF(t *testing.T) {
	mem := &memDB{}
	srv := newDashboardServer(t, mem)
	session := dashboardSessionCookie(t, srv)
	csrf := dashboardCSRFCookie(t, srv, session)

	req := httptest.NewRequest(http.MethodPost, "/dashboard/action/watchlist/address/add",
		strings.NewReader("address=TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", csrf.Value)
	req.AddCookie(session)
	req.AddCookie(csrf)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(mem.addresses) != 1 {
		t.Fatalf("expected 1 address, got %d", len(mem.addresses))
	}
}

func TestRuntimeIncludesLiveTipFields(t *testing.T) {
	mem := &memDB{
		cursors: []internaldb.CursorRow{{Scope: "TRX", HighestBlock: 49_999_970, UpdatedAt: time.Now()}},
		contracts: []internaldb.WatchedContract{{
			ContractAddress: "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
			Status:          "active",
			CreatedAt:       time.Now(), UpdatedAt: time.Now(),
		}},
	}
	srv := newDashboardServer(t, mem)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtime", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, key := range []string{"currentBlock", "trxTargetBlock", "trc20TargetBlock", "trxCursor", "trxBlocksBehind", "contractLags"} {
		if !strings.Contains(body, key) {
			t.Fatalf("expected %s in runtime response", key)
		}
	}
}

func TestBearerAuthStillWorks(t *testing.T) {
	srv := newDashboardServer(t, &memDB{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtime", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestRuntimeNoNegativeLag(t *testing.T) {
	mem := &memDB{
		cursors: []internaldb.CursorRow{{Scope: "TRX", HighestBlock: 60_000_000, UpdatedAt: time.Now()}},
	}
	srv := newDashboardServer(t, mem)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtime", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), `"trxBlocksBehind":-`) {
		t.Fatal("expected no negative trxBlocksBehind")
	}
}

func TestDashboardPOSTRejectsInvalidCSRF(t *testing.T) {
	srv := newDashboardServer(t, &memDB{})
	session := dashboardSessionCookie(t, srv)
	csrf := dashboardCSRFCookie(t, srv, session)

	req := httptest.NewRequest(http.MethodPost, "/dashboard/action/watchlist/address/add",
		strings.NewReader("address=TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", "wrong-token")
	req.AddCookie(session)
	req.AddCookie(csrf)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestDashboardWatchlistDeactivateReactivate(t *testing.T) {
	addr := "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	mem := &memDB{addresses: []internaldb.WatchedAddress{{
		ID: "id-1", Address: addr, Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}}}
	srv := newDashboardServer(t, mem)
	session := dashboardSessionCookie(t, srv)
	csrf := dashboardCSRFCookie(t, srv, session)

	rec := dashboardPOST(t, srv, "/dashboard/action/watchlist/address/deactivate", "address="+addr, session, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("deactivate expected 200, got %d", rec.Code)
	}
	if mem.addresses[0].Status != "inactive" {
		t.Fatal("expected inactive after deactivate")
	}

	rec = dashboardPOST(t, srv, "/dashboard/action/watchlist/address/reactivate", "address="+addr, session, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("reactivate expected 200, got %d", rec.Code)
	}
	if mem.addresses[0].Status != "active" {
		t.Fatal("expected active after reactivate")
	}
}

func TestDashboardWatchlistExactSearch(t *testing.T) {
	addr1 := "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"
	addr2 := "TXYZabcdefghijklmnopqrstuvwxyz123456"
	mem := &memDB{addresses: []internaldb.WatchedAddress{
		{ID: "1", Address: addr1, Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "2", Address: addr2, Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}}
	srv := newDashboardServer(t, mem)
	session := dashboardSessionCookie(t, srv)

	rec := dashboardGET(t, srv, "/dashboard/partial/watchlist/addresses?search="+addr1, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, addr1) {
		t.Fatal("expected matching address in response")
	}
	if strings.Contains(body, addr2) {
		t.Fatal("expected non-matching address excluded")
	}
}

func TestDashboardWatchlistPagination(t *testing.T) {
	mem := &memDB{}
	for i := 0; i < 30; i++ {
		mem.addresses = append(mem.addresses, internaldb.WatchedAddress{
			ID: "id", Address: "T" + strings.Repeat("A", i+1) + "zzzzzzzzzzzzzzzzzzzzzzzzzz",
			Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now(),
		})
	}
	srv := newDashboardServer(t, mem)
	session := dashboardSessionCookie(t, srv)

	rec := dashboardGET(t, srv, "/dashboard/partial/watchlist/addresses", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Load more") {
		t.Fatal("expected pagination Load more button")
	}
}

func TestDashboardWebhookPreservesSecret(t *testing.T) {
	mem := &memDB{webhook: &internaldb.WebhookConfig{
		WebhookURL: "https://old.example/hook", SigningSecret: "keep-me", IsActive: true, UpdatedAt: time.Now(),
	}}
	srv := newDashboardServer(t, mem)
	session := dashboardSessionCookie(t, srv)
	csrf := dashboardCSRFCookie(t, srv, session)

	body := "webhookUrl=https://new.example/hook&isActive=on"
	rec := dashboardPOST(t, srv, "/dashboard/action/webhook/settings", body, session, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if mem.webhook.SigningSecret != "keep-me" {
		t.Fatalf("expected secret preserved, got %q", mem.webhook.SigningSecret)
	}
	if mem.webhook.WebhookURL != "https://new.example/hook" {
		t.Fatalf("expected URL updated, got %q", mem.webhook.WebhookURL)
	}
}

func TestDashboardWebhookRetryEvent(t *testing.T) {
	mem := &memDB{webhookEvents: []internaldb.DashboardWebhookEvent{
		{ID: "ev-1", Status: "failed", AttemptCount: 3},
		{ID: "ev-2", Status: "delivered", AttemptCount: 1},
	}}
	srv := newDashboardServer(t, mem)
	session := dashboardSessionCookie(t, srv)
	csrf := dashboardCSRFCookie(t, srv, session)

	rec := dashboardPOST(t, srv, "/dashboard/action/webhook/retry/ev-1", "", session, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if mem.webhookEvents[0].Status != "pending" || mem.webhookEvents[0].AttemptCount != 3 {
		t.Fatal("expected failed event rescheduled with attempt_count preserved")
	}
	if mem.webhookEvents[1].Status != "delivered" {
		t.Fatal("expected delivered event unchanged")
	}
}

func TestDashboardWebhookEventStatusFilter(t *testing.T) {
	mem := &memDB{webhookEvents: []internaldb.DashboardWebhookEvent{
		{ID: "ev-1", Status: "failed", TxHash: "abc"},
		{ID: "ev-2", Status: "delivered", TxHash: "def"},
	}}
	srv := newDashboardServer(t, mem)
	session := dashboardSessionCookie(t, srv)

	rec := dashboardGET(t, srv, "/dashboard/partial/webhooks?status=failed", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "abc") {
		t.Fatal("expected failed event in filtered view")
	}
	if strings.Contains(body, "def") {
		t.Fatal("expected delivered event excluded from failed filter")
	}
}

func TestDashboardRetriesBothQueues(t *testing.T) {
	mem := &memDB{retries: []internaldb.RetryJobRecord{
		{ID: "1", Queue: internaldb.QueueTronAdminRetry, FromBlock: 1, ToBlock: 2, Status: "pending", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "2", Queue: internaldb.QueueTronReconcile, FromBlock: 3, ToBlock: 4, Status: "running", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "3", Queue: internaldb.QueueTronReconcile, FromBlock: 5, ToBlock: 6, Status: "completed", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}}
	srv := newDashboardServer(t, mem)
	session := dashboardSessionCookie(t, srv)

	rec := dashboardGET(t, srv, "/dashboard/partial/retries", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, internaldb.QueueTronAdminRetry) || !strings.Contains(body, internaldb.QueueTronReconcile) {
		t.Fatal("expected both queues in default pending/running view")
	}
	if strings.Contains(body, "5–6") {
		t.Fatal("expected completed job excluded from default filter")
	}
}

func TestDashboardRetryRangeValidatesBounds(t *testing.T) {
	mem := &memDB{}
	srv := newDashboardServer(t, mem)
	session := dashboardSessionCookie(t, srv)
	csrf := dashboardCSRFCookie(t, srv, session)

	before := len(mem.retries)
	rec := dashboardPOST(t, srv, "/dashboard/action/retries/range", "fromBlock=100&toBlock=50", session, csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (re-render panel), got %d", rec.Code)
	}
	if len(mem.retries) != before {
		t.Fatal("expected invalid range to not enqueue job")
	}
}

func TestDashboardOverviewRendersLag(t *testing.T) {
	mem := &memDB{
		cursors: []internaldb.CursorRow{{Scope: "TRX", HighestBlock: 49_999_970, UpdatedAt: time.Now()}},
	}
	srv := newDashboardServer(t, mem)
	session := dashboardSessionCookie(t, srv)

	rec := dashboardGET(t, srv, "/dashboard/partial/overview", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Chain Tip", "TRX Lag", "49999970", "50000000"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected overview to contain %q", want)
		}
	}
}

func TestDashboardWatchlistContractsRefreshURL(t *testing.T) {
	mem := &memDB{}
	srv := newDashboardServer(t, mem)
	session := dashboardSessionCookie(t, srv)

	rec := dashboardGET(t, srv, "/dashboard/partial/watchlist/contracts?status=active", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `data-refresh-url="/dashboard/partial/watchlist/contracts?status=active"`) {
		t.Fatal("expected contracts refresh URL on contracts pane")
	}
}

func TestRuntimeContractLagPagination(t *testing.T) {
	mem := &memDB{cursors: []internaldb.CursorRow{{Scope: "TRX", HighestBlock: 100, UpdatedAt: time.Now()}}}
	for i := 0; i < 510; i++ {
		mem.contracts = append(mem.contracts, internaldb.WatchedContract{
			ContractAddress: fmt.Sprintf("TContract%04dzzzzzzzzzzzzzzzz", i),
			Status:          "active",
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		})
	}
	srv := newDashboardServer(t, mem)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runtime", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		ContractLags []any `json:"contractLags"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.ContractLags) != 510 {
		t.Fatalf("expected all 510 contract lags, got %d", len(resp.ContractLags))
	}
}
