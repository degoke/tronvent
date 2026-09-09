package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const QueueTronReconcile = "tron-reconcile"

var ErrWebhookEventNotFound = errors.New("webhook event not found")

// DashboardWebhookEvent is a read model for webhook outbox rows.
type DashboardWebhookEvent struct {
	ID               string
	EventType        string
	Scope            string
	TxHash           string
	BlockNumber      int64
	Status           string
	AttemptCount     int
	LastError        *string
	LastResponseCode *int
	CreatedAt        time.Time
	UpdatedAt        time.Time
	NextAttemptAt    time.Time
}

// DashboardDeliveryAttempt is a read model for webhook delivery history.
type DashboardDeliveryAttempt struct {
	ID            string
	AttemptNumber int
	ResponseCode  *int
	ErrorMessage  *string
	DurationMs    *int
	CreatedAt     time.Time
}

// DashboardQueueJob is a read model for queue_jobs rows shown in the dashboard.
type DashboardQueueJob struct {
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

// UpsertWebhookConfigPreserveSecret updates webhook settings, keeping the existing
// signing secret when signingSecret is empty.
func (c *Client) UpsertWebhookConfigPreserveSecret(ctx context.Context, webhookURL, signingSecret string, isActive bool, source string) (*WebhookConfig, error) {
	secret := signingSecret
	if secret == "" {
		existing, err := c.GetWebhookConfig(ctx)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			return nil, fmt.Errorf("UpsertWebhookConfigPreserveSecret: no existing webhook config")
		}
		secret = existing.SigningSecret
	}
	return c.UpsertWebhookConfig(ctx, webhookURL, secret, isActive, source)
}

// ListWebhookEvents returns recent webhook outbox rows with optional status filter.
func (c *Client) ListWebhookEvents(ctx context.Context, status string, limit int) ([]DashboardWebhookEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := c.Pool.Query(ctx, `
		SELECT id::text, event_type, scope, tx_hash, block_number, status,
		       attempt_count, last_error, last_response_code,
		       created_at, updated_at, next_attempt_at
		FROM webhook_events
		WHERE ($1 = '' OR status = $1)
		ORDER BY created_at DESC
		LIMIT $2
	`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("ListWebhookEvents: %w", err)
	}
	defer rows.Close()

	var out []DashboardWebhookEvent
	for rows.Next() {
		var ev DashboardWebhookEvent
		if err := rows.Scan(
			&ev.ID, &ev.EventType, &ev.Scope, &ev.TxHash, &ev.BlockNumber, &ev.Status,
			&ev.AttemptCount, &ev.LastError, &ev.LastResponseCode,
			&ev.CreatedAt, &ev.UpdatedAt, &ev.NextAttemptAt,
		); err != nil {
			return nil, fmt.Errorf("ListWebhookEvents scan: %w", err)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// ListWebhookDeliveryAttempts returns delivery history for one webhook event.
func (c *Client) ListWebhookDeliveryAttempts(ctx context.Context, eventID string) ([]DashboardDeliveryAttempt, error) {
	rows, err := c.Pool.Query(ctx, `
		SELECT id::text, attempt_number, response_code, error_message, duration_ms, created_at
		FROM webhook_delivery_attempts
		WHERE webhook_event_id = $1
		ORDER BY attempt_number ASC
	`, eventID)
	if err != nil {
		return nil, fmt.Errorf("ListWebhookDeliveryAttempts: %w", err)
	}
	defer rows.Close()

	var out []DashboardDeliveryAttempt
	for rows.Next() {
		var att DashboardDeliveryAttempt
		if err := rows.Scan(
			&att.ID, &att.AttemptNumber, &att.ResponseCode, &att.ErrorMessage, &att.DurationMs, &att.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("ListWebhookDeliveryAttempts scan: %w", err)
		}
		out = append(out, att)
	}
	return out, rows.Err()
}

// RetryWebhookEvent schedules one immediate delivery attempt for a failed/dead event.
func (c *Client) RetryWebhookEvent(ctx context.Context, eventID string) error {
	tag, err := c.Pool.Exec(ctx, `
		UPDATE webhook_events
		SET status = 'pending',
		    next_attempt_at = now(),
		    last_error = NULL,
		    last_response_code = NULL,
		    updated_at = now()
		WHERE id = $1 AND status IN ('failed', 'dead')
	`, eventID)
	if err != nil {
		return fmt.Errorf("RetryWebhookEvent: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrWebhookEventNotFound
	}
	return nil
}

// RetryAllFailedDeadWebhookEvents schedules one immediate delivery attempt for
// every failed/dead event.
func (c *Client) RetryAllFailedDeadWebhookEvents(ctx context.Context) (int64, error) {
	tag, err := c.Pool.Exec(ctx, `
		UPDATE webhook_events
		SET status = 'pending',
		    next_attempt_at = now(),
		    last_error = NULL,
		    last_response_code = NULL,
		    updated_at = now()
		WHERE status IN ('failed', 'dead')
	`)
	if err != nil {
		return 0, fmt.Errorf("RetryAllFailedDeadWebhookEvents: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ListQueueJobs returns recent jobs across the given queues with optional status filter.
// statusFilter "" defaults to pending+running; "all" returns every status.
func (c *Client) ListQueueJobs(ctx context.Context, queues []string, statusFilter string, limit int) ([]DashboardQueueJob, error) {
	if len(queues) == 0 {
		queues = []string{QueueTronAdminRetry, QueueTronReconcile}
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	var rows pgx.Rows
	var err error
	switch statusFilter {
	case "", "active":
		rows, err = c.Pool.Query(ctx, `
			SELECT
				id, queue, job_type,
				(payload->>'fromBlock')::bigint,
				(payload->>'toBlock')::bigint,
				status, attempts, max_attempts, last_error,
				created_at, updated_at, completed_at
			FROM queue_jobs
			WHERE queue = ANY($1)
			  AND status IN ('pending', 'running')
			ORDER BY created_at DESC
			LIMIT $2
		`, queues, limit)
	case "all":
		rows, err = c.Pool.Query(ctx, `
			SELECT
				id, queue, job_type,
				(payload->>'fromBlock')::bigint,
				(payload->>'toBlock')::bigint,
				status, attempts, max_attempts, last_error,
				created_at, updated_at, completed_at
			FROM queue_jobs
			WHERE queue = ANY($1)
			ORDER BY created_at DESC
			LIMIT $2
		`, queues, limit)
	default:
		rows, err = c.Pool.Query(ctx, `
			SELECT
				id, queue, job_type,
				(payload->>'fromBlock')::bigint,
				(payload->>'toBlock')::bigint,
				status, attempts, max_attempts, last_error,
				created_at, updated_at, completed_at
			FROM queue_jobs
			WHERE queue = ANY($1)
			  AND status = $2
			ORDER BY created_at DESC
			LIMIT $3
		`, queues, statusFilter, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("ListQueueJobs: %w", err)
	}
	defer rows.Close()

	var out []DashboardQueueJob
	for rows.Next() {
		var job DashboardQueueJob
		if err := rows.Scan(
			&job.ID, &job.Queue, &job.JobType,
			&job.FromBlock, &job.ToBlock,
			&job.Status, &job.Attempts, &job.MaxAttempts, &job.LastError,
			&job.CreatedAt, &job.UpdatedAt, &job.CompletedAt,
		); err != nil {
			return nil, fmt.Errorf("ListQueueJobs scan: %w", err)
		}
		out = append(out, job)
	}
	return out, rows.Err()
}
