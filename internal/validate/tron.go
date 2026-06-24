package validate

import (
	btcbase58 "github.com/btcsuite/btcd/btcutil/base58"
)

// TronAddress reports whether addr is a valid Tron base58check address (mainnet-style T-prefix).
func TronAddress(addr string) bool {
	if len(addr) != 34 || addr[0] != 'T' {
		return false
	}
	payload, version, err := btcbase58.CheckDecode(addr)
	if err != nil {
		return false
	}
	return version == 0x41 && len(payload) == 20
}
