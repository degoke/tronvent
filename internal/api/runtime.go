package api

import (
	"context"
	"net/http"
)

// ContractLag describes scanner lag for one watched TRC-20 contract.
type ContractLag struct {
	ContractAddress string
	Symbol          string
	Cursor          int64
	BlocksBehind    int64
}

// RuntimeSnapshot is live scanner tip/lag state for the dashboard and API.
type RuntimeSnapshot struct {
	CurrentBlock     int64
	TrxTargetBlock   int64
	Trc20TargetBlock int64
	TrxCursor        int64
	TrxBlocksBehind  int64
	ContractLags     []ContractLag
}

func blocksBehind(target, cursor int64) int64 {
	if cursor <= 0 || cursor >= target {
		return 0
	}
	return target - cursor
}

func (s *Server) buildRuntimeSnapshot(ctx context.Context) RuntimeSnapshot {
	snap := RuntimeSnapshot{}

	if s.chainTip != nil {
		current, err := s.chainTip.GetLatestBlockNumber(ctx)
		if err == nil && current > 0 {
			snap.CurrentBlock = current
			snap.TrxTargetBlock = current - s.cfg.RequiredConfs
			if snap.TrxTargetBlock < 0 {
				snap.TrxTargetBlock = 0
			}
			snap.Trc20TargetBlock = snap.TrxTargetBlock - s.cfg.Trc20EventConfs
			if snap.Trc20TargetBlock < 0 {
				snap.Trc20TargetBlock = 0
			}
		}
	}

	cursors, err := s.db.ListCursors(ctx)
	if err == nil {
		cursorByScope := make(map[string]int64, len(cursors))
		for _, c := range cursors {
			cursorByScope[c.Scope] = c.HighestBlock
		}
		snap.TrxCursor = cursorByScope["TRX"]
		snap.TrxBlocksBehind = blocksBehind(snap.TrxTargetBlock, snap.TrxCursor)

		var afterContract string
		for {
			contracts, err := s.db.ListContracts(ctx, "active", 500, afterContract, "")
			if err != nil || len(contracts) == 0 {
				break
			}
			for _, c := range contracts {
				cursor := cursorByScope[c.ContractAddress]
				symbol := ""
				if c.TokenSymbol != nil {
					symbol = *c.TokenSymbol
				}
				snap.ContractLags = append(snap.ContractLags, ContractLag{
					ContractAddress: c.ContractAddress,
					Symbol:          symbol,
					Cursor:          cursor,
					BlocksBehind:    blocksBehind(snap.Trc20TargetBlock, cursor),
				})
				afterContract = c.ContractAddress
			}
			if len(contracts) < 500 {
				break
			}
		}
	}

	return snap
}

func (s *Server) handleGetRuntime(w http.ResponseWriter, r *http.Request) {
	cursors, err := s.db.ListCursors(r.Context())
	if err != nil {
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

	snap := s.buildRuntimeSnapshot(r.Context())
	contractLagOut := make([]map[string]any, 0, len(snap.ContractLags))
	for _, lag := range snap.ContractLags {
		item := map[string]any{
			"contractAddress": lag.ContractAddress,
			"cursor":          lag.Cursor,
			"blocksBehind":    lag.BlocksBehind,
		}
		if lag.Symbol != "" {
			item["symbol"] = lag.Symbol
		}
		contractLagOut = append(contractLagOut, item)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tronGridBaseUrl":      s.cfg.TronGridBaseURL,
		"watchedAddressCount":  s.addresses.Len(),
		"watchedContractCount": s.contracts.Len(),
		"contracts":            s.contracts.List(),
		"cursors":              cursorOut,
		"currentBlock":         snap.CurrentBlock,
		"trxTargetBlock":       snap.TrxTargetBlock,
		"trc20TargetBlock":     snap.Trc20TargetBlock,
		"trxCursor":            snap.TrxCursor,
		"trxBlocksBehind":      snap.TrxBlocksBehind,
		"contractLags":         contractLagOut,
	})
}
