package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"webhook-relay/internal/config"
	"webhook-relay/internal/database"
	"webhook-relay/internal/models"
	"webhook-relay/internal/queue"
	"webhook-relay/internal/signing"
)

// createWebhookRequest is the JSON body a publisher sends to
// POST /api/v1/webhooks.
type createWebhookRequest struct {
	IdempotencyKey string          `json:"idempotency_key"`
	TargetURL      string          `json:"target_url"`
	Payload        json.RawMessage `json:"payload"`
	EventType      *string         `json:"event_type,omitempty"`

	// MaxAttempts optionally overrides the default retry count (5).
	// Mainly useful for testing/demoing the DLQ path quickly instead of
	// waiting through the full backoff schedule. Must be between 1 and 10.
	MaxAttempts *int `json:"max_attempts,omitempty"`
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

	app := fiber.New(fiber.Config{
		AppName: "webhook-relay-api",
	})

	app.Get("/healthz", func(c *fiber.Ctx) error {
		return c.SendStatus(fiber.StatusOK)
	})

	app.Post("/api/v1/webhooks", func(c *fiber.Ctx) error {
		if cfg.InboundSigningSecret != "" {
			header := c.Get("X-Webhook-Signature")
			if header == "" {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
					"error": "missing X-Webhook-Signature header",
				})
			}
			if err := signing.Verify(cfg.InboundSigningSecret, c.Body(), header); err != nil {
				return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
					"error": "invalid signature",
				})
			}
		}

		var req createWebhookRequest
		if err := c.BodyParser(&req); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "invalid request body",
			})
		}
		if req.IdempotencyKey == "" || req.TargetURL == "" {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "idempotency_key and target_url are required",
			})
		}

		maxAttempts := 5
		if req.MaxAttempts != nil {
			if *req.MaxAttempts < 1 || *req.MaxAttempts > 10 {
				return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
					"error": "max_attempts must be between 1 and 10",
				})
			}
			maxAttempts = *req.MaxAttempts
		}

		w := &models.Webhook{
			IdempotencyKey: req.IdempotencyKey,
			TargetURL:      req.TargetURL,
			Payload:        req.Payload,
			EventType:      req.EventType,
			MaxAttempts:    maxAttempts,
		}

		if err := models.InsertWebhook(c.Context(), db, w); err != nil {
			if database.IsUniqueViolation(err) {
				// Same idempotency_key seen before: treat as already-accepted,
				// not an error, so retried client requests are safe.
				return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
					"status": "already_accepted",
				})
			}
			log.Printf("insert webhook failed: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to record webhook",
			})
		}

		task, err := queue.NewDeliverTask(w.ID)
		if err != nil {
			log.Printf("build task failed: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to enqueue delivery",
			})
		}

		if _, err := asynqClient.EnqueueContext(c.Context(), task); err != nil {
			log.Printf("enqueue failed: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to enqueue delivery",
			})
		}

		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
			"id":     w.ID,
			"status": w.Status,
		})
	})

	app.Get("/api/v1/webhooks/:id", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "invalid id",
			})
		}

		w, err := models.GetWebhook(c.Context(), db, id)
		if err != nil {
			log.Printf("get webhook failed: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to fetch webhook",
			})
		}
		if w == nil {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
				"error": "not found",
			})
		}

		return c.JSON(w)
	})

	app.Post("/api/v1/webhooks/:id/replay", func(c *fiber.Ctx) error {
		id, err := uuid.Parse(c.Params("id"))
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "invalid id",
			})
		}

		if err := models.ResetForReplay(c.Context(), db, id); err != nil {
			if err == pgx.ErrNoRows {
				// Either the id doesn't exist, or it exists but isn't in
				// FAILED status — either way, not eligible for replay.
				existing, getErr := models.GetWebhook(c.Context(), db, id)
				if getErr == nil && existing != nil {
					return c.Status(fiber.StatusConflict).JSON(fiber.Map{
						"error": fmt.Sprintf("webhook is in %s status, only FAILED webhooks can be replayed", existing.Status),
					})
				}
				return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
					"error": "not found",
				})
			}
			log.Printf("replay reset failed: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to reset webhook for replay",
			})
		}

		task, err := queue.NewDeliverTask(id)
		if err != nil {
			log.Printf("build replay task failed: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to enqueue replay",
			})
		}

		if _, err := asynqClient.EnqueueContext(c.Context(), task); err != nil {
			log.Printf("enqueue replay failed: %v", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error": "failed to enqueue replay",
			})
		}

		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
			"id":     id,
			"status": "PENDING",
		})
	})

	log.Printf("api listening on :%s", cfg.APIPort)
	if err := app.Listen(":" + cfg.APIPort); err != nil {
		log.Fatalf("server error: %v", err)
	}
}