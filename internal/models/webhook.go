package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type WebhookStatus string

const (
	StatusPending    WebhookStatus = "PENDING"
	StatusProcessing WebhookStatus = "PROCESSING"
	StatusDelivered  WebhookStatus = "DELIVERED"
	StatusFailed     WebhookStatus = "FAILED"
	StatusCancelled  WebhookStatus = "CANCELLED"
)

// Webhook mirrors the `webhooks` table. It represents the current state of
// a single event's delivery lifecycle.
type Webhook struct {
	ID              uuid.UUID       `db:"id" json:"id"`
	IdempotencyKey  string          `db:"idempotency_key" json:"idempotency_key"`
	TargetURL       string          `db:"target_url" json:"target_url"`
	Payload         json.RawMessage `db:"payload" json:"payload"` // stored as JSONB; serializes as real JSON, not base64
	EventType       *string         `db:"event_type" json:"event_type,omitempty"`
	Status          WebhookStatus   `db:"status" json:"status"`
	Attempts        int             `db:"attempts" json:"attempts"`
	MaxAttempts     int             `db:"max_attempts" json:"max_attempts"`
	LastStatusCode  *int            `db:"last_status_code" json:"last_status_code,omitempty"`
	LastError       *string         `db:"last_error" json:"last_error,omitempty"`
	NextAttemptAt   *time.Time      `db:"next_attempt_at" json:"next_attempt_at,omitempty"`
	DeliveredAt     *time.Time      `db:"delivered_at" json:"delivered_at,omitempty"`
	CreatedAt       time.Time       `db:"created_at" json:"created_at"`
	UpdatedAt       time.Time       `db:"updated_at" json:"updated_at"`
}

// DeliveryAttempt mirrors the `delivery_attempts` table — one row per HTTP
// attempt made against the target, kept even after the webhook itself
// reaches a terminal state.
type DeliveryAttempt struct {
	ID            uuid.UUID `db:"id" json:"id"`
	WebhookID     uuid.UUID `db:"webhook_id" json:"webhook_id"`
	AttemptNumber int       `db:"attempt_number" json:"attempt_number"`
	StatusCode    *int      `db:"status_code" json:"status_code,omitempty"`
	ResponseBody  *string   `db:"response_body" json:"response_body,omitempty"`
	Error         *string   `db:"error" json:"error,omitempty"`
	DurationMS    *int      `db:"duration_ms" json:"duration_ms,omitempty"`
	AttemptedAt   time.Time `db:"attempted_at" json:"attempted_at"`
}