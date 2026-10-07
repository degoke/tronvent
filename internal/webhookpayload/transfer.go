package webhookpayload

import "strings"

// TransferDedupeKey identifies one transfer leg (same tx can include several).
func TransferDedupeKey(fromAddress, toAddress, amount string) string {
	return strings.TrimSpace(fromAddress) + ":" + strings.TrimSpace(toAddress) + ":" + strings.TrimSpace(amount)
}
