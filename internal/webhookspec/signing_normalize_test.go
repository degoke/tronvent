package webhookspec_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/degoke/tronvent/internal/webhookspec"
)

func TestNormalizeSigningKeyLegacyPlaintext(t *testing.T) {
	legacy := "this-is-a-long-legacy-secret-key!!"
	normalized, err := webhookspec.NormalizeSigningKey(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := webhookspec.ValidateSigningKey(normalized); err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(normalized, "whsec_"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != legacy {
		t.Fatalf("normalized key bytes must match legacy plaintext")
	}
}

func TestNormalizeSigningKeyPreservesWhsec(t *testing.T) {
	secret, err := webhookspec.GenerateSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	out, err := webhookspec.NormalizeSigningKey(secret)
	if err != nil || out != secret {
		t.Fatalf("expected unchanged whsec, got %q err=%v", out, err)
	}
}

func TestPrepareSigningKeyForDeliveryKeepsShortMigratedWhsec(t *testing.T) {
	short := "this-is-a-long-legacy-secret-key!!"
	normalized, err := webhookspec.NormalizeSigningKey(short)
	if err != nil {
		t.Fatal(err)
	}
	out, err := webhookspec.PrepareSigningKeyForDelivery(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if out != normalized {
		t.Fatalf("expected stored key unchanged, got %q", out)
	}
}

func TestNormalizeSigningKeyRejectsShortLegacy(t *testing.T) {
	_, err := webhookspec.NormalizeSigningKey("short")
	if err == nil {
		t.Fatal("expected error for legacy secret shorter than whsec minimum")
	}
}
