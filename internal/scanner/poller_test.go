package scanner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/degoke/tronvent/internal/config"
	internaldb "github.com/degoke/tronvent/internal/db"
)

type stubDB struct {
	mu            sync.Mutex
	scannedBlocks map[string]int64
	retryJobs     []internaldb.BlockRangeJob
	completed     []string
	failed        []string
}

func newStubDB(initialBlock int64) *stubDB {
	return &stubDB{
		scannedBlocks: map[string]int64{"TRX": initialBlock},
	}
}

func (s *stubDB) GetScannedBlock(_ context.Context, scope string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scannedBlocks[scope], nil
}

func (s *stubDB) SetScannedBlock(_ context.Context, scope string, blockNum int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scannedBlocks[scope] = blockNum
	return nil
}

func (s *stubDB) ClaimBlockRangeJobs(_ context.Context, _ string, _ string, _ int) ([]internaldb.BlockRangeJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	jobs := s.retryJobs
	s.retryJobs = nil
	return jobs, nil
}

func (s *stubDB) ClaimBlockRangeJob(_ context.Context, _ string, _ string) (*internaldb.BlockRangeJob, error) {
	return nil, nil
}

func (s *stubDB) CompleteJob(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = append(s.completed, id)
	return nil
}

func (s *stubDB) FailJob(_ context.Context, id string, _ error, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed = append(s.failed, id)
	return nil
}

type stubOutbox struct {
	mu     sync.Mutex
	events []json.RawMessage
}

func (s *stubOutbox) EnqueueWebhookEvent(_ context.Context, eventType, scope, txHash string, blockNumber, blockTimestamp int64, payload any) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	id := fmt.Sprintf("evt-%s-%s", scope, txHash)
	m["id"] = id
	data, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.events = append(s.events, data)
	s.mu.Unlock()
	return id, nil
}

type stubContracts struct {
	contracts []string
}

func (s stubContracts) List() []string {
	return append([]string(nil), s.contracts...)
}

const knownTRC20Address = "TTestWatchedXXXXXXXXXXXXXXXXXXXXXXXX"

func mockTronGridServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/wallet/getnowblock", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"blockID":"0000000000000064abc","block_header":{"raw_data":{"number":100,"timestamp":1700000000000}},"transactions":[]}`)
	})
	mux.HandleFunc("/wallet/getblockbylimitnext", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"block":[{"blockID":"0000000000000064abc","block_header":{"raw_data":{"number":100,"timestamp":1700000000000}},"transactions":[]}]}`)
	})
	mux.HandleFunc("/v1/contracts/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := map[string]any{
			"data": []map[string]any{
				{
					"transaction_id":  "0xdeadbeef1",
					"block_number":    100,
					"block_timestamp": 1700000000000,
					"result": map[string]any{
						"from":  "TSenderXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
						"to":    knownTRC20Address,
						"value": "1500000",
					},
				},
			},
			"meta":    map[string]any{"page_size": 1, "at": int64(1700000000000), "fingerprint": ""},
			"success": true,
		}
		_ = json.NewEncoder(w).Encode(resp)
	})
	return httptest.NewServer(mux)
}

func TestAddressStoreReload(t *testing.T) {
	hs := NewHashSet(nil)
	callback := func(addr string) { hs.Add(addr) }
	callback("TAddr1XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX")
	if !hs.Contains("TAddr1XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX") {
		t.Fatal("expected address in hashset after add")
	}
}

func TestPoller_DetectsMatchedAddressAndEnqueuesEvent(t *testing.T) {
	srv := mockTronGridServer(t)
	defer srv.Close()

	outbox := &stubOutbox{}
	stubDatabaseClient := newStubDB(99)
	const usdtContract = "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"
	stubDatabaseClient.scannedBlocks[usdtContract] = 99

	cfg := &config.Config{
		TronGridBaseURL:   srv.URL,
		TronGridAPIKey:    "test-api-key",
		RequiredConfs:     0,
		Trc20EventConfs:   0,
		Trc20CursorRetain: 0,
		Trc20EventRetries: 1,
	}

	p := &Poller{
		cfg:        cfg,
		db:         stubDatabaseClient,
		outbox:     outbox,
		addresses:  NewHashSet([]string{knownTRC20Address}),
		contracts:  stubContracts{contracts: []string{usdtContract}},
		httpClient: srv.Client(),
		sem:        make(chan struct{}, 20),
	}

	if err := p.poll(context.Background()); err != nil {
		t.Fatalf("poll() error: %v", err)
	}

	outbox.mu.Lock()
	count := len(outbox.events)
	outbox.mu.Unlock()
	if count == 0 {
		t.Fatal("expected outbox event for matched TRC-20 address")
	}
	if !strings.Contains(string(outbox.events[0]), knownTRC20Address) {
		t.Errorf("expected event payload to contain watched address, got %s", outbox.events[0])
	}
}

func TestPoller_IgnoresUnmatchedAddresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "getnowblock"):
			_, _ = fmt.Fprint(w, `{"blockID":"block-100","block_header":{"raw_data":{"number":100,"timestamp":1700000000000}},"transactions":[]}`)
		case strings.Contains(r.URL.Path, "getblockbylimitnext"):
			_, _ = fmt.Fprint(w, `{"block":[{"blockID":"block-100","block_header":{"raw_data":{"number":100,"timestamp":1700000000000}},"transactions":[]}]}`)
		case strings.Contains(r.URL.Path, "/v1/contracts/"):
			resp := map[string]any{
				"data": []map[string]any{{
					"transaction_id": "0xdeadbeef2", "block_number": 100, "block_timestamp": 1700000000000,
					"result": map[string]any{"from": "TSender2XXXXXXXXXXXXXXXXXXXXXXXXXXXX", "to": "TUnknownReceiverXXXXXXXXXXXXXXXXXXX", "value": "5000000"},
				}},
				"meta": map[string]any{"page_size": 1, "at": int64(1700000000000), "fingerprint": ""}, "success": true,
			}
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer srv.Close()

	outbox := &stubOutbox{}
	const contract = "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"
	dbStub := newStubDB(99)
	dbStub.scannedBlocks[contract] = 99

	p := &Poller{
		cfg:        &config.Config{TronGridBaseURL: srv.URL, TronGridAPIKey: "test"},
		db:         dbStub,
		outbox:     outbox,
		addresses:  NewHashSet([]string{"TWatchedButDifferentXXXXXXXXXXXXXXXX"}),
		contracts:  stubContracts{contracts: []string{contract}},
		httpClient: srv.Client(),
		sem:        make(chan struct{}, 20),
	}
	if err := p.poll(context.Background()); err != nil {
		t.Fatalf("poll() error: %v", err)
	}
	outbox.mu.Lock()
	count := len(outbox.events)
	outbox.mu.Unlock()
	if count != 0 {
		t.Errorf("expected 0 events, got %d", count)
	}
}

func TestPoller_RespectsRequiredConfs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "getnowblock") {
			_, _ = fmt.Fprint(w, `{"blockID":"block-100","block_header":{"raw_data":{"number":100,"timestamp":1700000000000}},"transactions":[]}`)
			return
		}
		t.Errorf("unexpected request %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	const contract = "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"
	dbStub := newStubDB(98)
	dbStub.scannedBlocks[contract] = 98

	p := &Poller{
		cfg:        &config.Config{TronGridBaseURL: srv.URL, TronGridAPIKey: "test", RequiredConfs: 2},
		db:         dbStub,
		outbox:     &stubOutbox{},
		addresses:  NewHashSet([]string{knownTRC20Address}),
		contracts:  stubContracts{contracts: []string{contract}},
		httpClient: srv.Client(),
		sem:        make(chan struct{}, 5),
	}
	if err := p.poll(context.Background()); err != nil {
		t.Fatalf("poll() error: %v", err)
	}
}

func TestPoller_ColdStartUsesLatestBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "getnowblock") {
			_, _ = fmt.Fprint(w, `{"blockID":"block-200","block_header":{"raw_data":{"number":200,"timestamp":1700000200000}},"transactions":[]}`)
			return
		}
		t.Errorf("unexpected request %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	const contract = "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"
	dbStub := newStubDB(0)

	p := &Poller{
		cfg:        &config.Config{TronGridBaseURL: srv.URL, TronGridAPIKey: "test", RequiredConfs: 2, StartBlock: 0},
		db:         dbStub,
		outbox:     &stubOutbox{},
		addresses:  NewHashSet([]string{knownTRC20Address}),
		contracts:  stubContracts{contracts: []string{contract}},
		httpClient: srv.Client(),
		sem:        make(chan struct{}, 5),
	}
	if err := p.poll(context.Background()); err != nil {
		t.Fatalf("poll() error: %v", err)
	}
	dbStub.mu.Lock()
	trxCursor := dbStub.scannedBlocks["TRX"]
	dbStub.mu.Unlock()
	if trxCursor != 198 {
		t.Errorf("expected TRX cursor = 198 after cold start, got %d", trxCursor)
	}
}

func TestPoller_RetryBlockRange(t *testing.T) {
	const retryContract = "TXLAQ63Xg1NAzckPwKHvzw7CSEmLMEqcdj"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "getnowblock"):
			_, _ = fmt.Fprint(w, `{"blockID":"block-100","block_header":{"raw_data":{"number":100,"timestamp":1700000100000}},"transactions":[]}`)
		case strings.Contains(r.URL.Path, "getblockbylimitnext"):
			_, _ = fmt.Fprint(w, `{"block":[{"blockID":"block-50","block_header":{"raw_data":{"number":50,"timestamp":1700000050000}},"transactions":[]},{"blockID":"block-51","block_header":{"raw_data":{"number":51,"timestamp":1700000051000}},"transactions":[]}]}`)
		case strings.Contains(r.URL.Path, "/v1/contracts/"):
			resp := map[string]any{
				"data": []map[string]any{{
					"transaction_id": "retry_tx_001", "block_number": 51, "block_timestamp": 1700000051000,
					"result": map[string]any{"from": "TSenderRetryXXXXXXXXXXXXXXXXXXXXXXXX", "to": knownTRC20Address, "value": "2000000"},
				}},
				"meta": map[string]any{"page_size": 1, "at": int64(1700000051000), "fingerprint": ""}, "success": true,
			}
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer srv.Close()

	outbox := &stubOutbox{}
	dbStub := newStubDB(100)
	dbStub.scannedBlocks[retryContract] = 100
	dbStub.retryJobs = []internaldb.BlockRangeJob{{ID: "retry-1", FromBlock: 50, ToBlock: 51}}

	p := &Poller{
		cfg:        &config.Config{TronGridBaseURL: srv.URL, TronGridAPIKey: "test", RequiredConfs: 0},
		db:         dbStub,
		outbox:     outbox,
		addresses:  NewHashSet([]string{knownTRC20Address}),
		contracts:  stubContracts{contracts: []string{retryContract}},
		httpClient: srv.Client(),
		sem:        make(chan struct{}, 5),
	}
	if err := p.poll(context.Background()); err != nil {
		t.Fatalf("poll() error: %v", err)
	}
	outbox.mu.Lock()
	eventCount := len(outbox.events)
	outbox.mu.Unlock()
	dbStub.mu.Lock()
	forwardCursor := dbStub.scannedBlocks[retryContract]
	retryJobsLeft := len(dbStub.retryJobs)
	dbStub.mu.Unlock()

	if eventCount != 1 {
		t.Errorf("expected 1 replayed event, got %d", eventCount)
	}
	if forwardCursor != 100 {
		t.Errorf("expected forward cursor to stay at 100, got %d", forwardCursor)
	}
	if retryJobsLeft != 0 {
		t.Errorf("expected retry queue empty, got %d jobs", retryJobsLeft)
	}
}

func TestTrc20CursorAfterBatch(t *testing.T) {
	p := &Poller{cfg: &config.Config{Trc20CursorRetain: 50}}
	cases := []struct {
		name                             string
		from, batchStart, batchEnd, want int64
	}{
		{"large batch advances by retain", 99, 100, 199, 149},
		{"small batch does not move cursor backward", 99, 100, 110, 99},
		{"cold start advances within first batch", 0, 100, 149, 99},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := p.trc20CursorAfterBatch(tc.from, tc.batchStart, tc.batchEnd)
			if got != tc.want {
				t.Errorf("trc20CursorAfterBatch(%d,%d,%d) = %d, want %d", tc.from, tc.batchStart, tc.batchEnd, got, tc.want)
			}
		})
	}
}
