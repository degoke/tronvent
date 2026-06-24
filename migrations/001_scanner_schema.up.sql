-- Scanner-owned state (Postgres is the source of truth).

CREATE TABLE IF NOT EXISTS scanner_watched_addresses (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  address text NOT NULL UNIQUE,
  status text NOT NULL DEFAULT 'active',
  source text NOT NULL DEFAULT 'api',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS scanner_watched_addresses_status_idx
  ON scanner_watched_addresses (status);

CREATE TABLE IF NOT EXISTS scanner_watched_contracts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  contract_address text NOT NULL UNIQUE,
  status text NOT NULL DEFAULT 'active',
  token_symbol text,
  source text NOT NULL DEFAULT 'api',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS scanner_watched_contracts_status_idx
  ON scanner_watched_contracts (status);

CREATE TABLE IF NOT EXISTS scanner_cursors (
  scope text PRIMARY KEY,
  highest_block bigint NOT NULL DEFAULT 0,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS scanner_webhook_config (
  id boolean PRIMARY KEY DEFAULT true,
  webhook_url text NOT NULL,
  signing_secret text NOT NULL,
  is_active boolean NOT NULL DEFAULT true,
  source text NOT NULL DEFAULT 'api',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT scanner_webhook_config_singleton CHECK (id = true)
);

CREATE TABLE IF NOT EXISTS webhook_events (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  event_type text NOT NULL,
  scope text NOT NULL,
  tx_hash text NOT NULL,
  block_number bigint NOT NULL,
  block_timestamp bigint NOT NULL,
  payload jsonb NOT NULL,
  dedupe_key text NOT NULL UNIQUE,
  status text NOT NULL DEFAULT 'pending',
  attempt_count integer NOT NULL DEFAULT 0,
  next_attempt_at timestamptz NOT NULL DEFAULT now(),
  delivered_at timestamptz,
  last_error text,
  last_response_code integer,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS webhook_events_dispatch_idx
  ON webhook_events (status, next_attempt_at, created_at);

CREATE TABLE IF NOT EXISTS webhook_delivery_attempts (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  webhook_event_id uuid NOT NULL REFERENCES webhook_events(id) ON DELETE CASCADE,
  attempt_number integer NOT NULL,
  request_headers jsonb NOT NULL,
  request_body jsonb NOT NULL,
  response_code integer,
  response_body text,
  error_message text,
  duration_ms integer,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS webhook_delivery_attempts_unique_attempt
  ON webhook_delivery_attempts (webhook_event_id, attempt_number);

CREATE TABLE IF NOT EXISTS scanner_settings (
  key text PRIMARY KEY,
  value jsonb NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);
