package main

import (
	"context"
	"encoding/json"
	"log"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

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

		w := &models.Webhook{
			IdempotencyKey: req.IdempotencyKey,
			TargetURL:      req.TargetURL,
			Payload:        req.Payload,
			EventType:      req.EventType,
			MaxAttempts:    5,
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

	log.Printf("api listening on :%s", cfg.APIPort)
	if err := app.Listen(":" + cfg.APIPort); err != nil {
		log.Fatalf("server error: %v", err)
	}
}