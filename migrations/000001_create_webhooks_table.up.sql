-- Enable pgcrypto for gen_random_uuid() (Neon/Postgres 13+ has it available as an extension)
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Delivery status lifecycle for a webhook event
CREATE TYPE webhook_status AS ENUM (
    'PENDING',    -- inserted, not yet enqueued/delivered
    'PROCESSING', -- currently being attempted by a worker
    'DELIVERED',  -- target responded 2xx
    'FAILED',     -- retries exhausted, moved to DLQ
    'CANCELLED'   -- manually cancelled before delivery
);

CREATE TABLE webhooks (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Client-supplied (or content-hash derived) key to prevent duplicate inserts
    -- on retried POST /api/v1/webhooks requests.
    idempotency_key  TEXT NOT NULL,

    -- Where this event should ultimately be delivered.
    target_url       TEXT NOT NULL,

    -- Free-form event payload as sent by the publisher.
    payload          JSONB NOT NULL,

    -- Optional event type/category, useful once multi-tenant subscriptions exist.
    event_type       TEXT,

    status           webhook_status NOT NULL DEFAULT 'PENDING',

    attempts         INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER NOT NULL DEFAULT 5,

    -- Last HTTP status code / error the target returned, for debugging + DLQ display.
    last_status_code INTEGER,
    last_error       TEXT,

    -- When the worker should next attempt delivery (used for backoff scheduling/polling).
    next_attempt_at  TIMESTAMPTZ,

    delivered_at     TIMESTAMPTZ,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Prevents duplicate webhook rows from retried client requests.
    CONSTRAINT uq_webhooks_idempotency_key UNIQUE (idempotency_key)
);

-- Worker needs to efficiently find events that are due for (re)delivery.
CREATE INDEX idx_webhooks_status_next_attempt
    ON webhooks (status, next_attempt_at);

-- Common lookup pattern: "show me everything for this target" (debugging/dashboard).
CREATE INDEX idx_webhooks_target_url
    ON webhooks (target_url);

-- Automatically bump updated_at on every row change.
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_webhooks_updated_at
    BEFORE UPDATE ON webhooks
    FOR EACH ROW
    EXECUTE FUNCTION set_updated_at();
