package pgnotify

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler is called when a Postgres NOTIFY is received on a channel.
type Handler func(ctx context.Context, channel, payload string)

// Listener subscribes to Postgres NOTIFY channels and dispatches handlers.
type Listener struct {
	pool     *pgxpool.Pool
	channels []string
	handler  Handler
}

// New creates a Listener for the given channels.
func New(pool *pgxpool.Pool, channels []string, handler Handler) *Listener {
	return &Listener{pool: pool, channels: channels, handler: handler}
}

// Run listens until ctx is cancelled. Reconnects on connection loss.
func (l *Listener) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if err := l.listenOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("pgnotify listener disconnected, reconnecting", "err", err)
			select {
			case <-time.After(2 * time.Second):
			case <-ctx.Done():
				return
			}
		}
	}
}

func (l *Listener) listenOnce(ctx context.Context) error {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	for _, ch := range l.channels {
		if _, err := conn.Exec(ctx, "LISTEN "+ch); err != nil {
			return err
		}
	}
	slog.Info("pgnotify listening", "channels", l.channels)

	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		if n != nil {
			l.handler(ctx, n.Channel, n.Payload)
		}
	}
}
