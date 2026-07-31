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
}

// Load reads required env vars and fails fast if anything critical is
// missing, rather than letting the app start in a broken state.
func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		RedisAddr:   os.Getenv("REDIS_ADDR"),
		RedisPass:   os.Getenv("REDIS_PASSWORD"), // optional, may be empty
		APIPort:     os.Getenv("API_PORT"),
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
