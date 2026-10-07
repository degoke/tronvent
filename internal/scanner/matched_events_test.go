package scanner

import (
	"context"
	"testing"

	"github.com/degoke/tronvent/internal/webhookpayload"
)

type activeAddressDB struct {
	stubDB
	active map[string]bool
}

func (d *activeAddressDB) IsWatchedAddressActive(_ context.Context, address string) (bool, error) {
	if d.active == nil {
		return true, nil
	}
	return d.active[address], nil
}

func testPoller(addresses AddressSet, db *activeAddressDB) *Poller {
	if db == nil {
		db = &activeAddressDB{}
	}
	return &Poller{addresses: addresses, db: db}
}

func TestMatchedTransferEvents_ReceivedOnly(t *testing.T) {
	watched := "TRecvXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	p := testPoller(NewHashSet([]string{watched}), nil)
	events := p.matchedTransferEvents(context.Background(), "TRX", "hash", "TSenderXXX", watched, "1", "", 1, 2)
	if len(events) != 1 || events[0].Direction != webhookpayload.DirectionReceived {
		t.Fatalf("expected one received event, got %+v", events)
	}
}

func TestMatchedTransferEvents_BroadcastedOnly(t *testing.T) {
	watched := "TSendXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	p := testPoller(NewHashSet([]string{watched}), nil)
	events := p.matchedTransferEvents(context.Background(), "TRX", "hash", watched, "TRecvXXX", "1", "", 1, 2)
	if len(events) != 1 || events[0].Direction != webhookpayload.DirectionBroadcasted {
		t.Fatalf("expected one broadcasted event, got %+v", events)
	}
}

func TestMatchedTransferEvents_SelfTransferBothDirections(t *testing.T) {
	watched := "TSelfXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	p := testPoller(NewHashSet([]string{watched}), nil)
	events := p.matchedTransferEvents(context.Background(), "TRX", "hash", watched, watched, "1", "", 1, 2)
	if len(events) != 2 {
		t.Fatalf("expected two events, got %d", len(events))
	}
}

func TestMatchedTransferEvents_InactiveInPostgresSkipped(t *testing.T) {
	watched := "TInactiveXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	p := testPoller(NewHashSet([]string{watched}), &activeAddressDB{
		active: map[string]bool{watched: false},
	})
	events := p.matchedTransferEvents(context.Background(), "TRX", "hash", "TSenderXXX", watched, "1", "", 1, 2)
	if len(events) != 0 {
		t.Fatalf("expected no events when postgres says inactive, got %+v", events)
	}
}

func TestMatchedTransferEvents_BloomFalsePositiveFiltered(t *testing.T) {
	notWatched := "TFalsePosXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	p := testPoller(NewHashSet(nil), &activeAddressDB{
		active: map[string]bool{notWatched: false},
	})
	// Simulate bloom false positive by putting address only in the set used for Contains.
	p.addresses = NewHashSet([]string{notWatched})
	events := p.matchedTransferEvents(context.Background(), "TRX", "hash", "TSenderXXX", notWatched, "1", "", 1, 2)
	if len(events) != 0 {
		t.Fatalf("expected no events when address not active in postgres, got %+v", events)
	}
}
