package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/degoke/tronvent/internal/webhookpayload"
	"github.com/degoke/tronvent/internal/webhookspec"
	"github.com/jackc/pgx/v5"
)

// ErrWebhookEndpointNotFound is returned when a webhook endpoint id does not exist.
var ErrWebhookEndpointNotFound = errors.New("webhook endpoint not found")

// WebhookEndpoint is a subscriber destination for webhook fanout.
type WebhookEndpoint struct {
	ID                    string
	WebhookURL            string
	SigningSecret         string
	SigningSecretPrevious string
	SigningPublicKey      string
	EventTypes            []string
	IsActive              bool
	FailureNotifyEmail    string
	Source                string
	UpdatedAt             time.Time
}

// SigningSecrets returns active and previous signing keys for delivery.
func (e *WebhookEndpoint) SigningSecrets() []string {
	out := make([]string, 0, 2)
	if e.SigningSecret != "" {
		out = append(out, e.SigningSecret)
	}
	if e.SigningSecretPrevious != "" && e.SigningSecretPrevious != e.SigningSecret {
		out = append(out, e.SigningSecretPrevious)
	}
	return out
}

func normalizeEndpointSigning(ep *WebhookEndpoint) error {
	key, err := webhookspec.PrepareSigningKeyForDelivery(ep.SigningSecret)
	if err != nil {
		return err
	}
	ep.SigningSecret = key
	if ep.SigningSecretPrevious == "" {
		return nil
	}
	prev, err := webhookspec.PrepareSigningKeyForDelivery(ep.SigningSecretPrevious)
	if err != nil {
		return err
	}
	ep.SigningSecretPrevious = prev
	return nil
}

// SubscribesTo reports whether the endpoint accepts the given event type.
func (e *WebhookEndpoint) SubscribesTo(eventType string) bool {
	return webhookpayload.EndpointSubscribes(e.EventTypes, eventType)
}

// ListWebhookEndpoints returns all configured endpoints.
func (c *Client) ListWebhookEndpoints(ctx context.Context) ([]WebhookEndpoint, error) {
	rows, err := c.Pool.Query(ctx, `
		SELECT id::text, webhook_url, signing_secret, COALESCE(signing_secret_previous, ''),
		       COALESCE(signing_public_key, ''), event_types, is_active,
		       COALESCE(failure_notify_email, ''), source, updated_at
		FROM webhook_endpoints
		ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("ListWebhookEndpoints: %w", err)
	}
	defer rows.Close()
	var out []WebhookEndpoint
	for rows.Next() {
		var ep WebhookEndpoint
		if err := rows.Scan(
			&ep.ID, &ep.WebhookURL, &ep.SigningSecret, &ep.SigningSecretPrevious, &ep.SigningPublicKey,
			&ep.EventTypes, &ep.IsActive, &ep.FailureNotifyEmail, &ep.Source, &ep.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("ListWebhookEndpoints scan: %w", err)
		}
		if err := normalizeEndpointSigning(&ep); err != nil {
			return nil, fmt.Errorf("ListWebhookEndpoints signing key: %w", err)
		}
		out = append(out, ep)
	}
	return out, rows.Err()
}

// GetWebhookEndpoint loads one endpoint by id.
func (c *Client) GetWebhookEndpoint(ctx context.Context, id string) (*WebhookEndpoint, error) {
	var ep WebhookEndpoint
	err := c.Pool.QueryRow(ctx, `
		SELECT id::text, webhook_url, signing_secret, COALESCE(signing_secret_previous, ''),
		       COALESCE(signing_public_key, ''), event_types, is_active,
		       COALESCE(failure_notify_email, ''), source, updated_at
		FROM webhook_endpoints WHERE id = $1
	`, id).Scan(
		&ep.ID, &ep.WebhookURL, &ep.SigningSecret, &ep.SigningSecretPrevious, &ep.SigningPublicKey,
		&ep.EventTypes, &ep.IsActive, &ep.FailureNotifyEmail, &ep.Source, &ep.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetWebhookEndpoint: %w", err)
	}
	if err := normalizeEndpointSigning(&ep); err != nil {
		return nil, fmt.Errorf("GetWebhookEndpoint signing key: %w", err)
	}
	return &ep, nil
}

// UpsertWebhookEndpoint creates or updates an endpoint (by id when non-empty).
func (c *Client) UpsertWebhookEndpoint(ctx context.Context, ep WebhookEndpoint) (*WebhookEndpoint, error) {
	policy := webhookspec.URLPolicy{}
	if err := policy.ValidateWebhookURL(ep.WebhookURL); err != nil {
		return nil, err
	}
	signingKey, err := webhookspec.NormalizeSigningKey(ep.SigningSecret)
	if err != nil {
		return nil, err
	}
	ep.SigningSecret = signingKey
	if ep.SigningSecretPrevious != "" {
		prev, err := webhookspec.NormalizeSigningKey(ep.SigningSecretPrevious)
		if err != nil {
			return nil, err
		}
		ep.SigningSecretPrevious = prev
	}
	if len(ep.EventTypes) > 0 {
		if err := webhookpayload.ValidateEventTypes(ep.EventTypes); err != nil {
			return nil, err
		}
	}
	types := ep.EventTypes
	if len(types) == 0 {
		types = webhookpayload.DefaultEventTypes()
	}

	var previous string
	if ep.ID != "" {
		existing, err := c.GetWebhookEndpoint(ctx, ep.ID)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			if existing.SigningSecret != "" && existing.SigningSecret != ep.SigningSecret {
				previous = existing.SigningSecret
			} else {
				previous = existing.SigningSecretPrevious
			}
		}
	}

	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var out WebhookEndpoint
	if ep.ID == "" {
		err = tx.QueryRow(ctx, `
			INSERT INTO webhook_endpoints (
				webhook_url, signing_secret, signing_secret_previous, signing_public_key,
				event_types, is_active, failure_notify_email, source, updated_at
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now())
			RETURNING id::text, webhook_url, signing_secret, COALESCE(signing_secret_previous,''),
			          COALESCE(signing_public_key,''), event_types, is_active,
			          COALESCE(failure_notify_email,''), source, updated_at
		`, ep.WebhookURL, ep.SigningSecret, nullIfEmpty(previous), nullIfEmpty(ep.SigningPublicKey),
			types, ep.IsActive, nullIfEmpty(ep.FailureNotifyEmail), ep.Source,
		).Scan(
			&out.ID, &out.WebhookURL, &out.SigningSecret, &out.SigningSecretPrevious, &out.SigningPublicKey,
			&out.EventTypes, &out.IsActive, &out.FailureNotifyEmail, &out.Source, &out.UpdatedAt,
		)
	} else {
		err = tx.QueryRow(ctx, `
			UPDATE webhook_endpoints SET
				webhook_url = $2,
				signing_secret = $3,
				signing_secret_previous = $4,
				signing_public_key = $5,
				event_types = $6,
				is_active = $7,
				failure_notify_email = $8,
				source = $9,
				updated_at = now()
			WHERE id = $1
			RETURNING id::text, webhook_url, signing_secret, COALESCE(signing_secret_previous,''),
			          COALESCE(signing_public_key,''), event_types, is_active,
			          COALESCE(failure_notify_email,''), source, updated_at
		`, ep.ID, ep.WebhookURL, ep.SigningSecret, nullIfEmpty(previous), nullIfEmpty(ep.SigningPublicKey),
			types, ep.IsActive, nullIfEmpty(ep.FailureNotifyEmail), ep.Source,
		).Scan(
			&out.ID, &out.WebhookURL, &out.SigningSecret, &out.SigningSecretPrevious, &out.SigningPublicKey,
			&out.EventTypes, &out.IsActive, &out.FailureNotifyEmail, &out.Source, &out.UpdatedAt,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("UpsertWebhookEndpoint: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyWebhookChanged, `{"reason":"reload"}`); err != nil {
		return nil, fmt.Errorf("notify webhook endpoints: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeactivateWebhookEndpoint disables one endpoint (e.g. HTTP 410).
func (c *Client) DeactivateWebhookEndpoint(ctx context.Context, endpointID, reason string) error {
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		UPDATE webhook_endpoints SET is_active = false, updated_at = now() WHERE id = $1
	`, endpointID)
	if err != nil {
		return fmt.Errorf("DeactivateWebhookEndpoint: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyWebhookChanged, fmt.Sprintf(`{"reason":"%s"}`, reason)); err != nil {
		return fmt.Errorf("notify webhook endpoint deactivate: %w", err)
	}
	return tx.Commit(ctx)
}

// DeleteWebhookEndpoint removes a subscriber endpoint.
func (c *Client) DeleteWebhookEndpoint(ctx context.Context, endpointID string) error {
	tx, err := c.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `DELETE FROM webhook_endpoints WHERE id = $1`, endpointID)
	if err != nil {
		return fmt.Errorf("DeleteWebhookEndpoint: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrWebhookEndpointNotFound
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyWebhookChanged, `{"reason":"reload"}`); err != nil {
		return fmt.Errorf("notify webhook endpoint delete: %w", err)
	}
	return tx.Commit(ctx)
}

// BootstrapWebhookEndpoints seeds the first endpoint from env when none exist.
func (c *Client) BootstrapWebhookEndpoints(ctx context.Context, webhookURL, signingSecret string) error {
	eps, err := c.ListWebhookEndpoints(ctx)
	if err != nil {
		return err
	}
	if len(eps) > 0 || webhookURL == "" {
		return nil
	}
	_, err = c.UpsertPrimaryWebhookEndpointPreserveSecret(ctx, webhookURL, signingSecret, true, "env", nil)
	return err
}

// UpsertPrimaryWebhookEndpointPreserveSecret updates the oldest endpoint or creates one.
func (c *Client) UpsertPrimaryWebhookEndpointPreserveSecret(ctx context.Context, webhookURL, signingSecret string, isActive bool, source string, eventTypes []string) (*WebhookEndpoint, error) {
	webhookURL = strings.TrimSpace(webhookURL)
	if webhookURL == "" {
		return nil, fmt.Errorf("webhook URL is required")
	}
	eps, err := c.ListWebhookEndpoints(ctx)
	if err != nil {
		return nil, err
	}
	var ep WebhookEndpoint
	if len(eps) > 0 {
		ep = eps[0]
		ep.WebhookURL = webhookURL
		ep.IsActive = isActive
		ep.Source = source
		if len(eventTypes) > 0 {
			ep.EventTypes = eventTypes
		}
		if signingSecret != "" {
			ep.SigningSecret = strings.TrimSpace(signingSecret)
			if strings.HasPrefix(ep.SigningSecret, "whsk_") {
				ep.SigningPublicKey, _ = webhookspec.PublicKeyFromPrivateKey(ep.SigningSecret)
			}
		}
	} else {
		signingKey := strings.TrimSpace(signingSecret)
		publicKey := ""
		if signingKey == "" {
			priv, pub, genErr := webhookspec.GenerateSigningKeyPair()
			if genErr != nil {
				return nil, genErr
			}
			signingKey, publicKey = priv, pub
		} else if strings.HasPrefix(signingKey, "whsk_") {
			publicKey, _ = webhookspec.PublicKeyFromPrivateKey(signingKey)
		}
		ep = WebhookEndpoint{
			WebhookURL: webhookURL, SigningSecret: signingKey, SigningPublicKey: publicKey,
			EventTypes: eventTypes, IsActive: isActive, Source: source,
		}
	}
	return c.UpsertWebhookEndpoint(ctx, ep)
}
