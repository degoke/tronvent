package scanner

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/degoke/tronvent/internal/config"
)

type leaseFailDB struct {
	stubDB
	renewCalls int
}

func (s *leaseFailDB) RenewScannerScopeLease(context.Context, string, string, time.Duration) error {
	s.renewCalls++
	if s.renewCalls > 1 {
		return errors.New("lease not held")
	}
	return nil
}

func TestRenewScopeLeaseSurfacesLeaseLost(t *testing.T) {
	p := &Poller{
		db:           &leaseFailDB{},
		scanWorkerID: "worker-1",
	}
	if err := p.renewScopeLease(context.Background(), "TRX"); err != nil {
		t.Fatalf("first renew: %v", err)
	}
	err := p.renewScopeLease(context.Background(), "TRX")
	if !errors.Is(err, ErrScopeLeaseLost) {
		t.Fatalf("expected ErrScopeLeaseLost, got %v", err)
	}
}

func TestPoller_SkipsForwardScanWhenScopeNotClaimed(t *testing.T) {
	srv := mockTronGridServer(t)
	defer srv.Close()

	stubDatabaseClient := newStubDB(99)
	stubDatabaseClient.scopeClaimOK = map[string]bool{"TRX": false}

	cfg := &config.Config{
		TronGridBaseURL: srv.URL,
		TronGridAPIKey:  "test-api-key",
		RequiredConfs:   0,
	}

	p := &Poller{
		cfg:        cfg,
		db:         stubDatabaseClient,
		outbox:     &stubOutbox{},
		addresses:  NewHashSet(nil),
		contracts:  stubContracts{},
		httpClient: srv.Client(),
		sem:        make(chan struct{}, 5),
	}

	if err := p.poll(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}

	stubDatabaseClient.mu.Lock()
	trxBlock := stubDatabaseClient.scannedBlocks["TRX"]
	stubDatabaseClient.mu.Unlock()
	if trxBlock != 99 {
		t.Fatalf("TRX cursor should not advance when lease not claimed, got %d", trxBlock)
	}
}

func TestPoller_AbortsForwardScanWhenRenewFails(t *testing.T) {
	srv := mockTronGridServer(t)
	defer srv.Close()

	stub := &leaseFailDB{}
	stub.scannedBlocks = map[string]int64{"TRX": 99}

	cfg := &config.Config{
		TronGridBaseURL: srv.URL,
		TronGridAPIKey:  "test-api-key",
		RequiredConfs:   0,
	}

	p := &Poller{
		cfg:        cfg,
		db:         stub,
		outbox:     &stubOutbox{},
		addresses:  NewHashSet(nil),
		contracts:  stubContracts{},
		httpClient: srv.Client(),
		sem:        make(chan struct{}, 5),
	}

	err := p.scanTrx(context.Background(), 100)
	if err == nil || !errors.Is(err, ErrScopeLeaseLost) {
		t.Fatalf("expected scanTrx to fail with lease lost, got %v", err)
	}

	stub.mu.Lock()
	trxBlock := stub.scannedBlocks["TRX"]
	stub.mu.Unlock()
	if trxBlock != 99 {
		t.Fatalf("cursor should not advance after lease loss, got %d", trxBlock)
	}
}

type leasesDisabledDB struct {
	stubDB
}

func (s *leasesDisabledDB) RequireScannerCursorLeases(context.Context) error {
	return errors.New("scanner cursor lease migration required")
}

func TestPoller_RunStopsWhenLeasesUnavailable(t *testing.T) {
	p := &Poller{
		cfg: &config.Config{PollIntervalMs: 1000},
		db:  &leasesDisabledDB{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.Run(ctx)
	}()
	wg.Wait()

	if p.ForwardScanReady() {
		t.Fatal("forward scan should not be ready when leases unavailable")
	}
}
