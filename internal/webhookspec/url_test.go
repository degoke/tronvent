package webhookspec_test

import (
	"testing"

	"github.com/degoke/tronvent/internal/webhookspec"
)

func TestValidateWebhookURLRequiresHTTPS(t *testing.T) {
	policy := webhookspec.URLPolicy{}
	if err := policy.ValidateWebhookURL("http://example.com/hook"); err == nil {
		t.Fatal("expected https requirement")
	}
	if err := policy.ValidateWebhookURL("https://example.com/hook"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateWebhookURLRejectsPrivateHosts(t *testing.T) {
	policy := webhookspec.URLPolicy{}
	if err := policy.ValidateWebhookURL("https://127.0.0.1/hook"); err == nil {
		t.Fatal("expected private host rejection")
	}
}
