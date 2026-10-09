package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/degoke/tronvent/internal/webhookpayload"
	"github.com/degoke/tronvent/internal/webhookspec"
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
	CreatedAt      time.Time
	EndpointID     string
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

// IsWatchedAddressActive reports whether address exists on the watchlist with status active.
func (c *Client) IsWatchedAddressActive(ctx context.Context, address string) (bool, error) {
	var status string
	err := c.Pool.QueryRow(ctx, `
		SELECT status FROM scanner_watched_addresses WHERE address = $1
	`, address).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("IsWatchedAddressActive: %w", err)
	}
	return status == "active", nil
}

// ActiveWatchedAddresses returns whether each address is actively watched (missing or inactive → false).
func (c *Client) ActiveWatchedAddresses(ctx context.Context, addresses []string) (map[string]bool, error) {
	out := make(map[string]bool, len(addresses))
	if len(addresses) == 0 {
		return out, nil
	}
	unique := make([]string, 0, len(addresses))
	seen := make(map[string]struct{}, len(addresses))
	for _, a := range addresses {
		if a == "" {
			continue
		}
		out[a] = false
		if _, ok := seen[a]; ok {
			continue
		}
		seen[a] = struct{}{}
		unique = append(unique, a)
	}
	if len(unique) == 0 {
		return out, nil
	}
	rows, err := c.Pool.Query(ctx, `
		SELECT address, status FROM scanner_watched_addresses WHERE address = ANY($1)
	`, unique)
	if err != nil {
		return nil, fmt.Errorf("ActiveWatchedAddresses: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var addr, status string
		if err := rows.Scan(&addr, &status); err != nil {
			return nil, fmt.Errorf("ActiveWatchedAddresses scan: %w", err)
		}
		out[addr] = status == "active"
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ActiveWatchedAddresses: %w", err)
	}
	return out, nil
}

// ListAddresses returns watched addresses with optional status filter, exact search, and cursor pagination.
func (c *Client) ListAddresses(ctx context.Context, status string, limit int, afterAddress, search string) ([]WatchedAddress, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if search != "" {
		rows, err := c.Pool.Query(ctx, `
			SELECT id::text, address, status, source, created_at, updated_at
			FROM scanner_watched_addresses
			WHERE address = $1
			  AND ($2 = '' OR status = $2)
			LIMIT 1
		`, search, status)
		if err != nil {
			return nil, fmt.Errorf("ListAddresses search: %w", err)
		}
		defer rows.Close()
		return scanWatchedAddresses(rows)
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

// DeactivateWatchedAddress marks an address inactive. The in-memory Bloom filter is not
// rebuilt; scanner confirmation uses IsWatchedAddressActive before enqueueing webhooks.
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

// ListContracts returns watched contracts with optional status filter, exact search, and cursor pagination.
func (c *Client) ListContracts(ctx context.Context, status string, limit int, afterContract, search string) ([]WatchedContract, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if search != "" {
		rows, err := c.Pool.Query(ctx, `
			SELECT id::text, contract_address, status, token_symbol, source, created_at, updated_at
			FROM scanner_watched_contracts
			WHERE contract_address = $1
			  AND ($2 = '' OR status = $2)
			LIMIT 1
		`, search, status)
		if err != nil {
			return nil, fmt.Errorf("ListContracts search: %w", err)
		}
		defer rows.Close()
		var out []WatchedContract
		for rows.Next() {
			var row WatchedContract
			if err := rows.Scan(&row.ID, &row.ContractAddress, &row.Status, &row.TokenSymbol, &row.Source, &row.CreatedAt, &row.UpdatedAt); err != nil {
				return nil, fmt.Errorf("ListContracts search scan: %w", err)
			}
			out = append(out, row)
		}
		return out, rows.Err()
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

// ScannerScopeLeaseDuration is how long a replica may hold a scope before another may take over.
// Renewed during each scan batch; keep comfortably above worst-case TronGrid batch latency.
const ScannerScopeLeaseDuration = 10 * time.Minute

// ErrScannerCursorLeasesRequired is returned when migration 006 has not been applied.
var ErrScannerCursorLeasesRequired = errors.New("scanner cursor lease columns required")

// ErrCursorLeaseConflict is returned when SetScannedBlock is called without holding the active lease.
var ErrCursorLeaseConflict = errors.New("scanner cursor update blocked by another scope lease holder")

// ScannerCursorLeasesEnabled reports whether migration 006 lease columns exist.
func (c *Client) ScannerCursorLeasesEnabled(ctx context.Context) (bool, error) {
	var ok bool
	err := c.Pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public'
			  AND table_name = 'scanner_cursors'
			  AND column_name = 'locked_by'
		)
	`).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("ScannerCursorLeasesEnabled: %w", err)
	}
	return ok, nil
}

// RequireScannerCursorLeases returns an error when migration 006 is not applied.
func (c *Client) RequireScannerCursorLeases(ctx context.Context) error {
	ok, err := c.ScannerCursorLeasesEnabled(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: apply migrations/006_scanner_cursor_scope_lease.up.sql", ErrScannerCursorLeasesRequired)
	}
	return nil
}

// TryClaimScannerScope grants an exclusive forward-scan lease for scope to workerID.
// Returns (highestBlock, claimed, err). claimed is false when another replica holds a valid lease.
func (c *Client) TryClaimScannerScope(ctx context.Context, scope, workerID string, lease time.Duration) (int64, bool, error) {
	if lease <= 0 {
		lease = ScannerScopeLeaseDuration
	}
	leaseSec := int(lease.Seconds())
	if leaseSec < 1 {
		leaseSec = 1
	}
	var highest int64
	err := c.Pool.QueryRow(ctx, `
		INSERT INTO scanner_cursors (scope, highest_block, locked_by, locked_until, updated_at)
		VALUES ($1, 0, $2, now() + ($3::bigint * interval '1 second'), now())
		ON CONFLICT (scope) DO UPDATE
		SET locked_by = EXCLUDED.locked_by,
		    locked_until = EXCLUDED.locked_until,
		    updated_at = now()
		WHERE scanner_cursors.locked_until IS NULL
		   OR scanner_cursors.locked_until < now()
		   OR scanner_cursors.locked_by = EXCLUDED.locked_by
		RETURNING highest_block
	`, scope, workerID, leaseSec).Scan(&highest)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("TryClaimScannerScope(%s): %w", scope, err)
	}
	return highest, true, nil
}

// RenewScannerScopeLease extends the lease for workerID while a long scan is in progress.
func (c *Client) RenewScannerScopeLease(ctx context.Context, scope, workerID string, lease time.Duration) error {
	if lease <= 0 {
		lease = ScannerScopeLeaseDuration
	}
	leaseSec := int(lease.Seconds())
	if leaseSec < 1 {
		leaseSec = 1
	}
	tag, err := c.Pool.Exec(ctx, `
		UPDATE scanner_cursors
		SET locked_until = now() + ($3::bigint * interval '1 second'),
		    updated_at = now()
		WHERE scope = $1 AND locked_by = $2
	`, scope, workerID, leaseSec)
	if err != nil {
		return fmt.Errorf("RenewScannerScopeLease(%s): %w", scope, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("RenewScannerScopeLease(%s): lease not held by %s", scope, workerID)
	}
	return nil
}

// ReleaseScannerScope drops the forward-scan lease when workerID holds it.
// released is false when no row matched (lease already expired or owned by another worker).
func (c *Client) ReleaseScannerScope(ctx context.Context, scope, workerID string) (bool, error) {
	tag, err := c.Pool.Exec(ctx, `
		UPDATE scanner_cursors
		SET locked_by = NULL, locked_until = NULL, updated_at = now()
		WHERE scope = $1 AND locked_by = $2
	`, scope, workerID)
	if err != nil {
		return false, fmt.Errorf("ReleaseScannerScope(%s): %w", scope, err)
	}
	return tag.RowsAffected() > 0, nil
}

// SetScannedBlock upserts the cursor for a scope.
// When leaseHolder is non-empty, the scope must not be actively leased to a different worker.
func (c *Client) SetScannedBlock(ctx context.Context, scope string, blockNum int64, leaseHolder string) error {
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var highest int64
	var lockedBy *string
	var lockedUntil *time.Time
	err = tx.QueryRow(ctx, `
		SELECT highest_block, locked_by, locked_until
		FROM scanner_cursors
		WHERE scope = $1
		FOR UPDATE
	`, scope).Scan(&highest, &lockedBy, &lockedUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		_, err = tx.Exec(ctx, `
			INSERT INTO scanner_cursors (scope, highest_block, updated_at)
			VALUES ($1, $2, now())
		`, scope, blockNum)
		if err != nil {
			return fmt.Errorf("SetScannedBlock(%s) insert: %w", scope, err)
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return fmt.Errorf("SetScannedBlock(%s) lock: %w", scope, err)
	}
	if leaseHolder != "" && activeScannerLease(lockedBy, lockedUntil) && *lockedBy != leaseHolder {
		return ErrCursorLeaseConflict
	}
	newHighest := blockNum
	if highest > newHighest {
		newHighest = highest
	}
	_, err = tx.Exec(ctx, `
		UPDATE scanner_cursors
		SET highest_block = $2, updated_at = now()
		WHERE scope = $1
	`, scope, newHighest)
	if err != nil {
		return fmt.Errorf("SetScannedBlock(%s) update: %w", scope, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("SetScannedBlock(%s) commit: %w", scope, err)
	}
	return nil
}

func activeScannerLease(lockedBy *string, lockedUntil *time.Time) bool {
	if lockedBy == nil || *lockedBy == "" {
		return false
	}
	if lockedUntil == nil {
		return true
	}
	return lockedUntil.After(time.Now())
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

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// DeactivateWebhook disables delivery for one endpoint or all endpoints when endpointID is empty.
func (c *Client) DeactivateWebhook(ctx context.Context, endpointID, reason string) error {
	if endpointID != "" {
		return c.DeactivateWebhookEndpoint(ctx, endpointID, reason)
	}
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE webhook_endpoints SET is_active = false, updated_at = now()`); err != nil {
		return fmt.Errorf("DeactivateWebhook: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyWebhookChanged, fmt.Sprintf(`{"reason":"%s"}`, reason)); err != nil {
		return fmt.Errorf("notify webhook deactivate: %w", err)
	}
	return tx.Commit(ctx)
}

// legacyWebhookDedupeExists reports whether an older dedupe_key format already recorded this transfer leg.
func (c *Client) legacyWebhookDedupeExists(ctx context.Context, tx pgx.Tx, scope, txHash, from, to, amount, endpointID string) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM webhook_events
			WHERE scope = $1 AND tx_hash = $2
			  AND COALESCE(payload->'data'->>'fromAddress', '') = $3
			  AND COALESCE(payload->'data'->>'toAddress', '') = $4
			  AND COALESCE(payload->'data'->>'amount', '') = $5
			  AND (
			    dedupe_key = ($1 || ':' || $2)
			    OR ($6 <> '' AND dedupe_key = ($1 || ':' || $2 || ':' || $6))
			  )
		)
	`, scope, txHash, from, to, amount, endpointID).Scan(&exists)
	return exists, err
}

// EnqueueWebhookEvent inserts a matched event into the outbox (deduplicated).
// The event id is generated here and injected into the payload before storage.
func (c *Client) EnqueueWebhookEvent(ctx context.Context, eventType, scope, txHash string, blockNumber, blockTimestamp int64, payload any) (string, error) {
	endpoints, err := c.ListWebhookEndpoints(ctx)
	if err != nil {
		return "", err
	}
	if len(endpoints) == 0 {
		return "", nil
	}
	stdType := webhookpayload.NormalizeEventType(eventType)
	if !webhookpayload.IsKnownEventType(stdType) {
		return "", fmt.Errorf("EnqueueWebhookEvent: unknown event type %q", stdType)
	}

	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var firstID string
	enqueued := 0
	for _, ep := range endpoints {
		if !ep.IsActive || !webhookpayload.EndpointSubscribes(ep.EventTypes, stdType) {
			continue
		}
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
		dataObj, ok := payloadMap["data"].(map[string]any)
		if !ok {
			return "", fmt.Errorf("webhook payload must include a data object")
		}
		if typ, _ := payloadMap["type"].(string); typ != "" && typ != stdType {
			return "", fmt.Errorf("EnqueueWebhookEvent: payload type %q does not match event type %q", typ, stdType)
		}
		payloadMap["type"] = stdType
		fromAddr, _ := dataObj["fromAddress"].(string)
		toAddr, _ := dataObj["toAddress"].(string)
		amount, _ := dataObj["amount"].(string)
		transferKey := webhookpayload.TransferDedupeKey(fromAddr, toAddr, amount)
		dataObj["id"] = id
		data, err := json.Marshal(payloadMap)
		if err != nil {
			return "", fmt.Errorf("marshal payload with id: %w", err)
		}
		if err := webhookspec.ValidatePayloadSize(data); err != nil {
			return "", err
		}
		var endpointID any
		dedupeKey := fmt.Sprintf("%s:%s:%s:%s", scope, txHash, stdType, transferKey)
		if ep.ID != "" {
			endpointID = ep.ID
			dedupeKey = fmt.Sprintf("%s:%s:%s:%s:%s", scope, txHash, stdType, transferKey, ep.ID)
		}
		legacyDup, err := c.legacyWebhookDedupeExists(ctx, tx, scope, txHash, fromAddr, toAddr, amount, ep.ID)
		if err != nil {
			return "", err
		}
		if legacyDup {
			continue
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO webhook_events (
				id, event_type, scope, tx_hash, block_number, block_timestamp, payload, dedupe_key, endpoint_id
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			ON CONFLICT (dedupe_key) DO NOTHING
		`, id, stdType, scope, txHash, blockNumber, blockTimestamp, data, dedupeKey, endpointID)
		if err != nil {
			return "", fmt.Errorf("EnqueueWebhookEvent: %w", err)
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		enqueued++
		if firstID == "" {
			firstID = id
		}
	}
	if enqueued == 0 {
		if err := tx.Commit(ctx); err != nil {
			return "", err
		}
		return "", nil
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return firstID, nil
}

// ClaimPendingWebhookEvents claims pending/failed events ready for delivery.
func (c *Client) ClaimPendingWebhookEvents(ctx context.Context, limit int) ([]WebhookEvent, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := c.Pool.Query(ctx, `
		UPDATE webhook_events AS w
		SET status = 'delivering',
		    updated_at = now(),
		    attempt_count = CASE
		      WHEN w.status = 'delivering' AND w.updated_at < now() - interval '5 minutes'
		      THEN w.attempt_count + 1
		      ELSE w.attempt_count
		    END
		WHERE w.id IN (
			SELECT id FROM webhook_events
			WHERE (
			    status IN ('pending', 'failed')
			    AND next_attempt_at <= now()
			  )
			  OR (
			    status = 'delivering'
			    AND updated_at < now() - interval '5 minutes'
			  )
			ORDER BY next_attempt_at ASC, created_at ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id::text, event_type, scope, tx_hash, block_number, block_timestamp,
		          payload, dedupe_key, status, attempt_count, next_attempt_at, created_at,
		          COALESCE(endpoint_id::text, '')
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
			&ev.Payload, &ev.DedupeKey, &ev.Status, &ev.AttemptCount, &ev.NextAttemptAt, &ev.CreatedAt,
			&ev.EndpointID,
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
