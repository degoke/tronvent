package scanner

import (
	"context"
	"errors"
	"testing"
	"time"
)

type batchConfirmDB struct {
	active   map[string]bool
	batch    int
	single   int
	batchErr error
	singleErr error
}

func (d *batchConfirmDB) IsWatchedAddressActive(_ context.Context, address string) (bool, error) {
	d.single++
	if d.singleErr != nil {
		return false, d.singleErr
	}
	return d.active[address], nil
}

func (d *batchConfirmDB) ActiveWatchedAddresses(_ context.Context, addresses []string) (map[string]bool, error) {
	d.batch++
	if d.batchErr != nil {
		return nil, d.batchErr
	}
	out := make(map[string]bool, len(addresses))
	for _, a := range addresses {
		out[a] = d.active[a]
	}
	return out, nil
}

func TestAddressConfirm_PrefetchUsesBatch(t *testing.T) {
	db := &batchConfirmDB{active: map[string]bool{"a": true, "b": false}}
	c := NewAddressConfirm(db)
	c.Prefetch(context.Background(), []string{"a", "b", "a"})
	if db.batch != 1 {
		t.Fatalf("expected one batch query, got %d", db.batch)
	}
	if !c.IsActive(context.Background(), "a") || c.IsActive(context.Background(), "b") {
		t.Fatal("expected cached active/inactive values")
	}
	if db.single != 0 {
		t.Fatalf("expected no single-row lookups after prefetch, got %d", db.single)
	}
}

func TestAddressConfirm_PrefetchBatchErrorFallsBackPerAddress(t *testing.T) {
	db := &batchConfirmDB{
		batchErr: errors.New("batch down"),
		active:   map[string]bool{"x": false, "y": true},
	}
	c := NewAddressConfirm(db)
	c.Prefetch(context.Background(), []string{"x", "y"})
	if db.batch != addressConfirmMaxAttempts {
		t.Fatalf("expected %d batch attempts, got %d", addressConfirmMaxAttempts, db.batch)
	}
	if db.single != 2 {
		t.Fatalf("expected per-address fallback lookups, got %d", db.single)
	}
	if c.IsActive(context.Background(), "x") {
		t.Fatal("expected inactive x, not batch fail-open")
	}
	if !c.IsActive(context.Background(), "y") {
		t.Fatal("expected active y from single lookup")
	}
}

func TestAddressConfirm_SingleLookupFailOpen(t *testing.T) {
	db := &batchConfirmDB{batchErr: errors.New("batch down"), singleErr: errors.New("db down")}
	c := NewAddressConfirm(db)
	if !c.IsActive(context.Background(), "y") {
		t.Fatal("expected fail-open on single lookup error")
	}
}

func TestAddressConfirm_ContextCancelFailOpen(t *testing.T) {
	db := &slowFailDB{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewAddressConfirm(db)
	if !c.IsActive(ctx, "z") {
		t.Fatal("expected fail-open when context cancelled during confirm")
	}
}

type slowFailDB struct{}

func (slowFailDB) IsWatchedAddressActive(context.Context, string) (bool, error) {
	return false, errors.New("transient")
}

func (slowFailDB) ActiveWatchedAddresses(context.Context, []string) (map[string]bool, error) {
	return nil, errors.New("transient")
}

func TestAddressConfirm_ContextCancelDuringSleepFailOpen(t *testing.T) {
	db := &batchConfirmDB{batchErr: errors.New("batch down"), singleErr: errors.New("transient")}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	c := NewAddressConfirm(db)
	if !c.IsActive(ctx, "addr") {
		t.Fatal("expected fail-open when context expires during confirm retries")
	}
}

func TestAddressConfirm_PrefetchContextCancelFailOpen(t *testing.T) {
	db := &batchConfirmDB{batchErr: context.Canceled, active: map[string]bool{"x": false}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := NewAddressConfirm(db)
	c.Prefetch(ctx, []string{"x"})
	if !c.IsActive(ctx, "x") {
		t.Fatal("expected prefetch cancel to fail-open cached addresses")
	}
}

func TestBloomConfirmCandidates_DedupesSelfTransfer(t *testing.T) {
	hs := NewHashSet([]string{"TSameXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"})
	addrs := bloomConfirmCandidates(hs, "TSameXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX", "TSameXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX")
	if len(addrs) != 1 {
		t.Fatalf("expected one candidate for self-transfer, got %v", addrs)
	}
}
