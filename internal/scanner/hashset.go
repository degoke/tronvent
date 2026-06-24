package scanner

import "sync"

// HashSet is a thread-safe set of strings used for O(1) address lookups.
type HashSet struct {
	mu   sync.RWMutex
	data map[string]struct{}
}

// NewHashSet initialises a HashSet pre-populated with the given addresses.
func NewHashSet(addresses []string) *HashSet {
	hs := &HashSet{data: make(map[string]struct{}, len(addresses))}
	for _, a := range addresses {
		hs.data[a] = struct{}{}
	}
	return hs
}

// Add inserts an address into the set.
func (hs *HashSet) Add(address string) {
	hs.mu.Lock()
	defer hs.mu.Unlock()
	hs.data[address] = struct{}{}
}

// Contains reports whether address is in the set.
func (hs *HashSet) Contains(address string) bool {
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	_, ok := hs.data[address]
	return ok
}

// Len returns the number of addresses in the set.
func (hs *HashSet) Len() int {
	hs.mu.RLock()
	defer hs.mu.RUnlock()
	return len(hs.data)
}
