package config

import (
	"testing"
	"time"
)

func setValidConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TELEGRAM_BOT_TOKEN", "")
	t.Setenv("TELEGRAM_TOKEN", "test-token")
	t.Setenv("WEBHOOK_URL", "https://bot.example.com/telegram/webhook/path")
	t.Setenv("WEBHOOK_SECRET_TOKEN", "test_secret-token")
	t.Setenv("WEBHOOK_LISTEN_ADDR", "")
	t.Setenv("OWNER_TELEGRAM_ID", "12345")
	t.Setenv("DATABASE_PATH", "")
	t.Setenv("TURSO_DATABASE_URL", "")
	t.Setenv("TURSO_AUTH_TOKEN", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DATABASE_AUTH_TOKEN", "")
	t.Setenv("GROUP_TIMEZONE", "")
	t.Setenv("TIMEZONE", "Europe/Minsk")
	t.Setenv("NOTIFICATION_CHECK_INTERVAL", "")
	t.Setenv("NOTIFICATION_POLL_SECONDS", "45")
	t.Setenv("LOG_LEVEL", "info")
}

func TestLoadSupportsLegacyEnvironmentNames(t *testing.T) {
	setValidConfigEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.TelegramBotToken != "test-token" {
		t.Errorf("TelegramBotToken = %q, want legacy token value", cfg.TelegramBotToken)
	}
	if cfg.WebhookListenAddr != ":8080" {
		t.Errorf("WebhookListenAddr = %q, want default :8080", cfg.WebhookListenAddr)
	}
	if cfg.NotificationCheckInterval != 45*time.Second {
		t.Errorf("NotificationCheckInterval = %s, want 45s", cfg.NotificationCheckInterval)
	}
	if cfg.GroupTimezone != "Europe/Minsk" {
		t.Errorf("GroupTimezone = %q, want Europe/Minsk", cfg.GroupTimezone)
	}
}

func TestLoadRequiresHTTPSWebhookURL(t *testing.T) {
	setValidConfigEnv(t)
	t.Setenv("WEBHOOK_URL", "http://bot.example.com/webhook")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid webhook URL error")
	}
}

func TestLoadRejectsInvalidWebhookSecret(t *testing.T) {
	setValidConfigEnv(t)
	t.Setenv("WEBHOOK_SECRET_TOKEN", "contains spaces")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid webhook secret error")
	}
}

func TestLoadTursoConfiguration(t *testing.T) {
	setValidConfigEnv(t)
	t.Setenv("TURSO_DATABASE_URL", "libsql://deadline-bot.example.turso.io")
	t.Setenv("TURSO_AUTH_TOKEN", "secret-token")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.TursoDatabaseURL != "libsql://deadline-bot.example.turso.io" {
		t.Errorf("TursoDatabaseURL = %q", cfg.TursoDatabaseURL)
	}
	if cfg.TursoAuthToken != "secret-token" {
		t.Error("TursoAuthToken was not loaded")
	}
}

func TestLoadRequiresTursoToken(t *testing.T) {
	setValidConfigEnv(t)
	t.Setenv("TURSO_DATABASE_URL", "libsql://deadline-bot.example.turso.io")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want missing Turso token error")
	}
}
