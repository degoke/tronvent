package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Client wraps a pgxpool connection pool.
type Client struct {
	Pool *pgxpool.Pool
}

type BlockRangeJob struct {
	ID        string
	FromBlock int64
	ToBlock   int64
}

// New creates a new DB client from the given DSN.
func New(ctx context.Context, dsn string) (*Client, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("db ping: %w", err)
	}
	return &Client{Pool: pool}, nil
}

// Close closes all connections in the pool.
func (c *Client) Close() {
	c.Pool.Close()
}

func (c *Client) EnqueueBlockRangeJob(ctx context.Context, queue string, jobType string, fromBlock int64, toBlock int64, maxAttempts int) error {
	if maxAttempts <= 0 {
		maxAttempts = 10
	}
	_, err := c.Pool.Exec(ctx, `
		INSERT INTO queue_jobs (id, queue, job_type, status, payload, max_attempts)
		VALUES ($1, $2, $3, 'pending', jsonb_build_object('fromBlock', $4::bigint, 'toBlock', $5::bigint), $6)
	`, jobID(), queue, jobType, fromBlock, toBlock, maxAttempts)
	if err != nil {
		return fmt.Errorf("EnqueueBlockRangeJob(%s): %w", queue, err)
	}
	return nil
}

func (c *Client) ClaimBlockRangeJobs(ctx context.Context, queue string, workerID string, limit int) ([]BlockRangeJob, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := c.Pool.Query(ctx, `
		UPDATE queue_jobs
		SET
			status = 'running',
			attempts = attempts + 1,
			locked_at = NOW(),
			locked_by = $2,
			updated_at = NOW()
		WHERE id IN (
			SELECT id
			FROM queue_jobs
			WHERE queue = $1
			  AND status IN ('pending', 'failed')
			  AND run_after <= NOW()
			  AND (locked_at IS NULL OR locked_at < NOW() - INTERVAL '5 minutes')
			ORDER BY run_after ASC, created_at ASC
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		RETURNING
			id,
			(payload->>'fromBlock')::bigint AS from_block,
			(payload->>'toBlock')::bigint AS to_block
	`, queue, workerID, limit)
	if err != nil {
		return nil, fmt.Errorf("ClaimBlockRangeJobs(%s): %w", queue, err)
	}
	defer rows.Close()

	jobs := make([]BlockRangeJob, 0, limit)
	for rows.Next() {
		var job BlockRangeJob
		if err := rows.Scan(&job.ID, &job.FromBlock, &job.ToBlock); err != nil {
			return nil, fmt.Errorf("ClaimBlockRangeJobs scan: %w", err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ClaimBlockRangeJobs rows: %w", err)
	}
	return jobs, nil
}

func (c *Client) ClaimBlockRangeJob(ctx context.Context, queue string, workerID string) (*BlockRangeJob, error) {
	jobs, err := c.ClaimBlockRangeJobs(ctx, queue, workerID, 1)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, pgx.ErrNoRows
	}
	return &jobs[0], nil
}

func (c *Client) CompleteJob(ctx context.Context, id string) error {
	_, err := c.Pool.Exec(ctx, `
		UPDATE queue_jobs
		SET status = 'completed', locked_at = NULL, locked_by = NULL, completed_at = NOW(), updated_at = NOW()
		WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("CompleteJob(%s): %w", id, err)
	}
	return nil
}

func (c *Client) FailJob(ctx context.Context, id string, cause error, retryAfter time.Duration) error {
	if retryAfter <= 0 {
		retryAfter = time.Minute
	}
	msg := ""
	if cause != nil {
		msg = cause.Error()
	}
	_, err := c.Pool.Exec(ctx, `
		UPDATE queue_jobs
		SET
			status = CASE WHEN attempts >= max_attempts THEN 'dead' ELSE 'failed' END,
			locked_at = NULL,
			locked_by = NULL,
			last_error = $2,
			run_after = NOW() + ($3::text || ' seconds')::interval,
			updated_at = NOW()
		WHERE id = $1
	`, id, msg, int(retryAfter.Seconds()))
	if err != nil {
		return fmt.Errorf("FailJob(%s): %w", id, err)
	}
	return nil
}

func jobID() string {
	return "job_" + randomHex(16)
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
