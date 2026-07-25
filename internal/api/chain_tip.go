package api

import "context"

// ChainTipProvider returns the current Tron chain tip block number.
type ChainTipProvider interface {
	GetLatestBlockNumber(ctx context.Context) (int64, error)
}
