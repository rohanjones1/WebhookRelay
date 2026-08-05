package models

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InsertWebhook creates a new PENDING webhook row. Relies on the unique
// constraint on idempotency_key to reject duplicate client retries — callers
// should check for a unique-violation error (pgErr.Code == "23505") and treat
// it as "already accepted" rather than a hard failure.
func InsertWebhook(ctx context.Context, db *pgxpool.Pool, w *Webhook) error {
	const q = `
		INSERT INTO webhooks (idempotency_key, target_url, payload, event_type, max_attempts)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, status, attempts, created_at, updated_at
	`
	return db.QueryRow(ctx, q,
		w.IdempotencyKey, w.TargetURL, w.Payload, w.EventType, w.MaxAttempts,
	).Scan(&w.ID, &w.Status, &w.Attempts, &w.CreatedAt, &w.UpdatedAt)
}

// GetWebhook fetches a single webhook by ID (used by the status-lookup endpoint).
func GetWebhook(ctx context.Context, db *pgxpool.Pool, id uuid.UUID) (*Webhook, error) {
	const q = `
		SELECT id, idempotency_key, target_url, payload, event_type, status,
		       attempts, max_attempts, last_status_code, last_error,
		       next_attempt_at, delivered_at, created_at, updated_at
		FROM webhooks
		WHERE id = $1
	`
	var w Webhook
	err := db.QueryRow(ctx, q, id).Scan(
		&w.ID, &w.IdempotencyKey, &w.TargetURL, &w.Payload, &w.EventType, &w.Status,
		&w.Attempts, &w.MaxAttempts, &w.LastStatusCode, &w.LastError,
		&w.NextAttemptAt, &w.DeliveredAt, &w.CreatedAt, &w.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &w, nil
}

// MarkDelivered transitions a webhook to DELIVERED after a successful attempt.
func MarkDelivered(ctx context.Context, db *pgxpool.Pool, id uuid.UUID, statusCode int) error {
	const q = `
		UPDATE webhooks
		SET status = 'DELIVERED',
		    attempts = attempts + 1,
		    last_status_code = $2,
		    last_error = NULL,
		    delivered_at = now()
		WHERE id = $1
	`
	_, err := db.Exec(ctx, q, id, statusCode)
	return err
}

// MarkAttemptFailed records a failed attempt and either schedules the next
// retry (next_attempt_at) or moves the webhook to FAILED if attempts are
// exhausted. The caller (worker) is responsible for computing nextAttemptAt
// via its backoff policy.
func MarkAttemptFailed(ctx context.Context, db *pgxpool.Pool, id uuid.UUID, statusCode *int, errMsg string, nextAttemptAt *time.Time, exhausted bool) error {
	status := "PENDING"
	if exhausted {
		status = "FAILED"
	}
	const q = `
		UPDATE webhooks
		SET status = $2,
		    attempts = attempts + 1,
		    last_status_code = $3,
		    last_error = $4,
		    next_attempt_at = $5
		WHERE id = $1
	`
	_, err := db.Exec(ctx, q, id, status, statusCode, errMsg, nextAttemptAt)
	return err
}

// InsertDeliveryAttempt appends an audit-log row for a single HTTP attempt.
func InsertDeliveryAttempt(ctx context.Context, db *pgxpool.Pool, a *DeliveryAttempt) error {
	const q = `
		INSERT INTO delivery_attempts (webhook_id, attempt_number, status_code, response_body, error, duration_ms)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, attempted_at
	`
	return db.QueryRow(ctx, q,
		a.WebhookID, a.AttemptNumber, a.StatusCode, a.ResponseBody, a.Error, a.DurationMS,
	).Scan(&a.ID, &a.AttemptedAt)
}

// ResetForReplay moves a FAILED webhook back to PENDING with attempts reset
// to 0, so it can be re-enqueued and retried from scratch. Only rows
// currently in FAILED status are eligible — this returns pgx.ErrNoRows if
// the id doesn't exist or the webhook isn't in a replayable state, so the
// caller can distinguish "not found" from "not eligible" from the row it
// re-reads afterward.
func ResetForReplay(ctx context.Context, db *pgxpool.Pool, id uuid.UUID) error {
	const q = `
		UPDATE webhooks
		SET status = 'PENDING',
		    attempts = 0,
		    last_status_code = NULL,
		    last_error = NULL,
		    next_attempt_at = NULL,
		    delivered_at = NULL
		WHERE id = $1 AND status = 'FAILED'
	`
	tag, err := db.Exec(ctx, q, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
// this is what the worker polls (or what backs an Asynq task's readiness).
func DueForRetry(ctx context.Context, db *pgxpool.Pool, limit int) ([]Webhook, error) {
	const q = `
		SELECT id, idempotency_key, target_url, payload, event_type, status,
		       attempts, max_attempts, last_status_code, last_error,
		       next_attempt_at, delivered_at, created_at, updated_at
		FROM webhooks
		WHERE status = 'PENDING'
		  AND (next_attempt_at IS NULL OR next_attempt_at <= now())
		ORDER BY created_at
		LIMIT $1
	`
	rows, err := db.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Webhook
	for rows.Next() {
		var w Webhook
		if err := rows.Scan(
			&w.ID, &w.IdempotencyKey, &w.TargetURL, &w.Payload, &w.EventType, &w.Status,
			&w.Attempts, &w.MaxAttempts, &w.LastStatusCode, &w.LastError,
			&w.NextAttemptAt, &w.DeliveredAt, &w.CreatedAt, &w.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}