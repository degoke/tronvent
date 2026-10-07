# Tronvent

![Tronvent](tronvent.png)

[![License: Elastic-2.0](https://img.shields.io/badge/License-Elastic--2.0-005571.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![TRON](https://img.shields.io/badge/TRON-TronGrid-red?logo=bitcoin&logoColor=white)](https://www.trongrid.io/)
[![Docker](https://img.shields.io/badge/Docker-ghcr.io%2Fdegoke%2Ftronvent-2496ED?logo=docker&logoColor=white)](https://ghcr.io/degoke/tronvent)
[![Helm](https://img.shields.io/badge/Helm-chart-0f1689?logo=helm&logoColor=white)](charts/tronvent)

**Never miss an important transaction on TRON — without running your own node.**

Tronvent is a self-hosted TRON chain monitor. Register the wallet addresses and TRC-20 contracts you care about, and Tronvent polls [TronGrid](https://www.trongrid.io/) on your behalf, filters every block for relevant activity, and delivers signed webhooks to your application in near real time.

---

## The problem

Building reliable TRON deposit/withdrawal detection is harder than it looks:

1. **TronGrid has no push subscription.** The hosted TronGrid API is REST-only. There is no WebSocket or server-sent event stream to subscribe to transactions or contract events. The [official TRON docs](https://developers.tron.network/docs/exchangewallet-integrate-with-the-tron-network) describe monitoring addresses by *continuously polling* account history APIs. TronGrid maintainers have [confirmed there is no WebSocket API](https://github.com/tronprotocol/java-tron/issues/5811); real-time push requires running your own full node with ZeroMQ event subscription — infrastructure most teams do not want to operate.

2. **The chain moves fast.** TRON produces a new block roughly every **3 seconds**. To catch inbound TRX transfers you must scan every new block. For TRC-20 tokens you additionally query contract event indexes, which can lag block data by several seconds.

3. **Scale adds up quickly.** A wallet or exchange may watch thousands or millions of deposit addresses. Polling per-address APIs does not scale. Scanning full blocks and filtering locally is the practical approach — but someone still has to run that loop reliably, persist cursors, handle restarts, and deliver events to your backend.

Tronvent exists to do exactly that: one service that watches your addresses and contracts, keeps up with the chain, and pushes matched events to you via webhooks.

---

## What Tronvent does

| You provide | Tronvent handles |
|---|---|
| TRON addresses to watch | Polls TronGrid every ~3 s for new blocks |
| TRC-20 contracts (e.g. USDT) | Fetches `Transfer` events per contract |
| A webhook URL + signing secret | Delivers signed JSON payloads with retries |
| PostgreSQL + TronGrid API key | Persists cursors, outbox, and watchlists |

**Supported activity today:**

- **TRX transfers** — native TRX `TransferContract` transactions where your address is sender or receiver
- **TRC-20 transfers** — `Transfer` events on watched token contracts (none are seeded by default)

When a match is found, Tronvent writes to a durable Postgres outbox and a background worker POSTs a signed webhook to your endpoint.

---

## How it works

```mermaid
flowchart LR
  TG[TronGrid API]
  P[Block Poller]
  BF[Bloom Filter]
  PG[(PostgreSQL)]
  W[Webhook Worker]
  APP[Your Application]

  TG -->|blocks + TRC-20 events| P
  P -->|Contains?| BF
  P -->|matched events| PG
  PG -->|outbox| W
  W -->|signed POST| APP
  APP -->|admin API| PG
  PG -->|LISTEN/NOTIFY| BF
```

### Polling loop

Every `TRON_POLL_INTERVAL_MS` (default **3000 ms**, aligned with TRON block time):

1. **Fetch chain tip** — `GET /wallet/getnowblock` to learn the latest block height.
2. **Apply confirmation lag** — only scan blocks up to `latest − TRON_REQUIRED_CONFIRMATIONS` so events are not published from blocks that might still reorganize.
3. **Scan TRX** — batch-fetch blocks via `/wallet/getblockbylimitnext` (up to 100 blocks per request), inspect every transaction, and match `TransferContract` senders/receivers against your watchlist.
4. **Scan TRC-20** — for each watched contract, query `/v1/contracts/{address}/events` with fingerprint pagination. An extra `TRON_TRC20_EVENT_CONFS` offset accounts for TronGrid's asynchronous event indexing.
5. **Advance cursors** — each scope (`TRX`, plus one cursor per TRC-20 contract) stores its highest scanned block in Postgres. On restart, scanning resumes from the last committed block.
6. **Enqueue webhooks** — matched events are deduplicated and written to `webhook_events`. The delivery worker claims pending rows and POSTs to your URL.

TRX and each TRC-20 contract scan **concurrently** with independent cursors, so one slow contract cannot block native TRX detection.

### Bloom filter for address lookup

Every watched address is held in an in-memory **Bloom filter** — a probabilistic data structure that answers “is this address probably in my set?” in O(1) time with minimal memory.

Tronvent tunes the filter for **1 million addresses at a 0.1% false-positive rate** (~14 MB of bit storage, 10 hash functions). Properties:

| Property | Behavior |
|---|---|
| **No false negatives** | A real watched address is *never* missed while it remains in the Bloom filter. |
| **Rare false positives** | ~1 in 1,000 non-watched addresses may pass the filter. Before a webhook is enqueued, the poller confirms the address is **actively** watched in Postgres (batch lookup per scan batch). False positives and deactivated rows do not produce webhooks. |
| **Memory efficient** | Millions of addresses fit in a few megabytes instead of a multi-gigabyte hash map. |

The filter reloads from Postgres on startup, when addresses are **added or reactivated** (`LISTEN/NOTIFY` on `scanner_addresses_changed`), and after the notification listener reconnects. **Removing** an address only updates Postgres (`status = inactive`); the Bloom filter is not rebuilt (avoids full reloads on large watchlists). Deactivated addresses may remain in the filter until the next full reload, but Postgres confirmation prevents webhooks. An optional periodic safety-net reload can be enabled with `STATE_RESYNC_INTERVAL_SECONDS`; `0` disables it.

If Postgres confirmation fails after retries, or the scan context is cancelled during confirm (e.g. shutdown), the poller **fails open** (enqueues the event) so transient DB errors and graceful stop do not drop in-flight matches.

### Reliability

- **Postgres outbox** — events are persisted before delivery; crashes do not lose matches.
- **Webhook retries** — [Standard Webhooks](https://www.standardwebhooks.com) delivery schedule (up to 10 attempts over ~75h, with jitter). Every attempt is logged in `webhook_delivery_attempts`.
- **Startup reconciliation** — if the scanner was offline and fell far behind, large gaps are enqueued as background block-range jobs instead of blocking the live poll loop.
- **Admin replay** — manually re-scan a block or range via the admin API when you need to backfill.
- **Prometheus metrics** — `/metrics` exposes blocks scanned, matches found, events published, and watchlist size.

### Latency and throughput

With tuned settings Tronvent stays within a few blocks of chain tip and delivers webhooks shortly after your confirmation threshold:

| Setting | Default | Low-latency example |
|---|---|---|
| `TRON_POLL_INTERVAL_MS` | `3000` | `3000` |
| `TRON_REQUIRED_CONFIRMATIONS` | `20` | `5` |
| `WEBHOOK_POLL_INTERVAL_MS` | `1000` | `1000` |

At **5 confirmations** and a 3 s block time, a transaction is eligible for scanning ~15 s after inclusion. The next poll tick and webhook dispatch add a few more seconds — typically **under 30 seconds from the confirmation you configured**.

The default of 20 confirmations trades latency for stronger finality (~60 s of block time). Adjust based on your risk tolerance.

Tronvent is designed to handle **thousands to millions of watched addresses** and the full transaction volume of each block without falling more than your configured confirmation depth behind tip under normal TronGrid rate limits.

---

## Prerequisites

- **PostgreSQL 16+** with the `pgcrypto` extension (for `gen_random_uuid()`)
- **TronGrid API key** — required for mainnet; [get one free at trongrid.io](https://www.trongrid.io/). Shasta and Nile testnets currently work without a key but setting one is recommended.
- **Go 1.26+** (local builds only)

---

## Getting started

Choose a deployment path below. All container-based paths use published artifacts; you do not need to clone this repository.

### Docker Compose

Create a `docker-compose.yml` file with this sample:

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: tronvent
      POSTGRES_USER: tronvent
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-tronvent-local}
    volumes:
      - tronvent-postgres:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U tronvent -d tronvent"]
      interval: 5s
      timeout: 5s
      retries: 10

  migrate:
    image: ghcr.io/degoke/tronvent:1.0.0
    command: ["/app/migrate"]
    environment:
      DATABASE_URL: postgres://tronvent:${POSTGRES_PASSWORD:-tronvent-local}@postgres:5432/tronvent
    depends_on:
      postgres:
        condition: service_healthy

  tronvent:
    image: ghcr.io/degoke/tronvent:1.0.0
    ports:
      - "8080:8080"
    env_file: .env
    environment:
      DATABASE_URL: postgres://tronvent:${POSTGRES_PASSWORD:-tronvent-local}@postgres:5432/tronvent
    depends_on:
      migrate:
        condition: service_completed_successfully

volumes:
  tronvent-postgres:
```

Then create a `.env` file with the required values:

```bash
TRONGRID_API_KEY_SCANNER=your-trongrid-api-key
ADMIN_API_TOKEN=a-long-random-secret
WEBHOOK_URL=https://your-app.example.com/webhooks/tron
WEBHOOK_SIGNING_SECRET=another-long-random-secret
```

Then start the complete local stack:

```bash
TRONVENT_IMAGE=ghcr.io/degoke/tronvent:1.0.0 docker compose up -d
docker compose logs -f tronvent
```

Tronvent is available at `http://localhost:8080`. Stop the stack with `docker compose down`; add `-v` if you also want to remove the local PostgreSQL volume.

### Docker

```bash
docker pull ghcr.io/degoke/tronvent:1.0.0
docker run --rm -p 8080:8080 \
  -e DATABASE_URL="$DATABASE_URL" \
  -e TRONGRID_API_KEY_SCANNER="$TRONGRID_API_KEY_SCANNER" \
  -e TRONGRID_BASE_URL="https://api.trongrid.io" \
  -e ADMIN_API_TOKEN="$ADMIN_API_TOKEN" \
  -e WEBHOOK_URL="$WEBHOOK_URL" \
  -e WEBHOOK_SIGNING_SECRET="$WEBHOOK_SIGNING_SECRET" \
  ghcr.io/degoke/tronvent:1.0.0
```

Pre-built images are published to `ghcr.io/degoke/tronvent` on release.

This route assumes PostgreSQL and migrations are already available. For a complete PostgreSQL + migration + Tronvent stack, use Docker Compose above.

---

### Kubernetes (Helm)

Install a released chart from GHCR:

```bash
helm registry login ghcr.io
helm upgrade --install tronvent oci://ghcr.io/degoke/charts/tronvent \
  --version 1.0.0 \
  --namespace tronvent --create-namespace \
  --set secrets.databaseUrl="$DATABASE_URL" \
  --set secrets.tronGridApiKey="$TRONGRID_API_KEY_SCANNER" \
  --set secrets.adminApiToken="$ADMIN_API_TOKEN" \
  --set secrets.webhookSigningSecret="$WEBHOOK_SIGNING_SECRET" \
  --set config.tronGridBaseUrl="https://api.trongrid.io" \
  --set config.webhookUrl="$WEBHOOK_URL"
```

The release workflow publishes both the Docker image and Helm chart when a `v*` tag is pushed. The chart is available at `oci://ghcr.io/degoke/charts/tronvent`.

```bash
# Run migrations first (see above)

helm upgrade --install tronvent ./charts/tronvent \
  --namespace tronvent --create-namespace \
  --set secrets.databaseUrl="$DATABASE_URL" \
  --set secrets.tronGridApiKey="$TRONGRID_API_KEY_SCANNER" \
  --set secrets.adminApiToken="$ADMIN_API_TOKEN" \
  --set secrets.webhookSigningSecret="$WEBHOOK_SIGNING_SECRET" \
  --set config.tronGridBaseUrl="https://api.trongrid.io" \
  --set config.webhookUrl="$WEBHOOK_URL"
```

For production, create a Kubernetes Secret ahead of time and reference it:

```yaml
secrets:
  create: false
  existingSecret: tronvent-secrets
```

See `charts/tronvent/values.yaml` for all configurable values including resource limits, probes, ingress, and Prometheus ServiceMonitor.

### Local development

#### 1. Run database migrations

```bash
make migrate
```

Requires `DATABASE_URL`. Applies all pending files under `migrations/` (tracked in `schema_migrations`). Notable migrations: `002` (webhook endpoints fanout), `003` (direction-specific default `event_types`), `004` (rewrites stored `transaction.trx` / `transaction.trc20` subscriptions to the four supported types), `005` (rewrites outbox `dedupe_key` to include event type and transfer leg).

#### 2. Configure environment

```bash
export DATABASE_URL="postgres://user:pass@localhost:5432/tronvent"
export TRONGRID_API_KEY_SCANNER="your-trongrid-api-key"
export ADMIN_API_TOKEN="a-long-random-secret"
export WEBHOOK_URL="https://your-app.example.com/webhooks/tron"
export WEBHOOK_SIGNING_SECRET="another-long-random-secret"

# Network — pick one:
export TRONGRID_BASE_URL="https://api.trongrid.io"          # Mainnet
# export TRONGRID_BASE_URL="https://api.shasta.trongrid.io" # Shasta testnet
# export TRONGRID_BASE_URL="https://nile.trongrid.io"       # Nile testnet
```

#### 3. Run locally

```bash
git clone https://github.com/degoke/tronvent.git
cd tronvent
make build
./bin/tronvent
```

Or with live reload during development:

```bash
air   # requires github.com/air-verse/air
```

#### 4. Register watches and webhook

```bash
BASE=http://localhost:8080
AUTH="Authorization: Bearer $ADMIN_API_TOKEN"

# Watch a deposit address
curl -s -X POST "$BASE/api/v1/addresses" \
  -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"address":"TXYZ..."}'

# Watch a TRC-20 contract
curl -s -X POST "$BASE/api/v1/contracts" \
  -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"contractAddress":"TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t","tokenSymbol":"USDT"}'

# Configure webhook delivery
curl -s -X PUT "$BASE/api/v1/webhook" \
  -H "$AUTH" -H "Content-Type: application/json" \
  -d "{\"webhookUrl\":\"$WEBHOOK_URL\",\"signingSecret\":\"$WEBHOOK_SIGNING_SECRET\"}"

# Check scanner state
curl -s "$BASE/api/v1/runtime" -H "$AUTH" | jq
```

Health check: `GET /health`  
Metrics: `GET /metrics`

---

## Network selection

| Network | `TRONGRID_BASE_URL` | Notes |
|---|---|---|
| **Mainnet** | `https://api.trongrid.io` | API key required |
| **Shasta testnet** | `https://api.shasta.trongrid.io` | [Faucet](https://www.trongrid.io/faucet) |
| **Nile testnet** | `https://nile.trongrid.io` | [Faucet](https://nileex.io/join/getJoinPage) |

Set `TRONGRID_BASE_URL` to match the network your addresses and contracts live on. Use separate Tronvent deployments (and databases) per network.

Reference: [TRON network endpoints](https://developers.tron.network/docs/connect-to-the-tron-network)

---

## Configuration reference

### Required

| Variable | Description |
|---|---|
| `DATABASE_URL` | PostgreSQL connection string |
| `TRONGRID_API_KEY_SCANNER` | TronGrid API key(s), comma-separated for a round-robin pool (`TRON-PRO-API-KEY` header) |

### TronGrid & polling

| Variable | Default | Description |
|---|---|---|
| `TRONGRID_BASE_URL` | `https://api.trongrid.io` | TronGrid HTTP endpoint |
| `TRON_POLL_INTERVAL_MS` | `3000` | Poll tick interval (ms) |
| `TRON_START_BLOCK` | `0` | Start block; `0` = resume cursor or begin at current tip |
| `TRON_REQUIRED_CONFIRMATIONS` | `20` | Blocks to wait before scanning (finality vs latency) |
| `TRON_TRC20_EVENT_CONFS` | `10` | Extra lag for TRC-20 event index |
| `TRON_TRC20_CURSOR_RETAIN` | `50` | Re-scan recent blocks while event index catches up |
| `TRON_FETCH_CONCURRENCY` | `5` | Parallel TronGrid HTTP requests |
| `TRON_HTTP_TIMEOUT_SECONDS` | `60` | HTTP client timeout |
| `TRON_RECONCILE_BATCH_SIZE` | `1000` | Blocks per startup backfill job |
| `TRON_TRC20_CONTRACTS` | — | Optional comma-separated contracts to seed on first boot |

### Webhook delivery

| Variable | Default | Description |
|---|---|---|
| `WEBHOOK_URL` | — | Bootstrap primary endpoint URL when `webhook_endpoints` is empty |
| `WEBHOOK_SIGNING_SECRET` | — | Optional `whsec_` / `whsk_` key for bootstrap (legacy plaintext is auto-wrapped to `whsec_` if ≥24 bytes); ed25519 key pair generated if omitted |
| `WEBHOOK_MAX_ATTEMPTS` | `10` | Delivery attempts per outbox event before status `dead` (max **10**, Standard Webhooks schedule) |
| `WEBHOOK_POLL_INTERVAL_MS` | `1000` | Outbox poll interval |
| `WEBHOOK_HTTP_TIMEOUT_SECONDS` | `30` | Delivery HTTP timeout |
| `WEBHOOK_NOTIFY_SMTP_HOST` | — | Optional SMTP host for endpoint failure emails |
| `WEBHOOK_NOTIFY_SMTP_PORT` | `587` | SMTP port |
| `WEBHOOK_NOTIFY_SMTP_USER` / `WEBHOOK_NOTIFY_SMTP_PASS` | — | SMTP credentials |
| `WEBHOOK_NOTIFY_SMTP_FROM` | — | From address for failure notifications |

### Server

| Variable | Default | Description |
|---|---|---|
| `HEALTH_PORT` | `8080` | HTTP port (health, metrics, admin API) |
| `ADMIN_API_TOKEN` | — | Bearer token for `/api/v1/*` (required for admin API) |
| `STATE_RESYNC_INTERVAL_SECONDS` | `0` (disabled) | Optional periodic watchlist reload safety net in seconds |
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARN`, `ERROR` |
| `LOG_FORMAT` | auto | `json` or `text` (color when TTY) |

---

## Admin API

All `/api/v1/*` routes require `Authorization: Bearer <ADMIN_API_TOKEN>`.

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/addresses` | Add watched address |
| `GET` | `/api/v1/addresses` | List addresses (`?status=active&limit=50`) |
| `DELETE` | `/api/v1/addresses/{address}` | Deactivate address |
| `POST` | `/api/v1/contracts` | Add watched TRC-20 contract |
| `GET` | `/api/v1/contracts` | List contracts |
| `DELETE` | `/api/v1/contracts/{contractAddress}` | Deactivate contract |
| `PUT` | `/api/v1/webhook` | Set primary webhook endpoint URL (optional signing secret; generated if missing) |
| `GET` | `/api/v1/webhook` | Get webhook config (secret not returned) |
| `GET` | `/api/v1/webhook/endpoints` | List subscriber endpoints |
| `POST` | `/api/v1/webhook/endpoints` | Create endpoint (generates ed25519 keys if omitted) |
| `GET` | `/api/v1/webhook/endpoints/{id}` | Get one endpoint |
| `PATCH` | `/api/v1/webhook/endpoints/{id}` | Update URL, event types, active flag, notify email, or signing key |
| `DELETE` | `/api/v1/webhook/endpoints/{id}` | Remove endpoint |
| `GET` | `/api/v1/webhook/schemas` | JSON Schemas for all event types |
| `GET` | `/api/v1/webhook/schemas/event?type=transaction.trx.received` | One event type schema |
| `GET` | `/api/v1/webhooks?status=failed&limit=50` | List webhook events (`status=dead` or `status=all` are also supported) |
| `GET` | `/api/v1/webhooks/{eventID}/attempts` | List delivery attempts for one webhook event |
| `POST` | `/api/v1/webhooks/{eventID}/retry` | Retry one failed or dead webhook event |
| `POST` | `/api/v1/webhooks/retry-all` | Retry all failed and dead webhook events |
| `GET` | `/api/v1/runtime` | Cursors, watchlist counts, active contracts |
| `POST` | `/api/v1/retries/block` | Replay a single block |
| `POST` | `/api/v1/retries/range` | Replay a block range |
| `GET` | `/api/v1/retries` | List retry jobs |

New and reactivated addresses propagate to the in-memory Bloom filter via Postgres `NOTIFY` (and incremental `Add` on the writer). Deactivating an address does not rebuild the filter; the scanner relies on Postgres confirmation before delivery. Contract watchlist changes still reload the in-memory contract list via `NOTIFY`. No restart required.

---

## Webhook payload

Each delivery is a `POST` with a [Standard Webhooks](https://www.standardwebhooks.com) JSON body and headers:

```
webhook-id:             <uuid> (same as data.id)
webhook-timestamp:      <unix seconds> (delivery attempt time)
webhook-signature:      v1,<base64 hmac>
Content-Type:           application/json
```

**Body example** (`transaction.trc20.received`):

```json
{
  "type": "transaction.trc20.received",
  "timestamp": "2024-06-24T15:04:05.123456789Z",
  "data": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "txHash": "abc123...",
    "fromAddress": "TXyz...",
    "toAddress": "TAbc...",
    "amount": "1000000",
    "tokenContractAddress": "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
    "blockNumber": 65432100,
    "blockTimestamp": 1719234567000,
    "confirmations": 5
  }
}
```

Supported event types (asset + direction relative to watched addresses):

| Type | Meaning |
|------|---------|
| `transaction.trx.received` | Watched address received TRX |
| `transaction.trx.broadcasted` | Watched address sent TRX |
| `transaction.trc20.received` | Watched address received a TRC-20 transfer |
| `transaction.trc20.broadcasted` | Watched address sent a TRC-20 transfer |

**Received** means a watched address is the transfer recipient; **broadcasted** means a watched address is the sender. A self-transfer to the same watched address can emit both event types (two deliveries).

The top-level `timestamp` is when the transfer occurred (block time, ISO 8601 UTC). For TRC-20, `data.amount` is the raw token value (check contract decimals). For TRX, it is a decimal string in TRX units.

### Verify signatures

Signed content is `webhook_id + "." + webhook_timestamp + "." + raw_body`, with `HMAC-SHA256` and a `v1,<base64>` `webhook-signature` header (space-separated when multiple keys are active during rotation). Reject requests outside the timestamp tolerance (replay protection) or when no signature matches.

Signing keys use Standard Webhooks serialization:

- **Preferred (asymmetric):** `whsk_` private (signs `v1a,<base64>` ed25519) and `whpk_` public (returned from the API for verification).
- **Symmetric:** `whsec_` + base64 (signs `v1,<base64>` HMAC-SHA256).

New endpoints default to ed25519. Libraries: [standard-webhooks/libraries](https://github.com/standard-webhooks/standard-webhooks/tree/main/libraries).

### Delivery behavior

Tronvent implements the Standard Webhooks producer guidelines:

- **HTTPS-only** subscriber URLs (private/link-local/metadata hosts blocked at config and connect time).
- **No redirect following** — `3xx` responses are terminal failures.
- **Retries** — up to **`WEBHOOK_MAX_ATTEMPTS`** per outbox row (default **10**, hard-capped at the spec table). Spacing follows the [Standard Webhooks retry schedule](https://www.standardwebhooks.com) (~75h total, with jitter). `429`, `5xx`, `502`, and `504` are retried; `Retry-After` is honored when present. After the last failed attempt the event is marked **`dead`** in the outbox (it is not delivered further until you call the retry API). Other **4xx** and **3xx** responses mark the event **`dead`** immediately (no endpoint disable). Invalid subscriber URL, signing configuration, oversize payload, or inactive endpoint also mark the event **`dead`** (fix config and use retry API).
- **Stuck deliveries** — rows left in `delivering` after a worker crash are reclaimed after **5 minutes** (`attempt_count` is incremented on reclaim).
- **Endpoint auto-disable** — the **subscriber endpoint** (`webhook_endpoints.is_active`) is set to `false` when:
  - the subscriber returns **`410 Gone`** (immediate), or
  - a delivery exhausts **`WEBHOOK_MAX_ATTEMPTS`** retryable failures for that event (chronic failure).
  Disabled endpoints stop receiving new fanout; existing pending rows for that endpoint may still be attempted until they die or you retry them.
- **Re-enable an endpoint** — there is no automatic reactivation. Set `isActive` back to `true`:
  - `PATCH /api/v1/webhook/endpoints/{id}` with `{"isActive": true}` (fix URL or signing key in the same request if needed), or
  - dashboard **Webhooks** → enable **Active** on the primary endpoint, or
  - `PUT /api/v1/webhook` (updates the primary endpoint and sets it active).
  After re-enabling, use `POST /api/v1/webhooks/{eventID}/retry` or **retry-all** for `failed`/`dead` events you still want delivered.
- **Event filtering** — per-endpoint `eventTypes` (default: all four direction-specific types above). An empty stored list is treated the same as the default at delivery time. Unsubscribed events are not enqueued.
- **Fanout** — multiple endpoints via `GET/POST/PATCH/DELETE /api/v1/webhook/endpoints/{id}` (one outbox row per endpoint).
- **Failure notification** — optional `failureNotifyEmail` per endpoint; configure `WEBHOOK_NOTIFY_SMTP_*` to send email when an endpoint is auto-disabled (410 or chronic failure).
- **Payload size** — rejected above **20 KB** at enqueue time.
- **Schemas** — JSON Schema in `internal/webhookpayload/schemas/` (one file per event type); API `GET /api/v1/webhook/schemas` and `GET /api/v1/webhook/schemas/event?type=<eventType>`; OpenAPI sketch in `docs/webhooks/openapi.yaml`.

**Static egress IPs:** configure your firewall from the outbound IPs of the host or NAT gateway running Tronvent (not assigned by the app).

---

## Development

```bash
make help        # list targets
make test        # run tests
make lint        # golangci-lint
make check       # fmt + vet + lint + test + build
make docker      # build local image
```

---

## Architecture at a glance

```
┌─────────────┐     poll blocks/events      ┌──────────────┐
│  TronGrid   │ ◄────────────────────────── │    Poller    │
│  (hosted)   │                             │  + BloomFilter│
└─────────────┘                             └──────┬───────┘
                                                   │ matched events
                                                   ▼
                                            ┌──────────────┐
                                            │  PostgreSQL  │
                                            │  cursors +   │
                                            │  outbox      │
                                            └──────┬───────┘
                                                   │
                     ┌─────────────────────────────┼──────────────────────────┐
                     ▼                             ▼                          ▼
              Webhook Worker               Admin API (:8080)           LISTEN/NOTIFY
              (signed POST)                addresses / contracts       live reload
                     │
                     ▼
              Your application
```

---

## Why not poll TronGrid yourself?

You can — and for a handful of addresses the [per-account history APIs](https://developers.tron.network/docs/exchangewallet-integrate-with-the-tron-network) work fine. Tronvent becomes worthwhile when you need:

- **Many addresses** — block-level scanning + Bloom filter beats N separate account pollers
- **Both TRX and TRC-20** — unified cursors, deduplication, and webhook delivery
- **Operational guarantees** — crash-safe outbox, gap reconciliation, metrics, replay tools
- **No node ops** — TronGrid handles chain access; you run one small Go service + Postgres

The alternative — running a TRON full node with [ZeroMQ event subscription](https://developers.tron.network/docs/use-java-trons-built-in-message-queue-for-event-subscription) — gives true push semantics but requires syncing and maintaining node infrastructure. Tronvent is the middle path: hosted chain access, self-hosted reliability.

---

## License

[Elastic License 2.0](LICENSE)
