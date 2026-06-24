package db_test

import (
	"testing"

	internaldb "github.com/degoke/tronvent/internal/db"
)

func TestValidateBlockRange(t *testing.T) {
	if err := internaldb.ValidateBlockRange(1, 10); err != nil {
		t.Fatalf("valid range: %v", err)
	}
	if err := internaldb.ValidateBlockRange(5, 5); err != nil {
		t.Fatalf("single block range: %v", err)
	}
	if err := internaldb.ValidateBlockRange(10, 1); err != internaldb.ErrInvalidBlockRange {
		t.Fatalf("expected ErrInvalidBlockRange, got %v", err)
	}
	if err := internaldb.ValidateBlockRange(0, 10); err != internaldb.ErrNonPositiveBlock {
		t.Fatalf("expected ErrNonPositiveBlock for from=0, got %v", err)
	}
	if err := internaldb.ValidateBlockRange(1, -1); err != internaldb.ErrNonPositiveBlock {
		t.Fatalf("expected ErrNonPositiveBlock for negative to, got %v", err)
	}
}
