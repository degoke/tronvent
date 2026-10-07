package scanner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	internaldb 	"github.com/degoke/tronvent/internal/db"
	"github.com/degoke/tronvent/internal/metrics"
)

// ErrScopeLeaseLost is returned when a forward scan loses its exclusive scope lease.
var ErrScopeLeaseLost = errors.New("scanner scope lease lost")

// NewScannerWorkerID identifies this process for scanner scope and startup reconcile leases.
func NewScannerWorkerID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	suffix := "0"
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		suffix = hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("scanner-%s-%d-%s", host, os.Getpid(), suffix)
}

func (p *Poller) scannerWorkerID() string {
	if p.scanWorkerID != "" {
		return p.scanWorkerID
	}
	return "test-scanner"
}

func (p *Poller) withScopeLease(ctx context.Context, scope string, fn func(ctx context.Context, cursorAtClaim int64) error) error {
	cursorAtClaim, claimed, err := p.db.TryClaimScannerScope(ctx, scope, p.scannerWorkerID(), internaldb.ScannerScopeLeaseDuration)
	if err != nil {
		return err
	}
	if !claimed {
		metrics.ScopeLeaseSkipped.WithLabelValues("forward_scan").Inc()
		slog.Debug("scanner scope held by another replica, skipping forward scan", "scope", scope)
		return nil
	}
	defer func() {
		relCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		released, err := p.db.ReleaseScannerScope(relCtx, scope, p.scannerWorkerID())
		if err != nil {
			slog.Warn("release scanner scope lease failed", "scope", scope, "err", err)
		} else if !released {
			slog.Warn("scanner scope lease was not held at release", "scope", scope)
		}
	}()
	return fn(ctx, cursorAtClaim)
}

func (p *Poller) renewScopeLease(ctx context.Context, scope string) error {
	if err := p.db.RenewScannerScopeLease(ctx, scope, p.scannerWorkerID(), internaldb.ScannerScopeLeaseDuration); err != nil {
		return fmt.Errorf("%w: %v", ErrScopeLeaseLost, err)
	}
	return nil
}

func (p *Poller) cursorAfterClaim(ctx context.Context, scope string, atClaim int64) (int64, error) {
	fromBlock, err := p.getHighestBlock(ctx, scope)
	if err != nil {
		return 0, err
	}
	return maxInt64(atClaim, fromBlock), nil
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (p *Poller) ensureScopeLeases(ctx context.Context) error {
	return p.db.RequireScannerCursorLeases(ctx)
}
