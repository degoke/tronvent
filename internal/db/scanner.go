package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	NotifyAddressesChanged = "scanner_addresses_changed"
	NotifyContractsChanged = "scanner_contracts_changed"
	NotifyWebhookChanged   = "scanner_webhook_config_changed"
)

type WatchedAddress struct {
	ID        string
	Address   string
	Status    string
	Source    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type WatchedContract struct {
	ID              string
	ContractAddress string
	Status          string
	TokenSymbol     *string
	Source          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type WebhookConfig struct {
	WebhookURL    string
	SigningSecret string
	IsActive      bool
	Source        string
	UpdatedAt     time.Time
}

type WebhookEvent struct {
	ID             string
	EventType      string
	Scope          string
	TxHash         string
	BlockNumber    int64
	BlockTimestamp int64
	Payload        json.RawMessage
	DedupeKey      string
	Status         string
	AttemptCount   int
	NextAttemptAt  time.Time
}

type CursorRow struct {
	Scope        string
	HighestBlock int64
	UpdatedAt    time.Time
}

// ListActiveAddresses returns all active watched addresses.
func (c *Client) ListActiveAddresses(ctx context.Context) ([]string, error) {
	rows, err := c.Pool.Query(ctx, `
		SELECT address FROM scanner_watched_addresses
		WHERE status = 'active'
		ORDER BY address
	`)
	if err != nil {
		return nil, fmt.Errorf("ListActiveAddresses: %w", err)
	}
	defer rows.Close()

	var addrs []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return nil, fmt.Errorf("ListActiveAddresses scan: %w", err)
		}
		addrs = append(addrs, addr)
	}
	return addrs, rows.Err()
}

// ListAddresses returns watched addresses with optional status filter and cursor pagination.
func (c *Client) ListAddresses(ctx context.Context, status string, limit int, afterAddress string) ([]WatchedAddress, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := c.Pool.Query(ctx, `
		SELECT id::text, address, status, source, created_at, updated_at
		FROM scanner_watched_addresses
		WHERE ($1 = '' OR status = $1)
		  AND ($2 = '' OR address > $2)
		ORDER BY address
		LIMIT $3
	`, status, afterAddress, limit)
	if err != nil {
		return nil, fmt.Errorf("ListAddresses: %w", err)
	}
	defer rows.Close()

	return scanWatchedAddresses(rows)
}

// AddWatchedAddress inserts an address or returns the existing active row.
// Returns (row, created, error). Sends NOTIFY on insert or reactivation.
func (c *Client) AddWatchedAddress(ctx context.Context, address, source string) (WatchedAddress, bool, error) {
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return WatchedAddress{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var existing WatchedAddress
	err = tx.QueryRow(ctx, `
		SELECT id::text, address, status, source, created_at, updated_at
		FROM scanner_watched_addresses
		WHERE address = $1
	`, address).Scan(&existing.ID, &existing.Address, &existing.Status, &existing.Source, &existing.CreatedAt, &existing.UpdatedAt)
	if err == nil {
		if existing.Status == "active" {
			if err := tx.Commit(ctx); err != nil {
				return WatchedAddress{}, false, err
			}
			return existing, false, nil
		}
		err = tx.QueryRow(ctx, `
			UPDATE scanner_watched_addresses
			SET status = 'active', updated_at = now()
			WHERE address = $1
			RETURNING id::text, address, status, source, created_at, updated_at
		`, address).Scan(&existing.ID, &existing.Address, &existing.Status, &existing.Source, &existing.CreatedAt, &existing.UpdatedAt)
		if err != nil {
			return WatchedAddress{}, false, fmt.Errorf("reactivate address: %w", err)
		}
		if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyAddressesChanged, `{"reason":"reload"}`); err != nil {
			return WatchedAddress{}, false, fmt.Errorf("notify addresses: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return WatchedAddress{}, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return WatchedAddress{}, false, fmt.Errorf("lookup address: %w", err)
	}

	var row WatchedAddress
	err = tx.QueryRow(ctx, `
		INSERT INTO scanner_watched_addresses (address, source)
		VALUES ($1, $2)
		RETURNING id::text, address, status, source, created_at, updated_at
	`, address, source).Scan(&row.ID, &row.Address, &row.Status, &row.Source, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		return WatchedAddress{}, false, fmt.Errorf("insert address: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyAddressesChanged, `{"reason":"reload"}`); err != nil {
		return WatchedAddress{}, false, fmt.Errorf("notify addresses: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return WatchedAddress{}, false, err
	}
	return row, true, nil
}

var (
	ErrWatchedAddressNotFound  = errors.New("watched address not found")
	ErrWatchedContractNotFound = errors.New("watched contract not found")
)

// DeactivateWatchedAddress marks an address inactive and notifies listeners.
func (c *Client) DeactivateWatchedAddress(ctx context.Context, address string) (WatchedAddress, error) {
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return WatchedAddress{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var row WatchedAddress
	err = tx.QueryRow(ctx, `
		UPDATE scanner_watched_addresses
		SET status = 'inactive', updated_at = now()
		WHERE address = $1 AND status = 'active'
		RETURNING id::text, address, status, source, created_at, updated_at
	`, address).Scan(&row.ID, &row.Address, &row.Status, &row.Source, &row.CreatedAt, &row.UpdatedAt)
	if err == nil {
		if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyAddressesChanged, `{"reason":"reload"}`); err != nil {
			return WatchedAddress{}, fmt.Errorf("notify addresses: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return WatchedAddress{}, err
		}
		return row, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return WatchedAddress{}, fmt.Errorf("deactivate address: %w", err)
	}

	err = tx.QueryRow(ctx, `
		SELECT id::text, address, status, source, created_at, updated_at
		FROM scanner_watched_addresses WHERE address = $1
	`, address).Scan(&row.ID, &row.Address, &row.Status, &row.Source, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WatchedAddress{}, ErrWatchedAddressNotFound
	}
	if err != nil {
		return WatchedAddress{}, fmt.Errorf("lookup address: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return WatchedAddress{}, err
	}
	return row, nil
}

// ListActiveContracts returns active contract addresses.
func (c *Client) ListActiveContracts(ctx context.Context) ([]string, error) {
	rows, err := c.Pool.Query(ctx, `
		SELECT contract_address FROM scanner_watched_contracts
		WHERE status = 'active'
		ORDER BY contract_address
	`)
	if err != nil {
		return nil, fmt.Errorf("ListActiveContracts: %w", err)
	}
	defer rows.Close()

	var contracts []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return nil, fmt.Errorf("ListActiveContracts scan: %w", err)
		}
		contracts = append(contracts, addr)
	}
	return contracts, rows.Err()
}

// ListContracts returns watched contracts with optional status filter and cursor pagination.
func (c *Client) ListContracts(ctx context.Context, status string, limit int, afterContract string) ([]WatchedContract, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := c.Pool.Query(ctx, `
		SELECT id::text, contract_address, status, token_symbol, source, created_at, updated_at
		FROM scanner_watched_contracts
		WHERE ($1 = '' OR status = $1)
		  AND ($2 = '' OR contract_address > $2)
		ORDER BY contract_address
		LIMIT $3
	`, status, afterContract, limit)
	if err != nil {
		return nil, fmt.Errorf("ListContracts: %w", err)
	}
	defer rows.Close()

	var out []WatchedContract
	for rows.Next() {
		var row WatchedContract
		if err := rows.Scan(&row.ID, &row.ContractAddress, &row.Status, &row.TokenSymbol, &row.Source, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("ListContracts scan: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// AddWatchedContract inserts a contract or returns the existing active row.
func (c *Client) AddWatchedContract(ctx context.Context, contractAddress, tokenSymbol, source string) (WatchedContract, bool, error) {
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return WatchedContract{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var existing WatchedContract
	err = tx.QueryRow(ctx, `
		SELECT id::text, contract_address, status, token_symbol, source, created_at, updated_at
		FROM scanner_watched_contracts
		WHERE contract_address = $1
	`, contractAddress).Scan(&existing.ID, &existing.ContractAddress, &existing.Status, &existing.TokenSymbol, &existing.Source, &existing.CreatedAt, &existing.UpdatedAt)
	if err == nil {
		if existing.Status == "active" {
			if err := tx.Commit(ctx); err != nil {
				return WatchedContract{}, false, err
			}
			return existing, false, nil
		}
		err = tx.QueryRow(ctx, `
			UPDATE scanner_watched_contracts
			SET status = 'active', token_symbol = COALESCE(NULLIF($2, ''), token_symbol), updated_at = now()
			WHERE contract_address = $1
			RETURNING id::text, contract_address, status, token_symbol, source, created_at, updated_at
		`, contractAddress, tokenSymbol).Scan(&existing.ID, &existing.ContractAddress, &existing.Status, &existing.TokenSymbol, &existing.Source, &existing.CreatedAt, &existing.UpdatedAt)
		if err != nil {
			return WatchedContract{}, false, fmt.Errorf("reactivate contract: %w", err)
		}
		if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyContractsChanged, `{"reason":"reload"}`); err != nil {
			return WatchedContract{}, false, fmt.Errorf("notify contracts: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return WatchedContract{}, false, err
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return WatchedContract{}, false, fmt.Errorf("lookup contract: %w", err)
	}

	var sym *string
	if tokenSymbol != "" {
		sym = &tokenSymbol
	}
	var row WatchedContract
	err = tx.QueryRow(ctx, `
		INSERT INTO scanner_watched_contracts (contract_address, token_symbol, source)
		VALUES ($1, $2, $3)
		RETURNING id::text, contract_address, status, token_symbol, source, created_at, updated_at
	`, contractAddress, sym, source).Scan(&row.ID, &row.ContractAddress, &row.Status, &row.TokenSymbol, &row.Source, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		return WatchedContract{}, false, fmt.Errorf("insert contract: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyContractsChanged, `{"reason":"reload"}`); err != nil {
		return WatchedContract{}, false, fmt.Errorf("notify contracts: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return WatchedContract{}, false, err
	}
	return row, true, nil
}

// DeactivateWatchedContract marks a contract inactive and notifies listeners.
func (c *Client) DeactivateWatchedContract(ctx context.Context, contractAddress string) (WatchedContract, error) {
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return WatchedContract{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var row WatchedContract
	err = tx.QueryRow(ctx, `
		UPDATE scanner_watched_contracts
		SET status = 'inactive', updated_at = now()
		WHERE contract_address = $1 AND status = 'active'
		RETURNING id::text, contract_address, status, token_symbol, source, created_at, updated_at
	`, contractAddress).Scan(&row.ID, &row.ContractAddress, &row.Status, &row.TokenSymbol, &row.Source, &row.CreatedAt, &row.UpdatedAt)
	if err == nil {
		if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyContractsChanged, `{"reason":"reload"}`); err != nil {
			return WatchedContract{}, fmt.Errorf("notify contracts: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return WatchedContract{}, err
		}
		return row, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return WatchedContract{}, fmt.Errorf("deactivate contract: %w", err)
	}

	err = tx.QueryRow(ctx, `
		SELECT id::text, contract_address, status, token_symbol, source, created_at, updated_at
		FROM scanner_watched_contracts WHERE contract_address = $1
	`, contractAddress).Scan(&row.ID, &row.ContractAddress, &row.Status, &row.TokenSymbol, &row.Source, &row.CreatedAt, &row.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WatchedContract{}, ErrWatchedContractNotFound
	}
	if err != nil {
		return WatchedContract{}, fmt.Errorf("lookup contract: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return WatchedContract{}, err
	}
	return row, nil
}

// BootstrapWatchedContracts inserts env-default contracts when the table is empty.
func (c *Client) BootstrapWatchedContracts(ctx context.Context, contracts []string) error {
	var count int
	if err := c.Pool.QueryRow(ctx, `SELECT COUNT(1) FROM scanner_watched_contracts`).Scan(&count); err != nil {
		return fmt.Errorf("BootstrapWatchedContracts count: %w", err)
	}
	if count > 0 {
		return nil
	}
	for _, contract := range contracts {
		if _, _, err := c.AddWatchedContract(ctx, contract, "", "env"); err != nil {
			return err
		}
	}
	return nil
}

// GetScannedBlock returns the highest scanned block for a scope from scanner_cursors.
func (c *Client) GetScannedBlock(ctx context.Context, scope string) (int64, error) {
	var highest int64
	err := c.Pool.QueryRow(ctx, `
		SELECT highest_block FROM scanner_cursors WHERE scope = $1
	`, scope).Scan(&highest)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("GetScannedBlock(%s): %w", scope, err)
	}
	return highest, nil
}

// SetScannedBlock upserts the cursor for a scope.
func (c *Client) SetScannedBlock(ctx context.Context, scope string, blockNum int64) error {
	_, err := c.Pool.Exec(ctx, `
		INSERT INTO scanner_cursors (scope, highest_block, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (scope) DO UPDATE
		SET highest_block = GREATEST(scanner_cursors.highest_block, EXCLUDED.highest_block),
		    updated_at = now()
	`, scope, blockNum)
	if err != nil {
		return fmt.Errorf("SetScannedBlock(%s): %w", scope, err)
	}
	return nil
}

// ListCursors returns all scanner cursors.
func (c *Client) ListCursors(ctx context.Context) ([]CursorRow, error) {
	rows, err := c.Pool.Query(ctx, `
		SELECT scope, highest_block, updated_at FROM scanner_cursors ORDER BY scope
	`)
	if err != nil {
		return nil, fmt.Errorf("ListCursors: %w", err)
	}
	defer rows.Close()

	var out []CursorRow
	for rows.Next() {
		var row CursorRow
		if err := rows.Scan(&row.Scope, &row.HighestBlock, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("ListCursors scan: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// GetWebhookConfig loads the singleton webhook config row.
func (c *Client) GetWebhookConfig(ctx context.Context) (*WebhookConfig, error) {
	var cfg WebhookConfig
	err := c.Pool.QueryRow(ctx, `
		SELECT webhook_url, signing_secret, is_active, source, updated_at
		FROM scanner_webhook_config WHERE id = true
	`).Scan(&cfg.WebhookURL, &cfg.SigningSecret, &cfg.IsActive, &cfg.Source, &cfg.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetWebhookConfig: %w", err)
	}
	return &cfg, nil
}

// BootstrapWebhookConfig inserts env defaults when no row exists.
func (c *Client) BootstrapWebhookConfig(ctx context.Context, webhookURL, signingSecret string) (*WebhookConfig, error) {
	existing, err := c.GetWebhookConfig(ctx)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	if webhookURL == "" || signingSecret == "" {
		return nil, nil
	}
	return c.UpsertWebhookConfig(ctx, webhookURL, signingSecret, true, "env")
}

// UpsertWebhookConfig updates the singleton webhook config and notifies listeners.
func (c *Client) UpsertWebhookConfig(ctx context.Context, webhookURL, signingSecret string, isActive bool, source string) (*WebhookConfig, error) {
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var cfg WebhookConfig
	err = tx.QueryRow(ctx, `
		INSERT INTO scanner_webhook_config (id, webhook_url, signing_secret, is_active, source, updated_at)
		VALUES (true, $1, $2, $3, $4, now())
		ON CONFLICT (id) DO UPDATE SET
			webhook_url = EXCLUDED.webhook_url,
			signing_secret = EXCLUDED.signing_secret,
			is_active = EXCLUDED.is_active,
			source = EXCLUDED.source,
			updated_at = now()
		RETURNING webhook_url, signing_secret, is_active, source, updated_at
	`, webhookURL, signingSecret, isActive, source).Scan(&cfg.WebhookURL, &cfg.SigningSecret, &cfg.IsActive, &cfg.Source, &cfg.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("UpsertWebhookConfig: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyWebhookChanged, `{"reason":"reload"}`); err != nil {
		return nil, fmt.Errorf("notify webhook: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// EnqueueWebhookEvent inserts a matched event into the outbox (deduplicated).
// The event id is generated here and injected into the payload before storage.
func (c *Client) EnqueueWebhookEvent(ctx context.Context, eventType, scope, txHash string, blockNumber, blockTimestamp int64, payload any) (string, error) {
	id := newUUID()
	payloadMap := map[string]any{}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("marshal payload: %w", err)
		}
		if err := json.Unmarshal(raw, &payloadMap); err != nil {
			return "", fmt.Errorf("unmarshal payload: %w", err)
		}
	}
	payloadMap["id"] = id
	data, err := json.Marshal(payloadMap)
	if err != nil {
		return "", fmt.Errorf("marshal payload with id: %w", err)
	}
	dedupeKey := fmt.Sprintf("%s:%s", scope, txHash)
	tag, err := c.Pool.Exec(ctx, `
		INSERT INTO webhook_events (
			id, event_type, scope, tx_hash, block_number, block_timestamp, payload, dedupe_key
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (dedupe_key) DO NOTHING
	`, id, eventType, scope, txHash, blockNumber, blockTimestamp, data, dedupeKey)
	if err != nil {
		return "", fmt.Errorf("EnqueueWebhookEvent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", nil
	}
	return id, nil
}

// ClaimPendingWebhookEvents claims pending/failed events ready for delivery.
func (c *Client) ClaimPendingWebhookEvents(ctx context.Context, limit int) ([]WebhookEvent, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := c.Pool.Query(ctx, `
		UPDATE webhook_events
		SET status = 'delivering', updated_at = now()
		WHERE id IN (
			SELECT id FROM webhook_events
			WHERE status IN ('pending', 'failed')
			  AND next_attempt_at <= now()
			ORDER BY next_attempt_at ASC, created_at ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id::text, event_type, scope, tx_hash, block_number, block_timestamp,
		          payload, dedupe_key, status, attempt_count, next_attempt_at
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("ClaimPendingWebhookEvents: %w", err)
	}
	defer rows.Close()

	var events []WebhookEvent
	for rows.Next() {
		var ev WebhookEvent
		if err := rows.Scan(
			&ev.ID, &ev.EventType, &ev.Scope, &ev.TxHash, &ev.BlockNumber, &ev.BlockTimestamp,
			&ev.Payload, &ev.DedupeKey, &ev.Status, &ev.AttemptCount, &ev.NextAttemptAt,
		); err != nil {
			return nil, fmt.Errorf("ClaimPendingWebhookEvents scan: %w", err)
		}
		events = append(events, ev)
	}
	return events, rows.Err()
}

// MarkWebhookEventDelivered marks an event as successfully delivered.
func (c *Client) MarkWebhookEventDelivered(ctx context.Context, id string, responseCode int) error {
	_, err := c.Pool.Exec(ctx, `
		UPDATE webhook_events
		SET status = 'delivered', delivered_at = now(), last_response_code = $2,
		    last_error = NULL, updated_at = now()
		WHERE id = $1
	`, id, responseCode)
	if err != nil {
		return fmt.Errorf("MarkWebhookEventDelivered: %w", err)
	}
	return nil
}

// MarkWebhookEventFailed schedules a retry or marks dead after max attempts.
func (c *Client) MarkWebhookEventFailed(ctx context.Context, id string, attemptNumber int, responseCode *int, errMsg string, nextAttempt time.Time, maxAttempts int) error {
	status := "failed"
	if attemptNumber >= maxAttempts {
		status = "dead"
	}
	var code any
	if responseCode != nil {
		code = *responseCode
	}
	_, err := c.Pool.Exec(ctx, `
		UPDATE webhook_events
		SET status = $2,
		    attempt_count = $3,
		    next_attempt_at = CASE WHEN $2 = 'dead' THEN next_attempt_at ELSE $4 END,
		    last_response_code = $5,
		    last_error = $6,
		    updated_at = now()
		WHERE id = $1
	`, id, status, attemptNumber, nextAttempt, code, errMsg)
	if err != nil {
		return fmt.Errorf("MarkWebhookEventFailed: %w", err)
	}
	return nil
}

// RecordWebhookDeliveryAttempt stores one delivery attempt for observability.
func (c *Client) RecordWebhookDeliveryAttempt(ctx context.Context, eventID string, attemptNumber int, reqHeaders, reqBody json.RawMessage, responseCode *int, responseBody, errMsg string, durationMs int) error {
	_, err := c.Pool.Exec(ctx, `
		INSERT INTO webhook_delivery_attempts (
			webhook_event_id, attempt_number, request_headers, request_body,
			response_code, response_body, error_message, duration_ms
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, eventID, attemptNumber, reqHeaders, reqBody, responseCode, responseBody, errMsg, durationMs)
	if err != nil {
		return fmt.Errorf("RecordWebhookDeliveryAttempt: %w", err)
	}
	return nil
}

func scanWatchedAddresses(rows pgx.Rows) ([]WatchedAddress, error) {
	var out []WatchedAddress
	for rows.Next() {
		var row WatchedAddress
		if err := rows.Scan(&row.ID, &row.Address, &row.Status, &row.Source, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan address: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
