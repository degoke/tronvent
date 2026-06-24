package scanner

import (
	"encoding/hex"

	btcbase58 "github.com/btcsuite/btcd/btcutil/base58"
)

// tronHexToBase58 converts a hex-encoded Tron internal address (e.g.
// "41abc...") to its base58check representation (the T-prefixed address
// users see). Tron uses Bitcoin's base58check format: the first byte is
// the version (0x41) and the remaining 20 bytes are the address payload.
func tronHexToBase58(hexAddr string) string {
	if len(hexAddr) == 0 {
		return ""
	}
	decoded, err := hex.DecodeString(hexAddr)
	if err != nil || len(decoded) != 21 {
		return hexAddr // return as-is on error
	}
	// btcutil CheckEncode(payload, version) appends the double-SHA256 checksum.
	// Tron addresses: version = decoded[0] (0x41), payload = decoded[1:].
	return btcbase58.CheckEncode(decoded[1:], decoded[0])
}
