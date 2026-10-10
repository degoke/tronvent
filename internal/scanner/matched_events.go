package scanner

import (
	"context"

	"github.com/degoke/tronvent/internal/webhookpayload"
)

// matchedTransferEvents returns one webhook candidate per watched address role on the transfer.
// Receiver matches produce received events; sender matches produce broadcasted events.
// Bloom positives are confirmed against Postgres (active watchlist row) before enqueue.
// Confirmations are measured against the latest chain tip the poller has read.
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
		Confirmations:        confirmationsAt(p.chainTip.Load(), blockNumber),
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

// confirmationsAt returns how many blocks chainTip is ahead of blockNumber, matching
// the poller's "block N has (latest - N) confirmations" rule. Never negative.
func confirmationsAt(chainTip, blockNumber int64) int64 {
	if chainTip <= blockNumber {
		return 0
	}
	return chainTip - blockNumber
}
