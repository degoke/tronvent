package scanner

// AddressSet is the interface satisfied by both HashSet (exact) and
// BloomFilter (probabilistic). Poller depends only on this interface so
// tests can inject HashSet while production uses BloomFilter.
type AddressSet interface {
	Add(address string)
	Contains(address string) bool
	Len() int
}
