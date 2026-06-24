package store_test

import (
	"context"
	"testing"

	"github.com/degoke/tronvent/internal/store"
)

type mockAddressLoader struct {
	addrs []string
}

func (m mockAddressLoader) ListActiveAddresses(_ context.Context) ([]string, error) {
	return m.addrs, nil
}

type mockContractLoader struct {
	contracts []string
}

func (m mockContractLoader) ListActiveContracts(_ context.Context) ([]string, error) {
	return m.contracts, nil
}

func TestAddressStoreReload(t *testing.T) {
	loader := mockAddressLoader{addrs: []string{
		"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
		"TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf",
	}}
	s := store.NewAddressStore(loader)
	if err := s.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 2 {
		t.Fatalf("expected 2 addresses, got %d", s.Len())
	}
	if !s.Contains("TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t") {
		t.Fatal("expected address in store")
	}
}

func TestContractStoreReloadAndAdd(t *testing.T) {
	contractLoader := mockContractLoader{contracts: []string{"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t"}}
	s := store.NewContractStore(contractLoader)
	if err := s.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 1 {
		t.Fatalf("expected 1 contract, got %d", s.Len())
	}
	s.Add("TXYZopYRdj2D9XRtbG411XZZ3kM5VkAeBf")
	if s.Len() != 2 {
		t.Fatalf("expected 2 contracts after add, got %d", s.Len())
	}
}
