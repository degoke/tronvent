# Tronvent

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
- **TRC-20 transfers** — `Transfer` events on watched token contracts (USDT is seeded by default)

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
| **No false negatives** | A real watched address is *never* missed. |
| **Rare false positives** | ~1 in 1,000 non-watched addresses may pass the filter. These are harmless: the event is still deduplicated and delivered; you simply receive a webhook you can ignore. |
| **Memory efficient** | Millions of addresses fit in a few megabytes instead of a multi-gigabyte hash map. |

The filter reloads from Postgres on startup, on `LISTEN/NOTIFY` when addresses change via the admin API, and on a periodic safety-net interval (`STATE_RESYNC_INTERVAL_SECONDS`).

### Reliability

- **Postgres outbox** — events are persisted before delivery; crashes do not lose matches.
- **Webhook retries** — exponential backoff (1 m → 2 h) up to `WEBHOOK_MAX_ATTEMPTS` (default 8). Every attempt is logged in `webhook_delivery_attempts`.
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

## Quick start

### 1. Run database migrations

```bash
psql "$DATABASE_URL" -f migrations/001_scanner_schema.up.sql
```

### 2. Configure environment

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

### 3. Run locally

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

### 4. Register watches and webhook

```bash
BASE=http://localhost:8080
AUTH="Authorization: Bearer $ADMIN_API_TOKEN"

# Watch a deposit address
curl -s -X POST "$BASE/api/v1/addresses" \
  -H "$AUTH" -H "Content-Type: application/json" \
  -d '{"address":"TXYZ..."}'

# Watch a TRC-20 contract (USDT on mainnet is bootstrapped automatically)
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

## Docker

```bash
docker build -t tronvent:local .
docker run --rm -p 8080:8080 \
  -e DATABASE_URL="$DATABASE_URL" \
  -e TRONGRID_API_KEY_SCANNER="$TRONGRID_API_KEY_SCANNER" \
  -e TRONGRID_BASE_URL="https://api.trongrid.io" \
  -e ADMIN_API_TOKEN="$ADMIN_API_TOKEN" \
  -e WEBHOOK_URL="$WEBHOOK_URL" \
  -e WEBHOOK_SIGNING_SECRET="$WEBHOOK_SIGNING_SECRET" \
  tronvent:local
```

Pre-built images are published to `ghcr.io/degoke/tronvent` on release.

---

## Kubernetes (Helm)

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
| `TRONGRID_API_KEY_SCANNER` | TronGrid API key (`TRON-PRO-API-KEY` header) |

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
| `TRON_TRC20_CONTRACTS` | USDT mainnet | Comma-separated contracts to seed on first boot |

### Webhook delivery

| Variable | Default | Description |
|---|---|---|
| `WEBHOOK_URL` | — | Bootstrap webhook URL (overridden by admin API) |
| `WEBHOOK_SIGNING_SECRET` | — | HMAC-SHA256 signing secret |
| `WEBHOOK_MAX_ATTEMPTS` | `8` | Max delivery attempts per event |
| `WEBHOOK_POLL_INTERVAL_MS` | `1000` | Outbox poll interval |
| `WEBHOOK_HTTP_TIMEOUT_SECONDS` | `30` | Delivery HTTP timeout |

### Server

| Variable | Default | Description |
|---|---|---|
| `HEALTH_PORT` | `8080` | HTTP port (health, metrics, admin API) |
| `ADMIN_API_TOKEN` | — | Bearer token for `/api/v1/*` (required for admin API) |
| `STATE_RESYNC_INTERVAL_SECONDS` | `60` | Periodic watchlist reload safety net |
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
| `PUT` | `/api/v1/webhook` | Set webhook URL and signing secret |
| `GET` | `/api/v1/webhook` | Get webhook config (secret not returned) |
| `GET` | `/api/v1/runtime` | Cursors, watchlist counts, active contracts |
| `POST` | `/api/v1/retries/block` | Replay a single block |
| `POST` | `/api/v1/retries/range` | Replay a block range |
| `GET` | `/api/v1/retries` | List retry jobs |

Address and contract changes propagate to the in-memory Bloom filter immediately via Postgres `NOTIFY` — no restart required.

---

## Webhook payload

Each delivery is a `POST` with JSON body and signed headers:

```
X-Tronvent-Event-Id:    <uuid>
X-Tronvent-Event-Type:  TRX | TRC20
X-Tronvent-Timestamp:   <unix seconds>
X-Tronvent-Signature:   sha256=<hex hmac>
Content-Type:           application/json
```

**Body example:**

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "type": "TRC20",
  "txHash": "abc123...",
  "fromAddress": "TXyz...",
  "toAddress": "TAbc...",
  "amount": "1000000",
  "tokenContractAddress": "TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t",
  "blockNumber": 65432100,
  "blockTimestamp": 1719234567000,
  "confirmations": 5
}
```

For TRC-20, `amount` is the raw token value (check contract decimals). For TRX, it is a decimal string in TRX units.

### Verify signatures

The signature is `HMAC-SHA256(secret, timestamp + "." + raw_body)`:

```go
mac := hmac.New(sha256.New, []byte(secret))
mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
mac.Write([]byte("."))
mac.Write(rawBody)
expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
```

Reject requests where the timestamp is too old (replay protection) or the signature does not match.

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
