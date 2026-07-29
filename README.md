# Webhook Relay 🪝

A high-throughput, resilient webhook delivery microservice built in Go.

Webhook Relay guarantees **at-least-once delivery** of API payloads. It acts as an asynchronous middleware between systems, decoupling ingestion from execution to ensure 0% data loss during downstream outages, network failures, or traffic spikes.

## ✨ Features

- 🚀 **High Throughput:** Capable of ingesting 10,000+ events per second without blocking the sender.
- ♻️ **Automated Retries:** Implements exponential backoff for failed deliveries, preventing thundering herd problems on downstream servers.
- 🛡️ **Zero Data Loss:** Safely logs all incoming payloads to PostgreSQL before queuing, ensuring recoverability in the event of a worker crash.
- 📬 **Dead Letter Queue (DLQ):** Automatically isolates payloads that fail after maximum retry attempts for manual inspection.

## 🛠️ Tech Stack

- **Language:** Go (Golang)
- **API Framework:** Fiber
- **Task Queue:** Asynq
- **In-Memory Store:** Redis
- **Database:** PostgreSQL (Neon)
- **Infrastructure:** Docker & Docker Compose

## 🏗️ System Architecture

1. **Ingestion:** API receives a webhook payload via POST request.
2. **Persistence:** State is immediately saved to Postgres (`PENDING`), and the payload is pushed to Redis.
3. **Acknowledgment:** API instantly returns `202 Accepted` to the sender.
4. **Execution:** Background Go workers pull the job from Redis and attempt delivery to the target URL.
5. **Resolution:**
   - _Success (2xx):_ Marks Postgres status as `DELIVERED`.
   - _Failure (4xx/5xx):_ Increments retry count and re-queues with exponential backoff.
   - _Exhausted:_ Moves to Dead Letter Queue (DLQ) and marks as `FAILED`.

## 🚀 Quick Start

### Prerequisites

- [Docker](https://docs.docker.com/get-docker/) & Docker Compose
- Go 1.21+ (if running locally without Docker)

### Run with Docker

1. Clone the repository:
   ```bash
   git clone [https://github.com/yourusername/webhook-relay.git](https://github.com/yourusername/webhook-relay.git)
   cd webhook-relay
   ```
