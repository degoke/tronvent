package pgnotify

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	errInitialConnect = errors.New("initial connect failed")
	errDisconnected   = errors.New("listener disconnected")
	errResync         = errors.New("resync failed")
)

type fakeNotificationConn struct {
	listenErr error
	waitErr   error
}

func (c *fakeNotificationConn) Listen(context.Context, string) error {
	return c.listenErr
}

func (c *fakeNotificationConn) WaitForNotification(ctx context.Context) (*pgconn.Notification, error) {
	if c.waitErr != nil {
		return nil, c.waitErr
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (c *fakeNotificationConn) Close(context.Context) error {
	return nil
}

func TestListenerWaitForReadyOnInitialConnection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	listener := New("unused", []string{"addresses"}, nil, func(context.Context) error {
		t.Fatal("reconnect handler called for initial connection")
		return nil
	})
	listener.connect = func(context.Context, string) (notificationConn, error) {
		return &fakeNotificationConn{}, nil
	}

	done := make(chan struct{})
	go func() {
		listener.Run(ctx)
		close(done)
	}()

	readyCtx, cancelReady := context.WithTimeout(context.Background(), time.Second)
	defer cancelReady()
	if err := listener.WaitForReady(readyCtx); err != nil {
		t.Fatalf("WaitForReady() error = %v", err)
	}
	cancel()
	<-done
}

func TestListenerInitialConnectionFailure(t *testing.T) {
	listener := New("unused", []string{"addresses"}, nil, nil)
	listener.connect = func(context.Context, string) (notificationConn, error) {
		return nil, errInitialConnect
	}

	done := make(chan struct{})
	go func() {
		listener.Run(context.Background())
		close(done)
	}()

	readyCtx, cancelReady := context.WithTimeout(context.Background(), time.Second)
	defer cancelReady()
	if err := listener.WaitForReady(readyCtx); !errors.Is(err, errInitialConnect) {
		t.Fatalf("WaitForReady() error = %v, want %v", err, errInitialConnect)
	}
	<-done
}

func TestListenerReconnectResyncsBeforeResuming(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var connections atomic.Int32
	var resyncs atomic.Int32
	listener := New("unused", []string{"addresses"}, nil, func(context.Context) error {
		resyncs.Add(1)
		return nil
	})
	listener.reconnectDelay = 0
	listener.connect = func(context.Context, string) (notificationConn, error) {
		if connections.Add(1) == 1 {
			return &fakeNotificationConn{waitErr: errDisconnected}, nil
		}
		return &fakeNotificationConn{}, nil
	}

	done := make(chan struct{})
	go func() {
		listener.Run(ctx)
		close(done)
	}()

	readyCtx, cancelReady := context.WithTimeout(context.Background(), time.Second)
	defer cancelReady()
	if err := listener.WaitForReady(readyCtx); err != nil {
		t.Fatalf("WaitForReady() error = %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for resyncs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := resyncs.Load(); got != 1 {
		t.Fatalf("reconnect resync count = %d, want 1", got)
	}

	cancel()
	<-done
}

func TestListenerRetriesWhenReconnectResyncFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var connections atomic.Int32
	var resyncs atomic.Int32
	listener := New("unused", []string{"addresses"}, nil, func(context.Context) error {
		if resyncs.Add(1) == 1 {
			return errResync
		}
		return nil
	})
	listener.reconnectDelay = 0
	listener.connect = func(context.Context, string) (notificationConn, error) {
		connections.Add(1)
		return &fakeNotificationConn{waitErr: errDisconnected}, nil
	}

	done := make(chan struct{})
	go func() {
		listener.Run(ctx)
		close(done)
	}()

	readyCtx, cancelReady := context.WithTimeout(context.Background(), time.Second)
	defer cancelReady()
	if err := listener.WaitForReady(readyCtx); err != nil {
		t.Fatalf("WaitForReady() error = %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for resyncs.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := resyncs.Load(); got < 2 {
		t.Fatalf("reconnect resync count = %d, want at least 2", got)
	}

	cancel()
	<-done
}
