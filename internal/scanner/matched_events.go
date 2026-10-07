package scanner

import (
	"context"
	"log/slog"

	"github.com/degoke/tronvent/internal/webhookpayload"
)

// matchedTransferEvents returns one webhook candidate per watched address role on the transfer.
// Receiver matches produce received events; sender matches produce broadcasted events.
// Bloom positives are confirmed against Postgres (active watchlist row) before enqueue.
func (p *Poller) matchedTransferEvents(
	ctx context.Context,
	kind string,
	txHash, fromAddr, toAddr, amount, tokenContract string,
	blockNumber, blockTimestamp int64,
) []RawEvent {
	if !p.addresses.Contains(toAddr) && !p.addresses.Contains(fromAddr) {
		return nil
	}
	base := RawEvent{
		Type:                 kind,
		TxHash:               txHash,
		FromAddress:          fromAddr,
		ToAddress:            toAddr,
		Amount:               amount,
		TokenContractAddress: tokenContract,
		BlockNumber:          blockNumber,
		BlockTimestamp:       blockTimestamp,
	}
	var out []RawEvent
	if p.addresses.Contains(toAddr) && p.watchedAddressActive(ctx, toAddr) {
		ev := base
		ev.Direction = webhookpayload.DirectionReceived
		out = append(out, ev)
	}
	if p.addresses.Contains(fromAddr) && p.watchedAddressActive(ctx, fromAddr) {
		ev := base
		ev.Direction = webhookpayload.DirectionBroadcasted
		out = append(out, ev)
	}
	return out
}

func (p *Poller) watchedAddressActive(ctx context.Context, address string) bool {
	active, err := p.db.IsWatchedAddressActive(ctx, address)
	if err != nil {
		slog.Error("confirm watched address", "address", address, "err", err)
		return false
	}
	return active
}
