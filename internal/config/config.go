package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Config holds all configuration loaded from environment variables.
type Config struct {
	// Database
	DatabaseURL string

	// TronGrid
	TronGridBaseURL string
	TronGridAPIKey  string // TRONGRID_API_KEY_SCANNER — dedicated key for the scanner service

	// Polling
	PollIntervalMs         int64
	StartBlock             int64 // 0 = resume from last scanned block
	RequiredConfs          int64 // minimum confirmations before pushing to queue
	Trc20EventConfs        int64 // additional block lag for TRC-20 event scans (node event-index lag)
	Trc20CursorRetain      int64
	Trc20EventRetries      int
	Trc20EventRetryDelayMs int64
	FetchConcurrency       int
	HTTPTimeoutSeconds     int64

	// HTTP server (health + admin API)
	HealthPort string

	// ReconcileBatchSize is the number of blocks per reconcile batch when
	// backfilling historical blocks at startup. Default 1000.
	ReconcileBatchSize int64

	// Trc20Contracts seeds scanner_watched_contracts when the table is empty.
	Trc20Contracts []string

	// Webhook defaults (bootstrap only when no DB row exists)
	WebhookURL                string
	WebhookSigningSecret      string
	WebhookMaxAttempts        int
	WebhookPollIntervalMs     int64
	WebhookHTTPTimeoutSeconds int64

	// Admin API auth
	AdminAPIToken string

	// StateResyncIntervalSeconds is the periodic full reload safety net for LISTEN/NOTIFY.
	StateResyncIntervalSeconds int64
}

// sanitizeDSN percent-encodes the password in a postgres:// URL that contains
// characters invalid in the userinfo component.
func sanitizeDSN(raw string) string {
	schemeEnd := strings.Index(raw, "://")
	if schemeEnd < 0 {
		return raw
	}
	at := strings.LastIndex(raw, "@")
	if at < 0 || at < schemeEnd {
		return raw
	}

	userinfo := raw[schemeEnd+3 : at]
	colon := strings.Index(userinfo, ":")
	if colon < 0 {
		return raw
	}

	pass := userinfo[colon+1:]
	if decoded, err := url.QueryUnescape(pass); err == nil {
		pass = decoded
	}
	encoded := url.QueryEscape(pass)
	encoded = strings.ReplaceAll(encoded, "+", "%20")

	return raw[:schemeEnd+3] + userinfo[:colon+1] + encoded + raw[at:]
}

// Load reads environment variables and returns a populated Config.
func Load() (cfg *Config, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("config: %v", r)
		}
	}()

	c := &Config{
		DatabaseURL:                sanitizeDSN(mustEnv("DATABASE_URL")),
		TronGridBaseURL:            envOrDefault("TRONGRID_BASE_URL", "https://api.trongrid.io"),
		TronGridAPIKey:             mustEnv("TRONGRID_API_KEY_SCANNER"),
		PollIntervalMs:             envInt64OrDefault("TRON_POLL_INTERVAL_MS", 3000),
		StartBlock:                 envInt64OrDefault("TRON_START_BLOCK", 0),
		RequiredConfs:              envInt64OrDefault("TRON_REQUIRED_CONFIRMATIONS", 20),
		Trc20EventConfs:            envInt64OrDefault("TRON_TRC20_EVENT_CONFS", 10),
		Trc20CursorRetain:          envInt64OrDefault("TRON_TRC20_CURSOR_RETAIN", 50),
		Trc20EventRetries:          int(envInt64OrDefault("TRON_TRC20_EVENT_RETRIES", 3)),
		Trc20EventRetryDelayMs:     envInt64OrDefault("TRON_TRC20_EVENT_RETRY_DELAY_MS", 2000),
		HealthPort:                 envOrDefault("HEALTH_PORT", "8080"),
		FetchConcurrency:           int(envInt64OrDefault("TRON_FETCH_CONCURRENCY", 5)),
		HTTPTimeoutSeconds:         envInt64OrDefault("TRON_HTTP_TIMEOUT_SECONDS", 60),
		ReconcileBatchSize:         envInt64OrDefault("TRON_RECONCILE_BATCH_SIZE", 1000),
		WebhookURL:                 os.Getenv("WEBHOOK_URL"),
		WebhookSigningSecret:       os.Getenv("WEBHOOK_SIGNING_SECRET"),
		WebhookMaxAttempts:         int(envInt64OrDefault("WEBHOOK_MAX_ATTEMPTS", 8)),
		WebhookPollIntervalMs:      envInt64OrDefault("WEBHOOK_POLL_INTERVAL_MS", 1000),
		WebhookHTTPTimeoutSeconds:  envInt64OrDefault("WEBHOOK_HTTP_TIMEOUT_SECONDS", 30),
		AdminAPIToken:              os.Getenv("ADMIN_API_TOKEN"),
		StateResyncIntervalSeconds: envInt64OrDefault("STATE_RESYNC_INTERVAL_SECONDS", 60),
	}
	if raw := os.Getenv("TRON_TRC20_CONTRACTS"); raw != "" {
		for _, s := range strings.Split(raw, ",") {
			if t := strings.TrimSpace(s); t != "" {
				c.Trc20Contracts = append(c.Trc20Contracts, t)
			}
		}
	}
	return c, nil
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		panic(fmt.Sprintf("required environment variable %q is not set", key))
	}
	return v
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt64OrDefault(key string, def int64) int64 {
	s := os.Getenv(key)
	if s == "" {
		return def
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return v
}
