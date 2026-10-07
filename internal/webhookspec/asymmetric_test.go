package webhookspec_test

import (
	"strings"
	"testing"
	"time"

	"github.com/degoke/tronvent/internal/webhookspec"
)

func TestGenerateSigningKeyPairAndSign(t *testing.T) {
	priv, pub, err := webhookspec.GenerateSigningKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := webhookspec.ValidateSigningKey(priv); err != nil {
		t.Fatal(err)
	}
	if pub == "" || pub[:5] != "whpk_" {
		t.Fatalf("public key: %q", pub)
	}
	headers, err := webhookspec.BuildHeaders("msg-id", time.Now().Unix(), []byte(`{"ok":true}`), []string{priv})
	if err != nil {
		t.Fatal(err)
	}
	sig := headers["webhook-signature"]
	if !strings.Contains(sig, "v1a,") {
		t.Fatalf("expected v1a signature, got %q", sig)
	}
}
