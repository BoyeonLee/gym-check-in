// Package config loads runtime configuration from environment variables.
// All secrets are read by name only — values must never appear in error messages.
package config

import (
	"fmt"
	"os"
)

// Config holds all server configuration. JWT secrets are optional in early
// scaffolding phases and become required once the auth layer is wired in.
type Config struct {
	DatabaseURL      string
	JWTAccessSecret  string
	JWTRefreshSecret string
	Port             string
	CORSOrigin       string
	AppEnv           string
}

// Load reads environment variables and returns a populated Config.
// Required: DATABASE_URL. Defaults: PORT=8080, APP_ENV=dev.
// Errors mention only the variable NAME — never the value — so secrets
// can never leak into logs or stderr.
func Load() (*Config, error) {
	cfg := &Config{
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		JWTAccessSecret:  os.Getenv("JWT_ACCESS_SECRET"),
		JWTRefreshSecret: os.Getenv("JWT_REFRESH_SECRET"),
		Port:             os.Getenv("PORT"),
		CORSOrigin:       os.Getenv("CORS_ORIGIN"),
		AppEnv:           os.Getenv("APP_ENV"),
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.AppEnv == "" {
		cfg.AppEnv = "dev"
	}

	if cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("config: required env DATABASE_URL is empty")
	}
	if cfg.AppEnv != "dev" && cfg.AppEnv != "prod" {
		return nil, fmt.Errorf("config: APP_ENV must be 'dev' or 'prod'")
	}
	return cfg, nil
}
