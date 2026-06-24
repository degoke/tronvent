package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/degoke/tronvent/internal/api"
	"github.com/degoke/tronvent/internal/config"
	internaldb "github.com/degoke/tronvent/internal/db"
	"github.com/degoke/tronvent/internal/metrics"
	"github.com/degoke/tronvent/internal/pgnotify"
	"github.com/degoke/tronvent/internal/scanner"
	"github.com/degoke/tronvent/internal/store"
	"github.com/degoke/tronvent/internal/webhook"
	"github.com/lmittmann/tint"
)

var defaultTrc20Contracts = []string{
	"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
}

func main() {
	level := slog.LevelInfo
	if v := strings.ToUpper(os.Getenv("LOG_LEVEL")); v != "" {
		if err := level.UnmarshalText([]byte(v)); err != nil {
			level = slog.LevelInfo
		}
	}

	logFormat := os.Getenv("LOG_FORMAT")
	isTTY := func() bool {
		fi, err := os.Stdout.Stat()
		return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
	}()
	useColor := logFormat == "text" || (logFormat != "json" && isTTY)

	var handler slog.Handler
	if useColor {
		handler = tint.NewHandler(os.Stdout, &tint.Options{
			Level:      level,
			TimeFormat: "15:04:05.000",
		})
	} else {
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	}
	slog.SetDefault(slog.New(handler))

	metrics.Register()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	db, err := internaldb.New(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	contracts := resolveTrc20Contracts(cfg)
	if err := db.BootstrapWatchedContracts(ctx, contracts); err != nil {
		slog.Error("bootstrap contracts", "err", err)
		os.Exit(1)
	}

	webhookCfg, err := db.BootstrapWebhookConfig(ctx, cfg.WebhookURL, cfg.WebhookSigningSecret)
	if err != nil {
		slog.Error("bootstrap webhook config", "err", err)
		os.Exit(1)
	}

	addressStore := store.NewAddressStore(db)
	contractStore := store.NewContractStore(db)
	webhookStore := store.NewWebhookConfigStore(db)

	if err := addressStore.Reload(ctx); err != nil {
		slog.Error("load watched addresses", "err", err)
		os.Exit(1)
	}
	if err := contractStore.Reload(ctx); err != nil {
		slog.Error("load watched contracts", "err", err)
		os.Exit(1)
	}
	if err := webhookStore.Reload(ctx); err != nil {
		slog.Error("load webhook config", "err", err)
		os.Exit(1)
	}
	if webhookCfg != nil {
		webhookStore.Set(webhookCfg)
	}

	reloadAll := func(reason string) {
		if err := addressStore.Reload(ctx); err != nil {
			slog.Error("reload addresses", "reason", reason, "err", err)
		}
		if err := contractStore.Reload(ctx); err != nil {
			slog.Error("reload contracts", "reason", reason, "err", err)
		}
		if err := webhookStore.Reload(ctx); err != nil {
			slog.Error("reload webhook config", "reason", reason, "err", err)
		}
	}

	listener := pgnotify.New(db.Pool, []string{
		internaldb.NotifyAddressesChanged,
		internaldb.NotifyContractsChanged,
		internaldb.NotifyWebhookChanged,
	}, func(ctx context.Context, channel, payload string) {
		slog.Debug("pgnotify received", "channel", channel, "payload", payload)
		switch channel {
		case internaldb.NotifyAddressesChanged:
			if err := addressStore.Reload(ctx); err != nil {
				slog.Error("notify reload addresses", "err", err)
			}
		case internaldb.NotifyContractsChanged:
			if err := contractStore.Reload(ctx); err != nil {
				slog.Error("notify reload contracts", "err", err)
			}
		case internaldb.NotifyWebhookChanged:
			if err := webhookStore.Reload(ctx); err != nil {
				slog.Error("notify reload webhook", "err", err)
			}
		}
	})
	go listener.Run(ctx)

	resyncInterval := time.Duration(cfg.StateResyncIntervalSeconds) * time.Second
	if resyncInterval <= 0 {
		resyncInterval = 60 * time.Second
	}
	go func() {
		ticker := time.NewTicker(resyncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				reloadAll("periodic")
			}
		}
	}()

	apiSrv := api.New(cfg, db, addressStore, contractStore, webhookStore)
	apiSrv.Start()

	slog.Info(
		"scanner starting",
		"tronGridBaseUrl", cfg.TronGridBaseURL,
		"watchedAddresses", addressStore.Len(),
		"contracts", contractStore.List(),
		"pollIntervalMs", cfg.PollIntervalMs,
		"requiredConfs", cfg.RequiredConfs,
	)

	poller := scanner.NewPoller(cfg, db, db, addressStore, contractStore)

	runStartupReconcile(ctx, cfg, db, poller, contractStore)

	go poller.RunReconciler(ctx)
	go poller.Run(ctx)
	go webhook.NewWorker(cfg, db, webhookStore).Run(ctx)

	<-ctx.Done()
	slog.Info("shutdown signal received, draining...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = apiSrv.Shutdown(shutdownCtx)

	slog.Info("scanner stopped")
}

func runStartupReconcile(ctx context.Context, cfg *config.Config, db *internaldb.Client, poller *scanner.Poller, contracts *store.ContractStore) {
	latestBlock, err := poller.GetLatestBlockNumber(ctx)
	if err != nil {
		slog.Error("[RECONCILE] startup: failed to get latest block", "err", err)
		return
	}
	safeBlock := latestBlock - cfg.RequiredConfs
	if safeBlock <= 0 {
		return
	}

	type scopeGap struct {
		scope        string
		cursor       int64
		blocksBehind int64
	}
	var gaps []scopeGap
	totalBatches := 0

	scopes := append([]string{"TRX"}, contracts.List()...)
	for _, scope := range scopes {
		cursor, err := db.GetScannedBlock(ctx, scope)
		if err != nil {
			slog.Error("[RECONCILE] startup: failed to get cursor", "scope", scope, "err", err)
			continue
		}
		if cursor <= 0 || cursor >= safeBlock {
			continue
		}

		blocksBehind := safeBlock - cursor
		batchSize := cfg.ReconcileBatchSize
		if batchSize <= 0 {
			batchSize = 1000
		}
		if blocksBehind < batchSize {
			slog.Info(
				"[RECONCILE] startup: gap too small, letting poller handle it",
				"scope", scope, "cursor", cursor, "behind", blocksBehind, "safeBlock", safeBlock,
			)
			continue
		}

		gaps = append(gaps, scopeGap{scope, cursor, blocksBehind})

		enqueued := 0
		for start := cursor + 1; start <= safeBlock; start += batchSize {
			end := start + batchSize - 1
			if end > safeBlock {
				end = safeBlock
			}
			if err := db.EnqueueBlockRangeJob(ctx, "tron-reconcile", "block-range", start, end, 10); err != nil {
				slog.Error("[RECONCILE] startup: enqueue failed", "scope", scope, "from", start, "to", end, "err", err)
			} else {
				enqueued++
			}
		}

		if err := db.SetScannedBlock(ctx, scope, safeBlock); err != nil {
			slog.Error("[RECONCILE] startup: failed to advance cursor", "scope", scope, "err", err)
		} else {
			slog.Info(
				"[RECONCILE] startup: cursor advanced",
				"scope", scope, "safeBlock", safeBlock,
				"batches", enqueued, "blocks", blocksBehind,
			)
		}
		totalBatches += enqueued
	}

	if len(gaps) == 0 {
		slog.Info("[RECONCILE] startup: no gaps detected, all scopes up to date")
	} else {
		slog.Info(
			"[RECONCILE] startup: reconciliation enqueued",
			"scopes", len(gaps), "totalBatches", totalBatches,
			"safeBlock", safeBlock, "latestBlock", latestBlock,
		)
		for _, g := range gaps {
			slog.Info(
				"[RECONCILE] startup: gap",
				"scope", g.scope, "cursor", g.cursor,
				"behind", g.blocksBehind,
			)
		}
	}
}

func resolveTrc20Contracts(cfg *config.Config) []string {
	if len(cfg.Trc20Contracts) > 0 {
		return cfg.Trc20Contracts
	}
	return defaultTrc20Contracts
}
