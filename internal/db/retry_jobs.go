package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	QueueTronAdminRetry = "tron-admin-retry"
	JobTypeBlockRange   = "block-range"
)

var (
	ErrNonPositiveBlock  = errors.New("block numbers must be positive")
	ErrInvalidBlockRange = errors.New("fromBlock must be <= toBlock")
)

// RetryJobRecord is a queue_jobs row for admin replay work.
type RetryJobRecord struct {
	ID          string
	Queue       string
	JobType     string
	FromBlock   int64
	ToBlock     int64
	Status      string
	Attempts    int
	MaxAttempts int
	LastError   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
}

// EnqueueRetryResult is returned when enqueueing manual replay work.
type EnqueueRetryResult struct {
	Job     RetryJobRecord
	Created bool
}

// ValidateBlockRange checks replay block bounds.
func ValidateBlockRange(fromBlock, toBlock int64) error {
	if fromBlock <= 0 || toBlock <= 0 {
		return ErrNonPositiveBlock
	}
	if fromBlock > toBlock {
		return ErrInvalidBlockRange
	}
	return nil
}

// EnqueueRetryJob enqueues a block-range replay job on tron-admin-retry.
// Identical pending or running jobs for the same range are deduplicated.
func (c *Client) EnqueueRetryJob(ctx context.Context, fromBlock, toBlock int64) (EnqueueRetryResult, error) {
	if err := ValidateBlockRange(fromBlock, toBlock); err != nil {
		return EnqueueRetryResult{}, err
	}

	const maxAttempts = 10

	var existing RetryJobRecord
	err := c.Pool.QueryRow(ctx, `
		SELECT
			id, queue, job_type,
			(payload->>'fromBlock')::bigint,
			(payload->>'toBlock')::bigint,
			status, attempts, max_attempts, last_error,
			created_at, updated_at, completed_at
		FROM queue_jobs
		WHERE queue = $1
		  AND job_type = $2
		  AND status IN ('pending', 'running')
		  AND (payload->>'fromBlock')::bigint = $3
		  AND (payload->>'toBlock')::bigint = $4
		ORDER BY created_at ASC
		LIMIT 1
	`, QueueTronAdminRetry, JobTypeBlockRange, fromBlock, toBlock).Scan(
		&existing.ID, &existing.Queue, &existing.JobType,
		&existing.FromBlock, &existing.ToBlock,
		&existing.Status, &existing.Attempts, &existing.MaxAttempts, &existing.LastError,
		&existing.CreatedAt, &existing.UpdatedAt, &existing.CompletedAt,
	)
	if err == nil {
		return EnqueueRetryResult{Job: existing, Created: false}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return EnqueueRetryResult{}, fmt.Errorf("EnqueueRetryJob lookup: %w", err)
	}

	id := jobID()
	var job RetryJobRecord
	err = c.Pool.QueryRow(ctx, `
		INSERT INTO queue_jobs (id, queue, job_type, status, payload, max_attempts)
		VALUES ($1, $2, $3, 'pending', jsonb_build_object('fromBlock', $4::bigint, 'toBlock', $5::bigint), $6)
		RETURNING
			id, queue, job_type,
			(payload->>'fromBlock')::bigint,
			(payload->>'toBlock')::bigint,
			status, attempts, max_attempts, last_error,
			created_at, updated_at, completed_at
	`, id, QueueTronAdminRetry, JobTypeBlockRange, fromBlock, toBlock, maxAttempts).Scan(
		&job.ID, &job.Queue, &job.JobType,
		&job.FromBlock, &job.ToBlock,
		&job.Status, &job.Attempts, &job.MaxAttempts, &job.LastError,
		&job.CreatedAt, &job.UpdatedAt, &job.CompletedAt,
	)
	if err != nil {
		return EnqueueRetryResult{}, fmt.Errorf("EnqueueRetryJob insert: %w", err)
	}
	return EnqueueRetryResult{Job: job, Created: true}, nil
}

// ListRetryJobs returns recent admin replay jobs, optionally filtered by status.
func (c *Client) ListRetryJobs(ctx context.Context, status string, limit int) ([]RetryJobRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	rows, err := c.Pool.Query(ctx, `
		SELECT
			id, queue, job_type,
			(payload->>'fromBlock')::bigint,
			(payload->>'toBlock')::bigint,
			status, attempts, max_attempts, last_error,
			created_at, updated_at, completed_at
		FROM queue_jobs
		WHERE queue = $1
		  AND ($2 = '' OR status = $2)
		ORDER BY created_at DESC
		LIMIT $3
	`, QueueTronAdminRetry, status, limit)
	if err != nil {
		return nil, fmt.Errorf("ListRetryJobs: %w", err)
	}
	defer rows.Close()

	return scanRetryJobs(rows)
}

func scanRetryJobs(rows pgx.Rows) ([]RetryJobRecord, error) {
	var out []RetryJobRecord
	for rows.Next() {
		var job RetryJobRecord
		if err := rows.Scan(
			&job.ID, &job.Queue, &job.JobType,
			&job.FromBlock, &job.ToBlock,
			&job.Status, &job.Attempts, &job.MaxAttempts, &job.LastError,
			&job.CreatedAt, &job.UpdatedAt, &job.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("ListRetryJobs scan: %w", err)
		}
		out = append(out, job)
	}
	return out, rows.Err()
}
