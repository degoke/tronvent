package scanner

import (
	"github.com/degoke/tronvent/internal/webhookpayload"
)

// matchedTransferEvents returns one webhook candidate per watched address role on the transfer.
// Receiver matches produce received events; sender matches produce broadcasted events.
func (p *Poller) matchedTransferEvents(
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
	if p.addresses.Contains(toAddr) {
		ev := base
		ev.Direction = webhookpayload.DirectionReceived
		out = append(out, ev)
	}
	if p.addresses.Contains(fromAddr) {
		ev := base
		ev.Direction = webhookpayload.DirectionBroadcasted
		out = append(out, ev)
	}
	return out
}
