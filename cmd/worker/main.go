package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"

	"webhook-relay/internal/config"
	"webhook-relay/internal/database"
	"webhook-relay/internal/models"
	"webhook-relay/internal/queue"
	"webhook-relay/internal/signing"
)

const deliveryTimeout = 5 * time.Second

// backoffSchedule maps attempt number (1-indexed) to the delay before the
// next retry. Matches the "10s, 1m, 5m..." sketch in the architecture doc.
// Once attempts exceeds len(backoffSchedule), retries are exhausted and the
// webhook is marked FAILED (DLQ).
var backoffSchedule = []time.Duration{
	10 * time.Second,
	1 * time.Minute,
	5 * time.Minute,
	30 * time.Minute,
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx := context.Background()

	db, err := database.NewPostgresPool(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	defer db.Close()

	asynqClient := queue.NewClient(cfg.RedisAddr, cfg.RedisPass)
	defer asynqClient.Close()

	httpClient := &http.Client{Timeout: deliveryTimeout}

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: cfg.RedisAddr, Password: cfg.RedisPass},
		asynq.Config{Concurrency: 10},
	)

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeWebhookDeliver, handleDeliver(db, httpClient, asynqClient, cfg))

	log.Println("worker started")
	if err := srv.Run(mux); err != nil {
		log.Fatalf("worker error: %v", err)
	}
}

// handleDeliver processes a single "webhook:deliver" task: loads the current
// row from Postgres, attempts HTTP delivery, records the attempt, and either
// marks the webhook DELIVERED, re-enqueues it for retry after a backoff
// delay, or marks it FAILED once retries are exhausted.
func handleDeliver(db *pgxpool.Pool, httpClient *http.Client, asynqClient *asynq.Client, cfg *config.Config) asynq.HandlerFunc {
	return func(ctx context.Context, t *asynq.Task) error {
		var p queue.DeliverPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return fmt.Errorf("unmarshal payload: %w", err)
		}

		w, err := models.GetWebhook(ctx, db, p.WebhookID)
		if err != nil {
			return fmt.Errorf("load webhook %s: %w", p.WebhookID, err)
		}
		if w == nil {
			// Row is gone (manually deleted/cancelled) — nothing to do.
			log.Printf("webhook %s no longer exists, skipping", p.WebhookID)
			return nil
		}
		if w.Status == models.StatusDelivered || w.Status == models.StatusCancelled {
			// Already handled (e.g. duplicate task delivery from Asynq) — no-op.
			return nil
		}

		attemptNumber := w.Attempts + 1
		start := time.Now()

		statusCode, respBody, deliverErr := attemptDelivery(ctx, httpClient, w.TargetURL, w.Payload, cfg.OutboundSigningSecret)
		duration := int(time.Since(start).Milliseconds())

		// Always record the attempt in the audit log, success or failure.
		attemptRecord := &models.DeliveryAttempt{
			WebhookID:     w.ID,
			AttemptNumber: attemptNumber,
			DurationMS:    &duration,
		}
		if statusCode != 0 {
			attemptRecord.StatusCode = &statusCode
		}
		if respBody != "" {
			attemptRecord.ResponseBody = &respBody
		}
		if deliverErr != nil {
			errStr := deliverErr.Error()
			attemptRecord.Error = &errStr
		}
		if err := models.InsertDeliveryAttempt(ctx, db, attemptRecord); err != nil {
			log.Printf("failed to record delivery attempt for %s: %v", w.ID, err)
		}

		success := deliverErr == nil && statusCode >= 200 && statusCode < 300
		if success {
			if err := models.MarkDelivered(ctx, db, w.ID, statusCode); err != nil {
				return fmt.Errorf("mark delivered: %w", err)
			}
			return nil
		}

		// Failure path: decide whether to retry or move to FAILED (DLQ).
		exhausted := attemptNumber >= w.MaxAttempts
		var errMsg string
		if deliverErr != nil {
			errMsg = deliverErr.Error()
		} else {
			errMsg = fmt.Sprintf("target responded with status %d", statusCode)
		}

		var statusCodePtr *int
		if statusCode != 0 {
			statusCodePtr = &statusCode
		}

		var nextAttemptAt *time.Time
		if !exhausted {
			delay := backoffFor(attemptNumber)
			next := time.Now().Add(delay)
			nextAttemptAt = &next
		}

		if err := models.MarkAttemptFailed(ctx, db, w.ID, statusCodePtr, errMsg, nextAttemptAt, exhausted); err != nil {
			return fmt.Errorf("mark attempt failed: %w", err)
		}

		if exhausted {
			log.Printf("webhook %s exhausted retries, moved to FAILED", w.ID)
			return nil // terminal — do not return an error, this isn't an Asynq-level failure
		}

		// Re-enqueue with Asynq's own delay so the task fires again roughly
		// when next_attempt_at says it should.
		task, err := queue.NewDeliverTask(w.ID)
		if err != nil {
			return fmt.Errorf("build retry task: %w", err)
		}
		delay := backoffFor(attemptNumber)
		if _, err := asynqClient.EnqueueContext(ctx, task, asynq.ProcessIn(delay)); err != nil {
			return fmt.Errorf("re-enqueue retry: %w", err)
		}

		log.Printf("webhook %s attempt %d failed (%s), retrying in %s", w.ID, attemptNumber, errMsg, delay)
		return nil
	}
}

// attemptDelivery makes the single outbound HTTP POST to the target. Returns
// the response status code (0 if the request never got a response), a
// truncated response body snippet for the audit log, and any transport-level
// error (timeout, connection refused, etc). If signingSecret is non-empty,
// the request is signed with an X-Webhook-Signature header so the receiver
// can verify it really came from this relay.
func attemptDelivery(ctx context.Context, client *http.Client, targetURL string, payload []byte, signingSecret string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(payload))
	if err != nil {
		return 0, "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "webhook-relay/1.0")

	if signingSecret != "" {
		req.Header.Set("X-Webhook-Signature", signing.Sign(signingSecret, payload))
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	const maxSnippet = 1024
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxSnippet))

	return resp.StatusCode, string(body), nil
}

// backoffFor returns the delay before the given attempt number's retry,
// falling back to the last defined step if attemptNumber exceeds the
// schedule (shouldn't happen given MaxAttempts, but keeps this safe).
func backoffFor(attemptNumber int) time.Duration {
	idx := attemptNumber - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(backoffSchedule) {
		idx = len(backoffSchedule) - 1
	}
	return backoffSchedule[idx]
}