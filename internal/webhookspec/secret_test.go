package webhookspec_test

import (
	"testing"

	"github.com/degoke/tronvent/internal/webhookspec"
)

func TestGenerateAndValidateSigningSecret(t *testing.T) {
	secret, err := webhookspec.GenerateSigningSecret()
	if err != nil {
		t.Fatal(err)
	}
	if err := webhookspec.ValidateSigningSecret(secret); err != nil {
		t.Fatal(err)
	}
	if err := webhookspec.ValidateSigningSecret("not-a-secret"); err == nil {
		t.Fatal("expected invalid secret error")
	}
}
