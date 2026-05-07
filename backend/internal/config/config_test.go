package config

import (
	"strings"
	"testing"
)

func TestLoad_DefaultPort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("PORT", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != "8080" {
		t.Fatalf("default port should be 8080, got %q", cfg.Port)
	}
	if cfg.AppEnv != "dev" {
		t.Fatalf("default APP_ENV should be dev, got %q", cfg.AppEnv)
	}
}

func TestLoad_MissingDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	_, err := Load()
	if err == nil {
		t.Fatalf("expected error when DATABASE_URL is missing")
	}
	// Error must mention the key name but never contain a value.
	msg := err.Error()
	if !strings.Contains(msg, "DATABASE_URL") {
		t.Fatalf("error should mention DATABASE_URL, got %q", msg)
	}
}

func TestLoad_AllValuesSet(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("JWT_ACCESS_SECRET", "access-secret")
	t.Setenv("JWT_REFRESH_SECRET", "refresh-secret")
	t.Setenv("PORT", "9090")
	t.Setenv("CORS_ORIGIN", "http://localhost:5173")
	t.Setenv("APP_ENV", "prod")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DatabaseURL != "postgres://localhost/db" ||
		cfg.JWTAccessSecret != "access-secret" ||
		cfg.JWTRefreshSecret != "refresh-secret" ||
		cfg.Port != "9090" ||
		cfg.CORSOrigin != "http://localhost:5173" ||
		cfg.AppEnv != "prod" {
		t.Fatalf("config not loaded correctly: %+v", cfg)
	}
}

func TestLoad_ErrorOmitsSecretValues(t *testing.T) {
	// Even if DATABASE_URL is missing, the error must not leak any secret value
	// that happens to be set in the environment.
	t.Setenv("DATABASE_URL", "")
	t.Setenv("JWT_ACCESS_SECRET", "super-secret-access-key-123")
	t.Setenv("JWT_REFRESH_SECRET", "super-secret-refresh-key-456")
	_, err := Load()
	if err == nil {
		t.Fatalf("expected error")
	}
	msg := err.Error()
	if strings.Contains(msg, "super-secret-access-key-123") || strings.Contains(msg, "super-secret-refresh-key-456") {
		t.Fatalf("error message must not leak secret values: %q", msg)
	}
}

func TestLoad_InvalidAppEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("APP_ENV", "staging")
	_, err := Load()
	if err == nil {
		t.Fatalf("expected error for invalid APP_ENV")
	}
}
