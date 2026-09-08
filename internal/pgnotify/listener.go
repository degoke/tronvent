package pgnotify

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Handler is called when a Postgres NOTIFY is received on a channel.
type Handler func(ctx context.Context, channel, payload string)

// ReconnectHandler is called after a listener reconnects successfully. The
// listener does not resume notifications until the handler succeeds.
type ReconnectHandler func(ctx context.Context) error

type notificationConn interface {
	Listen(ctx context.Context, channel string) error
	WaitForNotification(ctx context.Context) (*pgconn.Notification, error)
	Close(ctx context.Context) error
}

type pgxNotificationConn struct {
	conn *pgx.Conn
}

func (c pgxNotificationConn) Listen(ctx context.Context, channel string) error {
	_, err := c.conn.Exec(ctx, "LISTEN "+channel)
	return err
}

func (c pgxNotificationConn) WaitForNotification(ctx context.Context) (*pgconn.Notification, error) {
	return c.conn.WaitForNotification(ctx)
}

func (c pgxNotificationConn) Close(ctx context.Context) error {
	return c.conn.Close(ctx)
}

// Listener subscribes to Postgres NOTIFY channels and dispatches handlers.
type Listener struct {
	dsn            string
	channels       []string
	handler        Handler
	onReconnect    ReconnectHandler
	ready          chan struct{}
	initialErr     chan error
	readyOnce      sync.Once
	connect        func(context.Context, string) (notificationConn, error)
	reconnectDelay time.Duration
}

// New creates a Listener for the given channels using a dedicated database connection.
func New(dsn string, channels []string, handler Handler, onReconnect ReconnectHandler) *Listener {
	return &Listener{
		dsn:         dsn,
		channels:    channels,
		handler:     handler,
		onReconnect: onReconnect,
		ready:       make(chan struct{}),
		initialErr:  make(chan error, 1),
		connect: func(ctx context.Context, dsn string) (notificationConn, error) {
			conn, err := pgx.Connect(ctx, dsn)
			if err != nil {
				return nil, err
			}
			return pgxNotificationConn{conn: conn}, nil
		},
		reconnectDelay: 2 * time.Second,
	}
}

// WaitForReady waits for the initial LISTEN subscription to be established.
func (l *Listener) WaitForReady(ctx context.Context) error {
	select {
	case <-l.ready:
		return nil
	case err := <-l.initialErr:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Run listens until ctx is cancelled. Reconnects on connection loss.
func (l *Listener) Run(ctx context.Context) {
	connected := false
	for {
		if ctx.Err() != nil {
			return
		}
		established, err := l.listenOnce(ctx, connected)
		if established {
			connected = true
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !connected {
				select {
				case l.initialErr <- err:
				default:
				}
				slog.Error("pgnotify initial connection failed", "err", err)
				return
			}
			slog.Warn("pgnotify listener disconnected, reconnecting", "err", err)
			select {
			case <-time.After(l.reconnectDelay):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (l *Listener) listenOnce(ctx context.Context, reconnect bool) (bool, error) {
	conn, err := l.connect(ctx, l.dsn)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close(context.Background()) }()

	for _, ch := range l.channels {
		if err := conn.Listen(ctx, ch); err != nil {
			return false, err
		}
	}
	slog.Info("pgnotify listening", "channels", l.channels)
	if !reconnect {
		l.readyOnce.Do(func() { close(l.ready) })
	}
	if reconnect && l.onReconnect != nil {
		if err := l.onReconnect(ctx); err != nil {
			return true, fmt.Errorf("reconnect resync: %w", err)
		}
	}

	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return true, err
		}
		if n != nil {
			l.handler(ctx, n.Channel, n.Payload)
		}
	}
}
