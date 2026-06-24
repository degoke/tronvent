package scanner

import (
	"sync"

	"github.com/bits-and-blooms/bloom/v3"
)

// bloomM and bloomK are tuned for 1 million addresses at a 0.1% false-positive
// rate. Computed via bloom.EstimateParameters(1_000_000, 0.001) → m=14377588, k=10.
// At this rate the processor sees ~1 spurious event per 1000 scanner matches.
const (
	bloomM uint = 14_377_588
	bloomK uint = 10
)

// BloomFilter is a thread-safe probabilistic address-membership filter.
//
// Guarantees:
//   - No false negatives: a real address is never missed.
//   - Low false-positive rate (~0.1% at 1M addresses): an unknown address may
//     pass the filter and reach the processor, where it is tagged as
//     flag_reason='false_positive' in tron_transactions.
type BloomFilter struct {
	mu     sync.RWMutex
	filter *bloom.BloomFilter
	count  int
}

// NewBloomFilter returns an empty, ready-to-use BloomFilter.
func NewBloomFilter() *BloomFilter {
	return &BloomFilter{filter: bloom.New(bloomM, bloomK)}
}

// Add inserts address into the filter. Thread-safe.
func (b *BloomFilter) Add(address string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.filter.AddString(address)
	b.count++
}

// AddBatch inserts all addresses in a single lock acquisition.
func (b *BloomFilter) AddBatch(addresses []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, a := range addresses {
		b.filter.AddString(a)
	}
	b.count += len(addresses)
}

// Contains reports whether address is probably in the set. Never returns false
// for a real address; may return true for non-members (false positive). Thread-safe.
func (b *BloomFilter) Contains(address string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.filter.TestString(address)
}

// Len returns the number of Add/AddBatch calls made (not deduplicated).
func (b *BloomFilter) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.count
}
