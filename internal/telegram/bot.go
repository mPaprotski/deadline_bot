package telegram

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"deadline_bot/internal/service"
	"deadline_bot/internal/storage/sqlite"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
	api             *tgbotapi.BotAPI
	storage         *sqlite.Storage
	userService     *service.UserService
	subjectService  *service.SubjectService
	labService      *service.LabService
	settingsService *service.SettingsService
	timeHelper      *service.TimeHelper
	clock           service.Clock
	fsm             *FSMManager
	logger          *slog.Logger
	server          *http.Server
	stopOnce        sync.Once
	wg              sync.WaitGroup
}

func NewBot(
	api *tgbotapi.BotAPI,
	storage *sqlite.Storage,
	userService *service.UserService,
	subjectService *service.SubjectService,
	labService *service.LabService,
	settingsService *service.SettingsService,
	timeHelper *service.TimeHelper,
	clock service.Clock,
	logger *slog.Logger,
) *Bot {
	if logger == nil {
		logger = slog.Default()
	}
	if clock == nil {
		clock = service.RealClock{}
	}

	return &Bot{
		api:             api,
		storage:         storage,
		userService:     userService,
		subjectService:  subjectService,
		labService:      labService,
		settingsService: settingsService,
		timeHelper:      timeHelper,
		clock:           clock,
		fsm:             NewFSMManager(storage),
		logger:          logger,
	}
}

func (b *Bot) Start(ctx context.Context, webhookURL, secretToken, listenAddr string) error {
	// Register commands in Telegram Bot API menu
	commands := []tgbotapi.BotCommand{
		{Command: "start", Description: "Запуск и главное меню"},
		{Command: "deadlines", Description: "Ближайшие дедлайны"},
		{Command: "subjects", Description: "Предметы и работы"},
		{Command: "settings", Description: "Настройки уведомлений"},
		{Command: "help", Description: "Инструкция и помощь"},
		{Command: "admin", Description: "Панель администратора"},
	}

	cmdCfg := tgbotapi.NewSetMyCommands(commands...)
	if _, err := b.api.Request(cmdCfg); err != nil {
		b.logger.Warn("Failed to set bot commands in Telegram menu", "error", err)
	}

	webhook, err := tgbotapi.NewWebhook(webhookURL)
	if err != nil {
		return fmt.Errorf("invalid webhook URL: %w", err)
	}
	parsedWebhookURL, err := url.Parse(webhookURL)
	if err != nil {
		return fmt.Errorf("parse webhook URL: %w", err)
	}
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen for webhook on %s: %w", listenAddr, err)
	}

	mux := http.NewServeMux()
	webhookPath := parsedWebhookURL.Path
	if webhookPath == "" {
		webhookPath = "/"
	}
	mux.HandleFunc(webhookPath, b.webhookHandler(ctx, secretToken, webhookPath))

	b.server = &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		if err := b.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			b.logger.Error("Webhook HTTP server stopped unexpectedly", "error", err)
		}
	}()

	params := tgbotapi.Params{
		"url":             webhook.URL.String(),
		"secret_token":    secretToken,
		"allowed_updates": `["message","callback_query"]`,
	}
	response, err := b.api.MakeRequest("setWebhook", params)
	if err != nil {
		_ = b.server.Close()
		b.wg.Wait()
		return fmt.Errorf("register Telegram webhook: %w", err)
	}
	if !response.Ok {
		_ = b.server.Close()
		b.wg.Wait()
		return fmt.Errorf("register Telegram webhook: %s", response.Description)
	}

	b.logger.Info("Telegram webhook started", "bot_username", b.api.Self.UserName, "listen_addr", listenAddr)

	return nil
}

func (b *Bot) webhookHandler(ctx context.Context, secretToken, webhookPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != webhookPath {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token")), []byte(secretToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		update, err := b.api.HandleUpdate(r)
		if err != nil {
			b.logger.Warn("Rejected invalid Telegram webhook update", "error", err)
			http.Error(w, "invalid update", http.StatusBadRequest)
			return
		}
		b.processUpdate(ctx, *update)
		w.WriteHeader(http.StatusOK)
	}
}

func (b *Bot) Stop() {
	b.stopOnce.Do(func() {
		if b.server != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := b.server.Shutdown(ctx); err != nil {
				b.logger.Warn("Graceful webhook shutdown failed; forcing close", "error", err)
				_ = b.server.Close()
			}
		}
	})
	b.wg.Wait()
}

func (b *Bot) processUpdate(ctx context.Context, update tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Error("Panic recovered in update processor", "panic", r)
		}
	}()

	if update.Message != nil {
		from := update.Message.From
		if from == nil {
			return
		}

		user, err := b.userService.EnsureUser(ctx, from.ID, from.UserName, from.FirstName, from.LastName)
		if err != nil {
			b.logger.Error("Failed to ensure user", "user_id", from.ID, "error", err)
			return
		}

		if err := b.handleMessage(ctx, update.Message, user); err != nil {
			b.logger.Error("Error handling message", "user_id", from.ID, "error", err)
		}
		return
	}

	if update.CallbackQuery != nil {
		from := update.CallbackQuery.From
		user, err := b.userService.EnsureUser(ctx, from.ID, from.UserName, from.FirstName, from.LastName)
		if err != nil {
			b.logger.Error("Failed to ensure user on callback", "user_id", from.ID, "error", err)
			return
		}

		if err := b.handleCallback(ctx, update.CallbackQuery, user); err != nil {
			b.logger.Error("Error handling callback", "user_id", from.ID, "error", err)
		}
		return
	}
}
