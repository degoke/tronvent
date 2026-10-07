package webhookspec

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// PrepareSigningKeyForDelivery normalizes keys for reads and delivery. Migrated whsec_/whsk_
// values that are shorter than the Standard Webhooks minimum are kept as stored so rotation still works.
func PrepareSigningKeyForDelivery(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", nil
	}
	normalized, err := NormalizeSigningKey(key)
	if err == nil {
		return normalized, nil
	}
	if strings.HasPrefix(key, secretPrefix) || strings.HasPrefix(key, privateKeyPrefix) {
		return key, nil
	}
	return "", err
}

// NormalizeSigningKey converts legacy plaintext HMAC secrets to whsec_ form (same key bytes as pre-Standard Webhooks).
func NormalizeSigningKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", fmt.Errorf("signing key is required")
	}
	if err := ValidateSigningKey(key); err == nil {
		return key, nil
	}
	if strings.HasPrefix(key, secretPrefix) || strings.HasPrefix(key, privateKeyPrefix) {
		return "", ValidateSigningKey(key)
	}
	normalized := secretPrefix + base64.StdEncoding.EncodeToString([]byte(key))
	if err := ValidateSigningKey(normalized); err != nil {
		return "", fmt.Errorf("legacy signing secret: %w (use a longer secret or rotate to whsk_/whsec_)", err)
	}
	return normalized, nil
}
