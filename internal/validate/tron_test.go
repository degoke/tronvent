package validate_test

import (
	"testing"

	"github.com/degoke/tronvent/internal/validate"
)

func TestTronAddress(t *testing.T) {
	if !validate.TronAddress("TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t") {
		t.Fatal("expected valid USDT contract address")
	}
	if validate.TronAddress("not-an-address") {
		t.Fatal("expected invalid address")
	}
	if validate.TronAddress("") {
		t.Fatal("expected empty to be invalid")
	}
}
