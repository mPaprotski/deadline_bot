package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"deadline_bot/internal/config"
	"deadline_bot/internal/notifications"
	"deadline_bot/internal/service"
	"deadline_bot/internal/storage/sqlite"
	"deadline_bot/internal/telegram"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.LogLevel,
	}))
	slog.SetDefault(logger)

	databaseBackend := "sqlite"
	if cfg.TursoDatabaseURL != "" {
		databaseBackend = "turso"
	}

	logger.Info("Starting Deadline Bot",
		"owner_id", cfg.OwnerTelegramID,
		"database_backend", databaseBackend,
		"timezone", cfg.GroupTimezone,
		"check_interval", cfg.NotificationCheckInterval.String(),
		"log_level", cfg.LogLevel.String(),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 1. Initialize SQLite storage
	var storage *sqlite.Storage
	if cfg.TursoDatabaseURL != "" {
		storage, err = sqlite.NewRemote(ctx, cfg.TursoDatabaseURL, cfg.TursoAuthToken, logger)
	} else {
		storage, err = sqlite.New(ctx, cfg.DatabasePath, logger)
	}
	if err != nil {
		return fmt.Errorf("database initialization failed: %w", err)
	}
	defer func() {
		if err := storage.Close(); err != nil {
			logger.Error("Error closing database", "error", err)
		} else {
			logger.Info("Database connection closed cleanly")
		}
	}()

	// 2. Run SQL migrations before starting services
	logger.Info("Executing database migrations...")
	if err := storage.Migrate(ctx); err != nil {
		return fmt.Errorf("database migration failed: %w", err)
	}
	logger.Info("Database migrations completed successfully")

	// 3. Initialize business services
	clock := service.RealClock{}
	timeHelper := service.NewTimeHelper(cfg.Location)
	userService := service.NewUserService(storage, clock, cfg.OwnerTelegramID)
	subjectService := service.NewSubjectService(storage, clock)
	labService := service.NewLabService(storage, clock, timeHelper)
	settingsService := service.NewSettingsService(storage, clock)

	// Ensure default group and owner setup
	initGroup, err := userService.EnsureDefaultGroup(ctx, "Учебная группа", "STUDENT2026")
	if err != nil {
		return fmt.Errorf("failed to initialize default study group: %w", err)
	}
	logger.Info("Study group ready", "group_name", initGroup.Name, "invite_code", initGroup.InviteCode)

	// 4. Initialize Telegram Bot API
	botAPI, err := tgbotapi.NewBotAPI(cfg.TelegramBotToken)
	if err != nil {
		return fmt.Errorf("failed to connect to Telegram Bot API: %w", err)
	}
	logger.Info("Connected to Telegram Bot API", "bot_id", botAPI.Self.ID, "username", botAPI.Self.UserName)

	// 5. Initialize and start notification worker
	sender := notifications.NewTelegramBotSender(botAPI)
	worker := notifications.NewWorker(storage, sender, clock, timeHelper, cfg.NotificationCheckInterval, logger)
	worker.Start(ctx)
	defer worker.Stop()

	// 6. Initialize and start Telegram Bot handlers
	bot := telegram.NewBot(botAPI, storage, userService, subjectService, labService, settingsService, timeHelper, clock, logger)
	if err := bot.Start(ctx, cfg.WebhookURL, cfg.WebhookSecretToken, cfg.WebhookListenAddr); err != nil {
		return fmt.Errorf("failed to start Telegram bot: %w", err)
	}
	defer bot.Stop()

	logger.Info("Application successfully started and listening for events")

	// Wait for OS termination signal
	<-ctx.Done()
	logger.Info("Termination signal received. Performing graceful shutdown...")

	// Graceful shutdown sequence
	bot.Stop()
	worker.Stop()

	logger.Info("Application stopped cleanly")
	return nil
}
