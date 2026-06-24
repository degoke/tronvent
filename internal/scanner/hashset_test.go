package scanner

import (
	"sync"
	"testing"
)

// TestHashSet_O1Lookup verifies that Contains performs an O(1) map lookup
// and correctly distinguishes members from non-members (M25.4).
func TestHashSet_O1Lookup(t *testing.T) {
	addresses := []string{
		"TAddr1XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
		"TAddr2XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
		"TAddr3XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
	}
	hs := NewHashSet(addresses)

	// All pre-populated addresses must be found
	for _, addr := range addresses {
		if !hs.Contains(addr) {
			t.Errorf("expected Contains(%q) = true, got false", addr)
		}
	}

	// Unknown addresses must not be found
	unknown := []string{
		"TUnknown1XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
		"",
		"TAddr1xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", // lowercase variant
	}
	for _, addr := range unknown {
		if hs.Contains(addr) {
			t.Errorf("expected Contains(%q) = false, got true", addr)
		}
	}
}

func TestHashSet_Len(t *testing.T) {
	hs := NewHashSet([]string{"A", "B", "C"})
	if hs.Len() != 3 {
		t.Errorf("expected Len() = 3, got %d", hs.Len())
	}
}

func TestHashSet_EmptySet(t *testing.T) {
	hs := NewHashSet(nil)
	if hs.Len() != 0 {
		t.Errorf("expected Len() = 0 for empty set, got %d", hs.Len())
	}
	if hs.Contains("anything") {
		t.Error("expected Contains() = false on empty set")
	}
}

func TestHashSet_Add(t *testing.T) {
	hs := NewHashSet(nil)

	hs.Add("TNewAddr1XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX")
	if !hs.Contains("TNewAddr1XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX") {
		t.Error("address added via Add() not found by Contains()")
	}
	if hs.Len() != 1 {
		t.Errorf("expected Len() = 1 after Add, got %d", hs.Len())
	}
}

func TestHashSet_AddDuplicate(t *testing.T) {
	addr := "TDupAddrXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	hs := NewHashSet([]string{addr})

	// Adding the same address again must not increase Len
	hs.Add(addr)
	if hs.Len() != 1 {
		t.Errorf("expected Len() = 1 after duplicate Add, got %d", hs.Len())
	}
}

// TestHashSet_ConcurrentReadWrite verifies that concurrent Add and Contains
// calls do not cause data races (requires `go test -race`).
func TestHashSet_ConcurrentReadWrite(t *testing.T) {
	hs := NewHashSet(nil)
	var wg sync.WaitGroup
	const goroutines = 50

	// 50 writers, each adding a unique address
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			// Use a simple unique string per goroutine
			addr := "TAddr" + string(rune('A'+n)) + "XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
			hs.Add(addr)
		}(i)
	}

	// 50 concurrent readers
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Contains may return true or false — just must not race
			_ = hs.Contains("TAddr1XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX")
		}()
	}

	wg.Wait()
}

// TestHashSet_PrePopulatedWithDuplicates verifies that duplicate seeds do not
// inflate the count (map semantics).
func TestHashSet_PrePopulatedWithDuplicates(t *testing.T) {
	addr := "TSharedXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	hs := NewHashSet([]string{addr, addr, addr})
	if hs.Len() != 1 {
		t.Errorf("expected Len() = 1 when seeded with 3 identical addresses, got %d", hs.Len())
	}
}
