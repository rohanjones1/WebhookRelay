package queue

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

const TypeWebhookDeliver = "webhook:deliver"

// DeliverPayload is the JSON body of a "webhook:deliver" task. Kept
// intentionally small — just enough to look up the full row — so the queue
// isn't carrying large payload blobs around; the worker re-reads full
// details (payload, target_url, attempt count) from Postgres by ID.
type DeliverPayload struct {
	WebhookID uuid.UUID `json:"webhook_id"`
}

// NewDeliverTask builds the Asynq task that gets enqueued right after a
// webhook row is inserted as PENDING.
func NewDeliverTask(webhookID uuid.UUID) (*asynq.Task, error) {
	payload, err := json.Marshal(DeliverPayload{WebhookID: webhookID})
	if err != nil {
		return nil, fmt.Errorf("marshal deliver payload: %w", err)
	}
	return asynq.NewTask(TypeWebhookDeliver, payload), nil
}

// NewClient wraps asynq.NewClient with the redis connection options built
// from config, so both cmd/api and cmd/worker construct it identically.
func NewClient(redisAddr, redisPassword string) *asynq.Client {
	return asynq.NewClient(asynq.RedisClientOpt{
		Addr:     redisAddr,
		Password: redisPassword,
	})
}
