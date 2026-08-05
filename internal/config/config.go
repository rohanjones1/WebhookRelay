package config

import (
	"fmt"
	"os"
)

// Config holds all environment-driven settings. Loaded once at startup by
// both cmd/api and cmd/worker.
type Config struct {
	// Postgres connection string, e.g.
	// postgres://user:password@host:5432/dbname?sslmode=require
	DatabaseURL string

	// Redis address for Asynq, e.g. "localhost:6379"
	RedisAddr string
	RedisPass string

	// Port the Fiber API listens on.
	APIPort string

	// OutboundSigningSecret signs the X-Webhook-Signature header on every
	// outbound delivery to a target_url, so receivers can verify the
	// request really came from this relay. Required for outbound signing;
	// if empty, outbound requests are sent unsigned.
	OutboundSigningSecret string

	// InboundSigningSecret, if set, requires every POST /api/v1/webhooks
	// request to include a valid X-Webhook-Signature header signed with
	// this secret. Leave empty to accept unsigned requests (e.g. for local
	// development or a trusted single-publisher setup).
	InboundSigningSecret string
}

// Load reads required env vars and fails fast if anything critical is
// missing, rather than letting the app start in a broken state.
func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:           os.Getenv("DATABASE_URL"),
		RedisAddr:             os.Getenv("REDIS_ADDR"),
		RedisPass:             os.Getenv("REDIS_PASSWORD"), // optional, may be empty
		APIPort:               os.Getenv("API_PORT"),
		OutboundSigningSecret: os.Getenv("OUTBOUND_SIGNING_SECRET"), // optional
		InboundSigningSecret:  os.Getenv("INBOUND_SIGNING_SECRET"),  // optional
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.RedisAddr == "" {
		cfg.RedisAddr = "localhost:6379" // sane local default
	}
	if cfg.APIPort == "" {
		cfg.APIPort = "8080"
	}

	return cfg, nil
}