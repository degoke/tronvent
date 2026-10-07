package scanner

import (
	"context"
	"errors"
	"testing"

	"github.com/degoke/tronvent/internal/webhookpayload"
)

type activeAddressDB struct {
	stubDB
	active map[string]bool
	err    error
	calls  int
}

func (d *activeAddressDB) IsWatchedAddressActive(_ context.Context, address string) (bool, error) {
	d.calls++
	if d.err != nil {
		return false, d.err
	}
	if d.active == nil {
		return true, nil
	}
	return d.active[address], nil
}

func (d *activeAddressDB) ActiveWatchedAddresses(_ context.Context, addresses []string) (map[string]bool, error) {
	if d.err != nil {
		return nil, d.err
	}
	out := make(map[string]bool, len(addresses))
	for _, a := range addresses {
		if d.active == nil {
			out[a] = true
		} else {
			out[a] = d.active[a]
		}
	}
	return out, nil
}

func testPoller(addresses AddressSet, db *activeAddressDB) *Poller {
	if db == nil {
		db = &activeAddressDB{}
	}
	return &Poller{addresses: addresses, db: db}
}

func TestMatchedTransferEvents_ReceivedOnly(t *testing.T) {
	watched := "TRecvXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	db := &activeAddressDB{}
	p := testPoller(NewHashSet([]string{watched}), db)
	confirm := NewAddressConfirm(db)
	events := p.matchedTransferEvents(context.Background(), confirm, "TRX", "hash", "TSenderXXX", watched, "1", "", 1, 2)
	if len(events) != 1 || events[0].Direction != webhookpayload.DirectionReceived {
		t.Fatalf("expected one received event, got %+v", events)
	}
}

func TestMatchedTransferEvents_BroadcastedOnly(t *testing.T) {
	watched := "TSendXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	db := &activeAddressDB{}
	p := testPoller(NewHashSet([]string{watched}), db)
	confirm := NewAddressConfirm(db)
	events := p.matchedTransferEvents(context.Background(), confirm, "TRX", "hash", watched, "TRecvXXX", "1", "", 1, 2)
	if len(events) != 1 || events[0].Direction != webhookpayload.DirectionBroadcasted {
		t.Fatalf("expected one broadcasted event, got %+v", events)
	}
}

func TestMatchedTransferEvents_SelfTransferBothDirections(t *testing.T) {
	watched := "TSelfXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	db := &activeAddressDB{}
	p := testPoller(NewHashSet([]string{watched}), db)
	confirm := NewAddressConfirm(db)
	confirm.Prefetch(context.Background(), []string{watched})
	events := p.matchedTransferEvents(context.Background(), confirm, "TRX", "hash", watched, watched, "1", "", 1, 2)
	if len(events) != 2 {
		t.Fatalf("expected two events, got %d", len(events))
	}
}

func TestMatchedTransferEvents_InactiveInPostgresSkipped(t *testing.T) {
	watched := "TInactiveXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	db := &activeAddressDB{active: map[string]bool{watched: false}}
	p := testPoller(NewHashSet([]string{watched}), db)
	confirm := NewAddressConfirm(db)
	confirm.Prefetch(context.Background(), []string{watched})
	events := p.matchedTransferEvents(context.Background(), confirm, "TRX", "hash", "TSenderXXX", watched, "1", "", 1, 2)
	if len(events) != 0 {
		t.Fatalf("expected no events when postgres says inactive, got %+v", events)
	}
}

func TestMatchedTransferEvents_BloomFalsePositiveFiltered(t *testing.T) {
	notWatched := "TFalsePosXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	db := &activeAddressDB{active: map[string]bool{notWatched: false}}
	p := testPoller(NewHashSet(nil), db)
	p.addresses = NewHashSet([]string{notWatched})
	confirm := NewAddressConfirm(db)
	confirm.Prefetch(context.Background(), []string{notWatched})
	events := p.matchedTransferEvents(context.Background(), confirm, "TRX", "hash", "TSenderXXX", notWatched, "1", "", 1, 2)
	if len(events) != 0 {
		t.Fatalf("expected no events when address not active in postgres, got %+v", events)
	}
}

func TestMatchedTransferEvents_ConfirmErrorFailOpen(t *testing.T) {
	watched := "TFailOpenXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	db := &activeAddressDB{err: errors.New("connection refused")}
	p := testPoller(NewHashSet([]string{watched}), db)
	confirm := NewAddressConfirm(db)
	events := p.matchedTransferEvents(context.Background(), confirm, "TRX", "hash", "TSenderXXX", watched, "1", "", 1, 2)
	if len(events) != 1 {
		t.Fatalf("expected fail-open enqueue on confirm error, got %+v", events)
	}
}
