package scanner

import (
	"context"

	"github.com/degoke/tronvent/internal/webhookpayload"
)

// matchedTransferEvents returns one webhook candidate per watched address role on the transfer.
// Receiver matches produce received events; sender matches produce broadcasted events.
// Bloom positives are confirmed against Postgres (active watchlist row) before enqueue.
func (p *Poller) matchedTransferEvents(
	ctx context.Context,
	confirm *AddressConfirm,
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
	if p.addresses.Contains(toAddr) && confirm.IsActive(ctx, toAddr) {
		ev := base
		ev.Direction = webhookpayload.DirectionReceived
		out = append(out, ev)
	}
	if p.addresses.Contains(fromAddr) && confirm.IsActive(ctx, fromAddr) {
		ev := base
		ev.Direction = webhookpayload.DirectionBroadcasted
		out = append(out, ev)
	}
	return out
}
