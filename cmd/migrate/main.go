package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/degoke/tronvent/internal/config"
	"github.com/degoke/tronvent/internal/migrate"
	"github.com/degoke/tronvent/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	dsn, err := config.DatabaseURL()
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	connectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(connectCtx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: connect: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := pool.Ping(connectCtx); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: ping: %v\n", err)
		os.Exit(1)
	}

	if err := migrate.Up(ctx, pool, migrations.FS); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("migrations complete")
}
