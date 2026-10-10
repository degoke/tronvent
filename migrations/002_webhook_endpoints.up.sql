-- Webhooks: multi-endpoint fanout, direction-specific event types, outbox dedupe keys, scanner scope leases.

ALTER TABLE scanner_webhook_config
  ADD COLUMN IF NOT EXISTS signing_secret_previous text,
  ADD COLUMN IF NOT EXISTS event_types text[] NOT NULL DEFAULT ARRAY['transaction.trx', 'transaction.trc20'];

CREATE TABLE IF NOT EXISTS webhook_endpoints (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  webhook_url text NOT NULL,
  signing_secret text NOT NULL,
  signing_secret_previous text,
  signing_public_key text,
  event_types text[] NOT NULL DEFAULT ARRAY[
    'transaction.trx.received',
    'transaction.trx.broadcasted',
    'transaction.trc20.received',
    'transaction.trc20.broadcasted'
  ],
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
SELECT
  webhook_url,
  CASE
    WHEN signing_secret ~ '^(whsec_|whsk_)' THEN signing_secret
    ELSE 'whsec_' || encode(convert_to(signing_secret, 'UTF8'), 'base64')
  END,
  CASE
    WHEN signing_secret_previous IS NULL OR signing_secret_previous = '' THEN signing_secret_previous
    WHEN signing_secret_previous ~ '^(whsec_|whsk_)' THEN signing_secret_previous
    ELSE 'whsec_' || encode(convert_to(signing_secret_previous, 'UTF8'), 'base64')
  END,
  event_types,
  is_active,
  source,
  created_at,
  updated_at
FROM scanner_webhook_config
WHERE id = true
  AND webhook_url <> ''
  AND NOT EXISTS (SELECT 1 FROM webhook_endpoints LIMIT 1);

UPDATE webhook_endpoints
SET signing_secret = 'whsec_' || encode(convert_to(signing_secret, 'UTF8'), 'base64')
WHERE signing_secret !~ '^(whsec_|whsk_)';

UPDATE webhook_endpoints
SET signing_secret_previous = 'whsec_' || encode(convert_to(signing_secret_previous, 'UTF8'), 'base64')
WHERE signing_secret_previous IS NOT NULL
  AND signing_secret_previous <> ''
  AND signing_secret_previous !~ '^(whsec_|whsk_)';

UPDATE webhook_endpoints we
SET event_types = sub.new_types
FROM (
  SELECT
    id,
    array_agg(DISTINCT mapped ORDER BY mapped) AS new_types
  FROM webhook_endpoints,
  LATERAL unnest(event_types) AS t,
  LATERAL (
    SELECT unnest(
      CASE t
        WHEN 'transaction.trx' THEN ARRAY['transaction.trx.received', 'transaction.trx.broadcasted']
        WHEN 'transaction.trc20' THEN ARRAY['transaction.trc20.received', 'transaction.trc20.broadcasted']
        ELSE ARRAY[t]
      END
    ) AS mapped
  ) expanded
  GROUP BY id
) sub
WHERE we.id = sub.id
  AND we.event_types && ARRAY['transaction.trx', 'transaction.trc20']::text[];

-- Only when a single endpoint exists (typical singleton migration). Skip when multiple endpoints already exist.
UPDATE webhook_events e
SET endpoint_id = sub.id
FROM (
  SELECT id FROM webhook_endpoints ORDER BY created_at ASC LIMIT 1
) sub
WHERE e.endpoint_id IS NULL
  AND (SELECT count(*)::int FROM webhook_endpoints) = 1;

-- Align dedupe keys with EnqueueWebhookEvent (event type + transfer leg + endpoint).
UPDATE webhook_events e
SET dedupe_key =
  e.scope || ':' || e.tx_hash || ':' ||
  COALESCE(NULLIF(btrim(e.event_type), ''), COALESCE(e.payload->>'type', 'unknown')) || ':' ||
  COALESCE(e.payload->'data'->>'fromAddress', '') || ':' ||
  COALESCE(e.payload->'data'->>'toAddress', '') || ':' ||
  COALESCE(e.payload->'data'->>'amount', '') || ':' ||
  e.endpoint_id::text
WHERE e.endpoint_id IS NOT NULL
  AND e.dedupe_key IS DISTINCT FROM (
    e.scope || ':' || e.tx_hash || ':' ||
    COALESCE(NULLIF(btrim(e.event_type), ''), COALESCE(e.payload->>'type', 'unknown')) || ':' ||
    COALESCE(e.payload->'data'->>'fromAddress', '') || ':' ||
    COALESCE(e.payload->'data'->>'toAddress', '') || ':' ||
    COALESCE(e.payload->'data'->>'amount', '') || ':' ||
    e.endpoint_id::text
  );

DELETE FROM webhook_events older
USING webhook_events newer
WHERE older.dedupe_key = newer.dedupe_key
  AND older.id > newer.id;

DROP TABLE IF EXISTS scanner_webhook_config;

-- Per-scope scan leases so multiple replicas do not forward-scan the same scope.
ALTER TABLE scanner_cursors
  ADD COLUMN IF NOT EXISTS locked_by text,
  ADD COLUMN IF NOT EXISTS locked_until timestamptz;

CREATE INDEX IF NOT EXISTS scanner_cursors_locked_until_idx
  ON scanner_cursors (locked_until)
  WHERE locked_until IS NOT NULL;
