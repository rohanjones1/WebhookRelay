-- Audit log of every individual delivery attempt for a webhook.
-- The `webhooks` table holds current state; this table holds full history,
-- which is what you actually want for debugging "why did this fail 4 times".
CREATE TABLE delivery_attempts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    webhook_id    UUID NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,

    attempt_number INTEGER NOT NULL,

    -- NULL if the request never got a response (timeout, connection refused, etc).
    status_code   INTEGER,

    -- Truncate long bodies at the application layer before inserting; this is
    -- for short error messages / response snippets, not full response bodies.
    response_body TEXT,
    error         TEXT,

    duration_ms   INTEGER,

    attempted_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_delivery_attempts_webhook_id
    ON delivery_attempts (webhook_id, attempt_number);
