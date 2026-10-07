package scanner

import (
	"fmt"

	"github.com/degoke/tronvent/internal/webhookpayload"
)

func transferEventKey(ev RawEvent) string {
	return fmt.Sprintf("%s:%s:%s", ev.TxHash, ev.Direction, webhookpayload.TransferDedupeKey(ev.FromAddress, ev.ToAddress, ev.Amount))
}
