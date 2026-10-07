package scanner

import (
	"testing"

	"github.com/degoke/tronvent/internal/webhookpayload"
)

func TestMatchedTransferEvents_ReceivedOnly(t *testing.T) {
	watched := "TRecvXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	p := &Poller{addresses: NewHashSet([]string{watched})}
	events := p.matchedTransferEvents("TRX", "hash", "TSenderXXX", watched, "1", "", 1, 2)
	if len(events) != 1 || events[0].Direction != webhookpayload.DirectionReceived {
		t.Fatalf("expected one received event, got %+v", events)
	}
}

func TestMatchedTransferEvents_BroadcastedOnly(t *testing.T) {
	watched := "TSendXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	p := &Poller{addresses: NewHashSet([]string{watched})}
	events := p.matchedTransferEvents("TRX", "hash", watched, "TRecvXXX", "1", "", 1, 2)
	if len(events) != 1 || events[0].Direction != webhookpayload.DirectionBroadcasted {
		t.Fatalf("expected one broadcasted event, got %+v", events)
	}
}

func TestMatchedTransferEvents_SelfTransferBothDirections(t *testing.T) {
	watched := "TSelfXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"
	p := &Poller{addresses: NewHashSet([]string{watched})}
	events := p.matchedTransferEvents("TRX", "hash", watched, watched, "1", "", 1, 2)
	if len(events) != 2 {
		t.Fatalf("expected two events, got %d", len(events))
	}
}
