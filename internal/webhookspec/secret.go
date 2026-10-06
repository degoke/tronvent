package webhookspec

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
)

const (
	secretPrefix   = "whsec_"
	minSecretBytes = 24
	maxSecretBytes = 64
)

// GenerateSigningSecret returns a new Standard Webhooks symmetric secret (whsec_ + base64).
func GenerateSigningSecret() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("generate signing secret: %w", err)
	}
	return secretPrefix + base64.StdEncoding.EncodeToString(key), nil
}

// ValidateSigningSecret ensures the secret uses whsec_ serialization with valid key length.
func ValidateSigningSecret(secret string) error {
	_, err := decodeSigningSecret(secret)
	return err
}

func decodeSigningSecret(secret string) ([]byte, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, fmt.Errorf("signing secret is required")
	}
	if !strings.HasPrefix(secret, secretPrefix) {
		return nil, fmt.Errorf("signing secret must start with %q", secretPrefix)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, secretPrefix))
	if err != nil {
		return nil, fmt.Errorf("signing secret must be base64 after %q: %w", secretPrefix, err)
	}
	if len(raw) < minSecretBytes || len(raw) > maxSecretBytes {
		return nil, fmt.Errorf("signing secret key must be %d-%d bytes, got %d", minSecretBytes, maxSecretBytes, len(raw))
	}
	return raw, nil
}
