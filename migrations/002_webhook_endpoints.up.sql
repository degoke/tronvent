-- Standard Webhooks: rotation fields on legacy singleton, webhook_endpoints, fanout column, drop singleton.

ALTER TABLE scanner_webhook_config
  ADD COLUMN IF NOT EXISTS signing_secret_previous text,
  ADD COLUMN IF NOT EXISTS event_types text[] NOT NULL DEFAULT ARRAY['transaction.trx', 'transaction.trc20'];

CREATE TABLE IF NOT EXISTS webhook_endpoints (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  webhook_url text NOT NULL,
  signing_secret text NOT NULL,
  signing_secret_previous text,
  signing_public_key text,
  event_types text[] NOT NULL DEFAULT ARRAY['transaction.trx', 'transaction.trc20'],
  is_active boolean NOT NULL DEFAULT true,
  failure_notify_email text,
  source text NOT NULL DEFAULT 'api',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE webhook_events
  ADD COLUMN IF NOT EXISTS endpoint_id uuid REFERENCES webhook_endpoints(id);

INSERT INTO webhook_endpoints (
  webhook_url, signing_secret, signing_secret_previous, event_types, is_active, source, created_at, updated_at
)
SELECT webhook_url, signing_secret, signing_secret_previous, event_types, is_active, source, created_at, updated_at
FROM scanner_webhook_config
WHERE id = true
  AND webhook_url <> ''
  AND NOT EXISTS (SELECT 1 FROM webhook_endpoints LIMIT 1);

DROP TABLE IF EXISTS scanner_webhook_config;
