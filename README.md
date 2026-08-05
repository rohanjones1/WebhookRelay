# 🪝 Webhook Relay

A high-throughput, fault-tolerant webhook delivery microservice. It sits between an event publisher and third-party consumer endpoints to guarantee **at-least-once delivery** — decoupling HTTP ingestion from background delivery, with automatic retries, exponential backoff, and a dead-letter queue for events that ultimately fail.

## Why this exists

Without a relay, if a customer's server is down or times out when you call their webhook endpoint, the request is just... gone — usually surfacing only as a line in a log, requiring manual intervention to notice and resend. Webhook Relay accepts the event immediately (sub-10ms), persists it, and takes responsibility for getting it delivered — retrying on failure and giving full visibility into what happened and why.

## Architecture

```text
[ Client / Publisher ]
          │
          │ 1. POST /api/v1/webhooks
          ▼
┌───────────────────┐
│   Fiber (API)     │ ── 2. INSERT status='PENDING' ──► [ Postgres ]
└─────────┬─────────┘
          │ 3. Enqueue job ("webhook:deliver")
          ▼
┌───────────────────┐
│   Asynq Client    │ ── 4. Push payload to memory ───► [    Redis    ]
└─────────┬─────────┘
          │
          └─ 5. Return HTTP 202 Accepted (~5-10ms)

┌───────────────────┐
│   Asynq Worker    │ ── 6. HTTP POST ───────► [ Target Customer Server ]
└─────────┬─────────┘
          │
   ┌──────┴──────────────────────────┐
   │                                 │
[ SUCCESS: 2xx ]            [ FAILURE: 4xx/5xx/timeout ]
   │                                 │
   ▼                                 ▼
Postgres: status="DELIVERED"   Re-queue with exponential backoff
                               (10s → 1m → 5m → 30m). After 5
                               attempts ──► status="FAILED" (DLQ)
```

Every delivery attempt — success or failure — is also written to an append-only `delivery_attempts` audit table, independent of the webhook's current state, so full history is queryable even after the event reaches a terminal status.

## Tech stack

| Layer              | Technology                                | Why                                                                          |
| :----------------- | :---------------------------------------- | :--------------------------------------------------------------------------- |
| Language           | Go 1.22+                                  | Native concurrency, low memory footprint, built for high-throughput I/O      |
| HTTP framework     | [Fiber v2](https://gofiber.io/)           | `fasthttp`-based, sub-10ms routing                                           |
| Queue / dispatcher | [Asynq](https://github.com/hibiken/asynq) | Background task queue with retry/backoff/DLQ primitives                      |
| Queue storage      | Redis 7                                   | Backing store for Asynq                                                      |
| Database           | PostgreSQL                                | Durable state + audit log                                                    |
| DB driver          | [pgx/v5](https://github.com/jackc/pgx)    | Direct SQL, no ORM                                                           |
| Containerization   | Docker + Docker Compose                   | Local parity across API, worker, Redis, Postgres, and the Asynqmon dashboard |
| Load testing       | k6                                        | Verifying throughput/latency claims                                          |

## Features

- ✅ Sub-10ms ingestion, decoupled from delivery via a background queue
- ✅ Idempotent ingestion — duplicate `idempotency_key` requests are safely deduplicated at the DB level
- ✅ Exponential backoff retries (10s → 1m → 5m → 30m), configurable per-event `max_attempts`
- ✅ Dead-letter handling — events that exhaust retries are marked `FAILED` rather than silently dropped
- ✅ Manual DLQ replay — `POST /api/v1/webhooks/:id/replay` resets a `FAILED` event and re-attempts delivery on demand
- ✅ Outbound HMAC-SHA256 request signing (`X-Webhook-Signature`, Stripe-style, replay-protected) — optional inbound verification too
- ✅ Full delivery audit trail — every attempt (status code, error, duration) is recorded
- ✅ Status lookup API for tracking any event's delivery state
- 🔲 Multi-tenant endpoint subscriptions — planned

## Quickstart

```bash
git clone <this-repo>
cd webhook-relay
cp .env.example .env
docker compose up --build
```

Apply database migrations (requires [golang-migrate](https://github.com/golang-migrate/migrate)):

```bash
export DATABASE_URL="postgres://webhook_relay:webhook_relay@localhost:5432/webhook_relay?sslmode=disable"
migrate -database "$DATABASE_URL" -path migrations up
```

Send a test event:

```bash
curl -X POST localhost:8080/api/v1/webhooks \
  -H 'Content-Type: application/json' \
  -d '{"idempotency_key":"test-001","target_url":"https://httpbin.org/status/200","payload":{"hello":"world"}}'
```

Check its status:

```bash
curl localhost:8080/api/v1/webhooks/<id>
```

Watch queue activity in the Asynqmon dashboard at [localhost:8081](http://localhost:8081).

## API

| Method | Path                          | Description                                                         |
| :----- | :---------------------------- | :------------------------------------------------------------------ |
| `POST` | `/api/v1/webhooks`            | Submit a new event for delivery                                     |
| `GET`  | `/api/v1/webhooks/:id`        | Look up the current status of an event                              |
| `POST` | `/api/v1/webhooks/:id/replay` | Manually re-attempt a `FAILED` event (resets attempts, re-enqueues) |
| `GET`  | `/healthz`                    | Liveness check                                                      |

**`POST /api/v1/webhooks`** body:

```json
{
  "idempotency_key": "unique-per-event-string",
  "target_url": "https://customer-server.example.com/webhook",
  "payload": { "any": "json" },
  "event_type": "order.created"
}
```

## Verified behavior

The full lifecycle has been run end-to-end locally:

- An event pointed at a `200`-returning endpoint reaches `DELIVERED` on the first attempt.
- An event pointed at a `500`-returning endpoint retries at 10s, 1m, 5m, and 30m intervals, incrementing `attempts` and recording each failure in `delivery_attempts`, before being marked `FAILED` after exhausting `max_attempts`.

## Load Testing (k6)

Load tests target `POST /api/v1/webhooks` — the ingestion path only. Since Webhook Relay's core design decouples ingestion from delivery (the API returns `202 Accepted` after a DB insert + queue enqueue, before any delivery attempt happens), this measures how fast events can be durably accepted, not delivery throughput. Delivery is handled asynchronously by the worker pool and is bounded separately by the backoff schedule and target endpoint availability.

All tests run locally via Docker Compose (Postgres, Redis, API, worker) on a single MacBook Pro, against a local Go sink server standing in for a real customer endpoint (instant `200 OK`, no real network latency). Test scripts: [`webhook-relay-load-test.js`](./load-test/webhook-relay-load-test.js) (realistic concurrent-client simulation) and [`webhook-relay-ceiling-test.js`](./load-test/webhook-relay-ceiling-test.js) (throughput ceiling discovery).

### Results

| Scenario                            | Rate        | p90 latency | p95 latency | Error rate | Notes                                                                       |
| ----------------------------------- | ----------- | ----------- | ----------- | ---------- | --------------------------------------------------------------------------- |
| Realistic load (500 concurrent VUs) | 2,268 req/s | —           | 22.15ms     | 0%         | VU-bound (think-time limited), not a throughput ceiling                     |
| Sustained ceiling                   | 1,300 req/s | 3.07ms      | 28.65ms     | 0%         | Stable — Postgres CPU flat (~130–180%) over 3 min hold                      |
| Beyond ceiling                      | 2,000 req/s | 11.66ms     | 101.83ms    | 0%         | Degrading — Postgres CPU climbed 180%→220% over 3 min, latency tail growing |

**Bottleneck identified:** at sustained rates above ~1,300–1,500 req/s, the Postgres container becomes CPU-bound (confirmed via `docker stats` during the run) before the Go/Fiber API layer shows any strain. Median latency stays low even under the degrading scenario (req/s=2,000, med≈2ms) — the bottleneck shows up first in tail latency (p95/p99), consistent with connection-pool queuing or lock contention rather than raw request-handling capacity.

**Methodology note:** ceiling was found via `k6`'s `ramping-arrival-rate` executor (directly controls request rate, independent of VU count/think-time), bisecting between a known-stable rate and a known-degrading rate until CPU and latency both plateaued rather than climbed over a 3-minute hold.

**Not yet measured:** effect of tuning Postgres `MaxConns` / indexing on the ceiling; true delivery-path throughput (bounded separately by target endpoint latency and the retry/backoff schedule).

## Roadmap

See [`webhook-relay-context.md`](./webhook-relay-context.md) for the full phased build plan, known design gaps, and planned resume-impact additions (Prometheus metrics, k6 load test results, CI).

## License

MIT
