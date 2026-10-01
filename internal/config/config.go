package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // embed timezone database
)

type Config struct {
	TelegramBotToken          string
	WebhookURL                string
	WebhookSecretToken        string
	WebhookListenAddr         string
	OwnerTelegramID           int64
	DatabasePath              string
	TursoDatabaseURL          string
	TursoAuthToken            string
	GroupTimezone             string
	Location                  *time.Location
	NotificationCheckInterval time.Duration
	LogLevel                  slog.Level
}

func Load() (*Config, error) {
	token := envOrFallback("TELEGRAM_BOT_TOKEN", "TELEGRAM_TOKEN")
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("TELEGRAM_BOT_TOKEN (or TELEGRAM_TOKEN) environment variable is required")
	}

	webhookURL := strings.TrimSpace(os.Getenv("WEBHOOK_URL"))
	parsedWebhookURL, err := url.Parse(webhookURL)
	if err != nil || parsedWebhookURL.Scheme != "https" || parsedWebhookURL.Host == "" || parsedWebhookURL.User != nil || parsedWebhookURL.RawQuery != "" || parsedWebhookURL.Fragment != "" {
		return nil, errors.New("WEBHOOK_URL must be a public HTTPS URL without credentials, query, or fragment")
	}

	webhookSecret := strings.TrimSpace(os.Getenv("WEBHOOK_SECRET_TOKEN"))
	if webhookSecret == "" || len(webhookSecret) > 256 {
		return nil, errors.New("WEBHOOK_SECRET_TOKEN is required and must be 1-256 characters")
	}
	for _, char := range webhookSecret {
		if !((char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_' || char == '-') {
			return nil, errors.New("WEBHOOK_SECRET_TOKEN may contain only letters, digits, '_' and '-'")
		}
	}
	webhookListenAddr := strings.TrimSpace(os.Getenv("WEBHOOK_LISTEN_ADDR"))
	if webhookListenAddr == "" {
		webhookListenAddr = ":8080"
	}

	ownerStr := os.Getenv("OWNER_TELEGRAM_ID")
	if strings.TrimSpace(ownerStr) == "" {
		return nil, errors.New("OWNER_TELEGRAM_ID environment variable is required")
	}
	ownerID, err := strconv.ParseInt(strings.TrimSpace(ownerStr), 10, 64)
	if err != nil || ownerID == 0 {
		return nil, fmt.Errorf("invalid OWNER_TELEGRAM_ID: must be a non-zero integer, got %q", ownerStr)
	}

	dbPath := os.Getenv("DATABASE_PATH")
	if strings.TrimSpace(dbPath) == "" {
		dbPath = "./data/bot.db"
	}
	tursoURL := strings.TrimSpace(envOrFallback("TURSO_DATABASE_URL", "DATABASE_URL"))
	tursoToken := strings.TrimSpace(envOrFallback("TURSO_AUTH_TOKEN", "DATABASE_AUTH_TOKEN"))
	if tursoURL != "" {
		parsedTursoURL, parseErr := url.Parse(tursoURL)
		if parseErr != nil || parsedTursoURL.Scheme != "libsql" || parsedTursoURL.Host == "" {
			return nil, errors.New("TURSO_DATABASE_URL must be a valid libsql:// URL")
		}
		if tursoToken == "" {
			return nil, errors.New("TURSO_AUTH_TOKEN is required when TURSO_DATABASE_URL is set")
		}
	}

	tz := envOrFallback("GROUP_TIMEZONE", "TIMEZONE")
	if strings.TrimSpace(tz) == "" {
		tz = "Europe/Minsk"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("invalid GROUP_TIMEZONE %q: %w", tz, err)
	}

	checkIntervalStr := envOrFallback("NOTIFICATION_CHECK_INTERVAL", "NOTIFICATION_POLL_SECONDS")
	if strings.TrimSpace(checkIntervalStr) == "" {
		checkIntervalStr = "1m"
	}
	interval, err := time.ParseDuration(checkIntervalStr)
	if err != nil {
		if seconds, parseErr := strconv.Atoi(checkIntervalStr); parseErr == nil {
			interval = time.Duration(seconds) * time.Second
			err = nil
		}
	}
	if err != nil || interval < 1*time.Second {
		return nil, fmt.Errorf("invalid NOTIFICATION_CHECK_INTERVAL %q: must be >= 1s", checkIntervalStr)
	}

	logLevelStr := strings.ToUpper(strings.TrimSpace(os.Getenv("LOG_LEVEL")))
	level := slog.LevelInfo
	switch logLevelStr {
	case "DEBUG":
		level = slog.LevelDebug
	case "INFO", "":
		level = slog.LevelInfo
	case "WARN", "WARNING":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		return nil, fmt.Errorf("unknown LOG_LEVEL %q: supported DEBUG, INFO, WARN, ERROR", logLevelStr)
	}

	return &Config{
		TelegramBotToken:          strings.TrimSpace(token),
		WebhookURL:                webhookURL,
		WebhookSecretToken:        webhookSecret,
		WebhookListenAddr:         webhookListenAddr,
		OwnerTelegramID:           ownerID,
		DatabasePath:              dbPath,
		TursoDatabaseURL:          tursoURL,
		TursoAuthToken:            tursoToken,
		GroupTimezone:             tz,
		Location:                  loc,
		NotificationCheckInterval: interval,
		LogLevel:                  level,
	}, nil
}

func envOrFallback(primary, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(primary)); value != "" {
		return value
	}
	return os.Getenv(fallback)
}
