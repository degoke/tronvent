package webhookspec

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

const (
	privateKeyPrefix = "whsk_"
	publicKeyPrefix  = "whpk_"
)

// GenerateSigningKeyPair creates a Standard Webhooks ed25519 key pair (preferred).
func GenerateSigningKeyPair() (privateKey string, publicKey string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate ed25519 key pair: %w", err)
	}
	// ed25519 private key seed is the first 32 bytes of the Go private key representation.
	seed := priv.Seed()
	return privateKeyPrefix + base64.StdEncoding.EncodeToString(seed),
		publicKeyPrefix + base64.StdEncoding.EncodeToString(pub),
		nil
}

func decodePrivateSigningKey(key string) (ed25519.PrivateKey, error) {
	key = strings.TrimSpace(key)
	if !strings.HasPrefix(key, privateKeyPrefix) {
		return nil, fmt.Errorf("private signing key must start with %q", privateKeyPrefix)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(key, privateKeyPrefix))
	if err != nil {
		return nil, fmt.Errorf("invalid private signing key: %w", err)
	}
	if l := len(raw); l != ed25519.SeedSize && l != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("invalid ed25519 private key length %d", l)
	}
	if len(raw) == ed25519.SeedSize {
		return ed25519.NewKeyFromSeed(raw), nil
	}
	return ed25519.PrivateKey(raw), nil
}

// ValidateSigningKey accepts Standard Webhooks symmetric (whsec_) or asymmetric private (whsk_) keys.
func ValidateSigningKey(key string) error {
	key = strings.TrimSpace(key)
	switch {
	case strings.HasPrefix(key, secretPrefix):
		return ValidateSigningSecret(key)
	case strings.HasPrefix(key, privateKeyPrefix):
		_, err := decodePrivateSigningKey(key)
		return err
	default:
		return fmt.Errorf("signing key must start with %q or %q", secretPrefix, privateKeyPrefix)
	}
}

func signAsymmetric(privateKey string, msgID string, ts time.Time, body []byte) (string, error) {
	priv, err := decodePrivateSigningKey(privateKey)
	if err != nil {
		return "", err
	}
	toSign := fmt.Sprintf("%s.%d.%s", msgID, ts.Unix(), body)
	sig := ed25519.Sign(priv, []byte(toSign))
	return "v1a," + base64.StdEncoding.EncodeToString(sig), nil
}

func signSymmetric(secret string, msgID string, ts time.Time, body []byte) (string, error) {
	if err := ValidateSigningSecret(secret); err != nil {
		return "", err
	}
	wh, err := newStandardWebhook(secret)
	if err != nil {
		return "", err
	}
	return wh.Sign(msgID, ts, body)
}

// PublicKeyFromPrivateKey derives the whpk_ public key from a whsk_ private key.
func PublicKeyFromPrivateKey(privateKey string) (string, error) {
	priv, err := decodePrivateSigningKey(privateKey)
	if err != nil {
		return "", err
	}
	pub := priv.Public().(ed25519.PublicKey)
	return publicKeyPrefix + base64.StdEncoding.EncodeToString(pub), nil
}

func signWithKey(key string, msgID string, ts time.Time, body []byte) (string, error) {
	key = strings.TrimSpace(key)
	if strings.HasPrefix(key, privateKeyPrefix) {
		return signAsymmetric(key, msgID, ts, body)
	}
	return signSymmetric(key, msgID, ts, body)
}
