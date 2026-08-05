# 🪝 Webhook Relay (`webhook-relay`) — Project Context & Blueprint

> This document is a self-contained project brief. It can be handed to any AI assistant or engineer to understand the project's purpose, architecture, current status, and remaining work — no other context required.

---

## 1. Executive Summary & Goals

### Project Vision
**Webhook Relay** is a high-throughput, fault-tolerant webhook delivery microservice. It sits between an event publisher (e.g., an e-commerce platform) and third-party consumers (e.g., customer endpoints) to guarantee **at-least-once delivery** of webhook events without data loss.

### Primary Problem Solved
Without a relay, if a target customer server is down or times out, the calling API loses the request in logs, requiring manual intervention (15+ mins per incident). Webhook Relay decouples HTTP ingestion from background delivery, maintaining a persistent state log and executing automated retries with exponential backoff.

### Resume & Career Objective
Targeted at Software Engineering (SWE) internship applications for a 2nd-year CS student. Demonstrates mastery of:
- Asynchronous microservice architecture & messaging queues.
- Distributed task processing & retry mechanics (Exponential Backoff, Dead Letter Queues).
- High-concurrency I/O performance in Go.
- Database state management and containerized deployment.

---

## 2. Tech Stack & Architectural Decisions

| Layer | Technology | Decision Rationale |
| :--- | :--- | :--- |
| **Language** | **Go (Golang 1.22+)** | Native concurrency (`goroutines`), low memory footprint, ideal for high throughput (10k+ RPS). |
| **HTTP Framework** | **Fiber v2** | Built on `fasthttp`, providing ultra-fast request routing and minimal sub-10ms response latency. |
| **Queue / Dispatcher** | **Asynq** | Go library for background task queuing, retries, exponential backoff, and DLQ handling out of the box. |
| **In-Memory Storage** | **Redis (7.x)** | Fast in-memory queue storage backend for Asynq (Docker locally, Upstash / Redis Cloud in prod). |
| **Primary Database** | **PostgreSQL (Neon)** | Relational database for persistent audit logging; Neon provides serverless scaling and DB branching. |
| **Containerization** | **Docker & Docker Compose** | Ensures local development parity across API, Worker, Redis, Postgres, and Asynqmon dashboard. |
| **Load Testing** | **k6** | Scripted load testing to measure p95/p99 latencies and verify throughput claims. |

---

## 3. Core Architecture & Request Lifecycle

```text
[ Client / Publisher ]
          │
          │ 1. POST /api/v1/webhooks
          ▼
┌───────────────────┐
│   Fiber (API)     │ ── 2. INSERT status='PENDING' ──► [ Postgres (Neon) ]
└─────────┬─────────┘
          │ 3. Enqueue job ("webhook:deliver")
          ▼
┌───────────────────┐
│   Asynq Client    │ ── 4. Push payload to memory ───► [    Redis 7    ]
└─────────┬─────────┘                                   [ Task Storage  ]
          │                                                    ▲
          └─ 5. Return HTTP 202 Accepted (In ~5-10ms)          │
                                                               │
┌──────────────────────────────────────────────────────────────┘
│ 6. Pull pending task
▼
┌───────────────────┐
│   Asynq Worker    │ ── 7. HTTP POST request ───────► [ Target Customer Server ]
└─────────┬─────────┘
          │
   ┌──────┴──────────────────────────┐
   │                                 │
[ SUCCESS: 2xx ]            [ FAILURE: 4xx/5xx/Timeout ]
   │                                 │
   ▼                                 ▼
Update Postgres:            Re-queue in Redis with Exponential
Status = "DELIVERED"        Backoff (10s, 1m, 5m...). Increment attempt.
                            If retries exhausted (>5) ──► Mark "FAILED" (DLQ)
```

---

## 4. Repository / Directory Structure

```text
webhook-relay/
├── cmd/
│   ├── api/          # Fiber Web Server entry point (Ingestion)
│   └── worker/       # Asynq Worker Daemon entry point (Background processor)
├── internal/
│   ├── config/       # Environment variable parsing
│   ├── database/     # Postgres connection & Redis Asynq options
│   ├── models/       # Struct definitions & DB queries
│   └── queue/        # Asynq task definitions & handler callbacks
├── migrations/       # SQL migration scripts (.sql)
├── Dockerfile        # Multi-stage build for microservice binaries
├── docker-compose.yml# Local infrastructure orchestrator
├── .env.example      # Public template for environment configuration
├── .gitignore        # Ignores compiled binaries and private .env
└── README.md         # Recruiter-facing documentation
```

---

## 5. Known Gaps / Design Issues to Resolve

These are open design questions and correctness gaps identified during planning, not yet implemented:

1. **~~No idempotency / signature verification.~~ HMAC signing is now implemented** (see Section 8.2). Idempotency was already handled via unique constraint from Phase 1.

2. **No deduplication key.** ~~Resolved~~ — `idempotency_key` has a unique constraint; duplicate inserts are caught and return `202 already_accepted`.

3. **Retry logic spec — mostly resolved.** Backoff schedule is concrete: 10s → 1m → 5m → 30m, `max_attempts` defaults to 5 but is now overridable per-request (1-10) for testing/demo purposes. No jitter yet (not implemented — low priority, only matters at real scale with many simultaneous retries). "FAILED" means terminal/DLQ; **manual replay is now implemented** (see Section 8.2).

4. **No delivery timeout/circuit breaking.** Still open. Per-request timeout exists (`http.Client{Timeout: 5s}`), but no circuit breaker across repeated failures to the same target — a target that's consistently down still gets hammered by the full worker pool if many events target it.

5. **Multi-tenancy not yet modeled.** Still open. Single `target_url` per event; no per-customer endpoint registration.

---

## 6. Resume-Impact Enhancements (Planned)

To maximize this project's value for SWE internship applications:

- **Observability**: structured logging (zerolog/zap) + a `/metrics` Prometheus endpoint (queue depth, delivery success rate, p95 latency).
- **Load test results in README**: a graph or table of RPS vs p95/p99 latency from k6 — the single most concrete, resume-verifiable claim (e.g. "sustained 10k RPS at <15ms p95").
- **Asynqmon dashboard screenshot** in the README as visual proof the queue/DLQ actually works.
- **GitHub Actions CI**: run `go test ./...` plus a `docker compose up` smoke test, to signal engineering maturity beyond "it ran on my laptop."

---

## 7. Build Roadmap

### Phase 1 — Core Path (end-to-end happy path)
1. Postgres schema: `webhooks` table (id, idempotency_key, payload, status, attempts, target_url, created_at, updated_at) + migration tooling (`golang-migrate` or `goose`).
2. Fiber API: `POST /api/v1/webhooks` → insert PENDING → enqueue Asynq task → return 202.
3. Asynq worker: dequeue → HTTP POST to target → update status on success/failure.
4. Docker Compose wiring everything together locally. Goal: one webhook flowing end-to-end before anything else.

### Phase 2 — Reliability
5. Exponential backoff + jitter config in Asynq, DLQ after N attempts.
6. Idempotency key uniqueness constraint.
7. HMAC signing (inbound verify + outbound sign).
8. Per-delivery timeout + basic circuit breaker.

### Phase 3 — Product Surface
9. `GET /api/v1/webhooks/:id` status lookup endpoint.
10. `POST /api/v1/webhooks/:id/replay` manual DLQ replay.
11. Multi-tenant `endpoints` table, if that scope is chosen.

### Phase 4 — Resume Polish
12. Prometheus metrics + Grafana panel(s), or at minimum raw numbers reported.
13. k6 load test script + results published in README.
14. GitHub Actions CI.
15. Clean architecture diagram image (based on Section 3) + a "Results" section in the README with real measured numbers.

---

## 8. Current Status (Updated)

Phases 1 and 2 are fully implemented, running locally via Docker Compose, and **verified end-to-end with real HTTP traffic and real external tools** — not just code that compiles, but a system that has actually processed events successfully, via failure/retry/DLQ paths, with signed outbound requests, and with manual DLQ recovery.

### 8.1 Repository structure (as it exists now)

```text
webhook-relay/
├── go.mod
├── Dockerfile                # multi-stage build: `api` and `worker` targets
├── docker-compose.yml        # postgres, redis, api, worker, asynqmon
├── .env.example
├── .gitignore
├── README.md                 # recruiter-facing project overview
├── webhook-relay-context.md  # this file
│
├── cmd/
│   ├── api/main.go           # Fiber HTTP server (ingestion, status lookup, replay)
│   └── worker/main.go        # Asynq worker (delivery + retry + HMAC signing logic)
│
├── internal/
│   ├── config/config.go      # env var loading (DB, Redis, API port, signing secrets)
│   ├── database/postgres.go  # pgx connection pool + IsUniqueViolation helper
│   ├── models/
│   │   ├── webhook.go        # Webhook / DeliveryAttempt structs (Payload is json.RawMessage)
│   │   └── queries.go        # InsertWebhook, GetWebhook, MarkDelivered,
│   │                         # MarkAttemptFailed, InsertDeliveryAttempt, DueForRetry,
│   │                         # ResetForReplay
│   ├── queue/tasks.go        # Asynq task type + payload + client constructor
│   └── signing/
│       ├── hmac.go           # Stripe-style HMAC-SHA256 sign/verify (Sign, Verify, SignAt, VerifyWithTolerance)
│       └── hmac_test.go      # unit tests, all passing (8 tests: round-trip, tamper, replay, malformed header, etc.)
│
└── migrations/
    ├── 000001_create_webhooks_table.up/down.sql
    ├── 000002_create_delivery_attempts_table.up/down.sql
    └── README.md
```

### 8.2 What's implemented

**API (`cmd/api/main.go`):**
- `POST /api/v1/webhooks` — accepts an event, inserts `PENDING` row, enqueues Asynq task, returns 202 in milliseconds. Accepts optional `max_attempts` (1-10, defaults to 5) to override retry count per-event — useful for testing the DLQ path quickly instead of waiting through the full backoff schedule. If `INBOUND_SIGNING_SECRET` is configured, requires and verifies an `X-Webhook-Signature` header on the request before accepting it.
- `GET /api/v1/webhooks/:id` — status lookup.
- `POST /api/v1/webhooks/:id/replay` — resets a `FAILED` webhook back to `PENDING` (attempts=0, clears error fields) and re-enqueues it. Returns 409 if the webhook exists but isn't in `FAILED` status, 404 if it doesn't exist. Note: replays against the *original* `target_url` — there is no way yet to override the target on replay (would be needed for a "fixed the endpoint, replayed, now it works" demo).
- `GET /healthz` — liveness check.
- Idempotency: duplicate `idempotency_key` is caught at the Postgres unique-constraint level and returns `202 already_accepted` instead of erroring or duplicating.

**Worker (`cmd/worker/main.go`):**
- Loads the row from Postgres by ID, makes the outbound HTTP POST with a 5s timeout, records every attempt (success or fail) to `delivery_attempts`, and either marks `DELIVERED`, re-enqueues with a computed backoff delay (via `asynq.ProcessIn`), or marks `FAILED` once `max_attempts` is exhausted.
- Backoff schedule: 10s → 1m → 5m → 30m.
- If `OUTBOUND_SIGNING_SECRET` is configured, every outbound delivery includes a signed `X-Webhook-Signature: t=<unix_ts>,v1=<hex_hmac>` header.

**HMAC signing (`internal/signing/hmac.go`):**
- Stripe-style scheme: signs `"<timestamp>.<payload>"` with HMAC-SHA256, header format `t=<unix_ts>,v1=<hex>`.
- `Verify` rejects both stale (>5min old) and future-dated timestamps (replay/clock-skew protection), using constant-time comparison (`hmac.Equal`) to avoid timing attacks.
- Both outbound signing (worker → target) and inbound verification (publisher → API) use the same package, controlled independently by `OUTBOUND_SIGNING_SECRET` / `INBOUND_SIGNING_SECRET` env vars — either can be left blank to disable that direction.
- Fully unit tested (`hmac_test.go`), 8 tests covering round-trip, wrong secret, tampered payload, malformed headers, stale/future timestamps, and tolerance window edges. All passing.

**Infra:**
- Full Docker Compose stack: `postgres`, `redis`, `api`, `worker`, `asynqmon` (queue dashboard on `localhost:8081`).
- `payload` field is `json.RawMessage`, so API responses show real JSON, not base64.
- Env vars: `DATABASE_URL`, `REDIS_ADDR`, `REDIS_PASSWORD`, `API_PORT`, `OUTBOUND_SIGNING_SECRET`, `INBOUND_SIGNING_SECRET` (all documented in `.env.example`).

### 8.3 Verified behavior (actually run, not just written)

- **Success path** — event targeting `https://httpbin.org/status/200` reached `DELIVERED` on the first attempt.
- **Failure/DLQ path** — event targeting `https://httpbin.org/status/500` retried exactly on schedule (10s/1m/5m/30m), incrementing `attempts` and recording `last_status_code: 500` each time, then was marked `FAILED` after exhausting `max_attempts` — confirmed via worker logs and by polling `GET /api/v1/webhooks/:id`.
- **HMAC signing** — verified visually using webhook.site as the target: confirmed the outbound request actually arrives with a well-formed `X-Webhook-Signature: t=...,v1=...` header. Unit tests also pass (`go test ./internal/signing/... -v`, 8/8 passing).
- **Manual replay** — created a webhook with `max_attempts:2` pointed at a failing endpoint, confirmed it reached `FAILED`, called the replay endpoint, confirmed it reset to `PENDING` (attempts back to 0) and re-ran the full retry cycle (proven via `updated_at` timestamp changing and re-reaching `FAILED` again, since the target was still broken).
- `go build ./...` and `go test ./...` both pass clean across the whole project.

### 8.4 Known gaps still open (see Section 5 for full detail)

- No delivery timeout circuit breaker (timeout exists per-request via `http.Client{Timeout: 5s}`, but no circuit breaker across repeated failures to the same target).
- No way to override `target_url` on replay (replay always retries against the original URL).
- No jitter on backoff (not a problem at current scale/single-user testing).
- Multi-tenancy (`endpoints`/`subscriptions` table) not modeled — currently single `target_url` per event, no per-customer registration.
- No Prometheus metrics, no k6 load test results yet, no CI.

### 8.5 Local dev environment notes (useful context for a fresh AI session)

- Developer is on macOS, using `(base)` conda environment in zsh, Docker Desktop for containers.
- `migrate` CLI (golang-migrate) is installed via `go install` and `$(go env GOPATH)/bin` has been added to `~/.zshrc` PATH.
- Project directory is `~/WebhookRelay` locally (note: differs in casing/spacing from the `webhook-relay` name used in this doc and on GitHub — same project).
- No Go toolchain is available in the AI assistant's sandbox environment, so all Go code changes are reviewed manually (not compiled) before being handed off; the developer runs `go build ./...` and `go test ./...` locally to confirm.

## 9. Next Steps (in priority order)

1. **~~HMAC signing~~ — done.** ~~Manual replay endpoint~~ — **done.**
2. **k6 load test** *(current priority)* — script a realistic load test, capture real RPS/p95/p99 numbers, put them in the README. This is the most concrete, resume-verifiable claim available and hasn't been started yet.
3. **Prometheus metrics** — expose `/metrics` (queue depth, delivery success rate, latency histogram).
4. **GitHub Actions CI** — run `go test ./...` and a `docker compose up` smoke test on push.
5. **(Optional polish) Replay with target_url override** — let `POST /api/v1/webhooks/:id/replay` accept an optional new `target_url` in the body, so a "broken endpoint → fixed → replayed successfully" demo is possible without manually editing the database.
6. **(Optional/scope decision) Multi-tenancy** — only worth doing if the story is "a real product," not just "a personal pipeline."
7. **(Optional/lower priority) Circuit breaker per target URL** — mentioned in known gaps; not urgent at current single-user testing scale.

## 10. Version Control

Project is committed to git and pushed to GitHub (`main` branch), with commits so far covering (in order): initial Phase 1 core path, HMAC signing + unit tests, manual DLQ replay endpoint with `max_attempts` override. `README.md` and `.gitignore` are in place; `.env` (real secrets, including signing secrets) is gitignored — only `.env.example` is tracked.
