package notifications

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"deadline_bot/internal/domain"
	"deadline_bot/internal/service"
	"deadline_bot/internal/storage/sqlite"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type TelegramSender interface {
	SendMessage(ctx context.Context, chatID int64, text string, replyMarkup *tgbotapi.InlineKeyboardMarkup) error
}

type Worker struct {
	storage    *sqlite.Storage
	sender     TelegramSender
	clock      service.Clock
	timeHelper *service.TimeHelper
	interval   time.Duration
	logger     *slog.Logger
	stopCh     chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
}

func NewWorker(
	storage *sqlite.Storage,
	sender TelegramSender,
	clock service.Clock,
	timeHelper *service.TimeHelper,
	interval time.Duration,
	logger *slog.Logger,
) *Worker {
	if clock == nil {
		clock = service.RealClock{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Worker{
		storage:    storage,
		sender:     sender,
		clock:      clock,
		timeHelper: timeHelper,
		interval:   interval,
		logger:     logger,
		stopCh:     make(chan struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.wg.Add(1)
	go w.run(ctx)
}

func (w *Worker) Stop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
	w.wg.Wait()
}

func (w *Worker) run(ctx context.Context) {
	defer w.wg.Done()
	w.logger.Info("Notification worker started", "interval", w.interval.String())

	// Step 1: Recover any jobs interrupted during previous process crash/restart
	recovered, err := w.storage.RecoverInterruptedProcessingJobs(ctx)
	if err != nil {
		w.logger.Error("Failed to recover interrupted jobs", "error", err)
	} else if recovered > 0 {
		w.logger.Info("Recovered interrupted notification jobs", "count", recovered)
	}

	// Process immediately on start
	w.processDueJobs(ctx)

	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			w.logger.Info("Notification worker stopping via context")
			return
		case <-w.stopCh:
			w.logger.Info("Notification worker stopping via stop signal")
			return
		case <-ticker.C:
			w.processDueJobs(ctx)
		}
	}
}

// ProcessOnce runs a single processing cycle (useful for tests and manual triggers).
func (w *Worker) ProcessOnce(ctx context.Context) {
	w.processDueJobs(ctx)
}

func (w *Worker) processDueJobs(ctx context.Context) {
	now := w.clock.Now()
	dueJobs, err := w.storage.GetDuePendingJobs(ctx, now, 100)
	if err != nil {
		w.logger.Error("Failed to fetch due pending notification jobs", "error", err)
		return
	}
	if len(dueJobs) == 0 {
		return
	}

	w.logger.Debug("Found due notification jobs", "count", len(dueJobs))

	// Group jobs to implement downtime catch-up rule:
	// "После простоя отправляется не более одного актуального напоминания по каждой несданной работе вместо серии накопившихся сообщений."
	jobsToProcess := w.applyDowntimeCatchup(ctx, dueJobs, now)

	for _, job := range jobsToProcess {
		if ctx.Err() != nil {
			return
		}
		w.handleSingleJob(ctx, job, now)
	}
}

func (w *Worker) applyDowntimeCatchup(ctx context.Context, dueJobs []domain.NotificationJob, now time.Time) []domain.NotificationJob {
	type userLabKey struct {
		userID int64
		labID  int64
	}

	reminderGroups := make(map[userLabKey][]domain.NotificationJob)
	var finalJobs []domain.NotificationJob

	for _, job := range dueJobs {
		isReminder := job.NotificationType == domain.Notify3d ||
			job.NotificationType == domain.Notify1d ||
			job.NotificationType == domain.Notify3h ||
			job.NotificationType == domain.NotifyOverdue

		if !isReminder {
			// Events like new_lab, lab_changed, lab_cancelled are distinct and not collapsed
			finalJobs = append(finalJobs, job)
			continue
		}

		key := userLabKey{userID: job.UserID, labID: job.LabID}
		reminderGroups[key] = append(reminderGroups[key], job)
	}

	// For each (user, lab) reminder group, pick at most ONE most relevant reminder
	for key, jobs := range reminderGroups {
		if len(jobs) == 1 {
			finalJobs = append(finalJobs, jobs[0])
			continue
		}

		// Find the best job:
		// If now > deadline: pick 'overdue' if present, otherwise latest scheduled.
		// If now <= deadline: pick the one closest to now (latest scheduled_at).
		var chosen domain.NotificationJob
		var maxScheduled time.Time

		// Check for overdue first
		var hasOverdue bool
		var overdueJob domain.NotificationJob
		for _, j := range jobs {
			if j.NotificationType == domain.NotifyOverdue {
				hasOverdue = true
				overdueJob = j
			}
			if j.ScheduledAt.After(maxScheduled) || maxScheduled.IsZero() {
				maxScheduled = j.ScheduledAt
				chosen = j
			}
		}

		// Check lab deadline
		lab, err := w.storage.GetLabByID(ctx, key.labID, key.userID)
		if err == nil && lab != nil && now.After(lab.DeadlineAt) && hasOverdue {
			chosen = overdueJob
		}

		finalJobs = append(finalJobs, chosen)

		// Cancel all superseded jobs in this group
		if err := w.storage.CancelSupersededDowntimeJobs(ctx, chosen.ID, key.userID, key.labID); err != nil {
			w.logger.Warn("Failed to cancel superseded downtime jobs", "error", err, "user_id", key.userID, "lab_id", key.labID)
		} else {
			w.logger.Info("Collapsed multiple accumulated reminders after downtime",
				"user_id", key.userID, "lab_id", key.labID, "kept_job_id", chosen.ID, "type", chosen.NotificationType)
		}
	}

	return finalJobs
}

func (w *Worker) handleSingleJob(ctx context.Context, job domain.NotificationJob, now time.Time) {
	// Attempt to transition status to processing
	ok, err := w.storage.MarkJobProcessing(ctx, job.ID)
	if err != nil {
		w.logger.Error("Failed to mark job processing", "job_id", job.ID, "error", err)
		return
	}
	if !ok {
		// Job was already claimed or cancelled
		return
	}

	// PRE-DISPATCH VALIDATIONS:
	// 1. User validation
	user, err := w.storage.GetUserByID(ctx, job.UserID)
	if err != nil || user == nil {
		_ = w.storage.MarkJobCancelled(ctx, job.ID)
		return
	}
	if user.Status != domain.UserStatusActive {
		_ = w.storage.MarkJobCancelled(ctx, job.ID)
		return
	}
	if user.IsBlocked {
		_ = w.storage.MarkJobCancelled(ctx, job.ID)
		return
	}

	// 2. Lab & Subject validation
	lab, err := w.storage.GetLabByID(ctx, job.LabID, job.UserID)
	if err != nil || lab == nil {
		_ = w.storage.MarkJobCancelled(ctx, job.ID)
		return
	}
	if lab.IsCancelled {
		_ = w.storage.MarkJobCancelled(ctx, job.ID)
		return
	}
	sub, err := w.storage.GetSubjectByID(ctx, lab.SubjectID)
	if err != nil || sub == nil || sub.IsArchived {
		_ = w.storage.MarkJobCancelled(ctx, job.ID)
		return
	}
	if job.DeadlineVersion != lab.DeadlineVersion {
		_ = w.storage.MarkJobCancelled(ctx, job.ID)
		return
	}

	// 3. Submission validation (for reminder types)
	isReminder := job.NotificationType == domain.Notify3d ||
		job.NotificationType == domain.Notify1d ||
		job.NotificationType == domain.Notify3h ||
		job.NotificationType == domain.NotifyOverdue

	if isReminder && lab.IsCompleted {
		_ = w.storage.MarkJobCancelled(ctx, job.ID)
		return
	}

	// 4. User settings validation
	settings, err := w.storage.GetUserSettings(ctx, job.UserID)
	if err != nil || settings == nil {
		def := domain.DefaultUserSettings(job.UserID, now)
		settings = &def
	}

	if isReminder {
		if !settings.RemindAll {
			_ = w.storage.MarkJobCancelled(ctx, job.ID)
			return
		}
		switch job.NotificationType {
		case domain.Notify3d:
			if !settings.Remind3d {
				_ = w.storage.MarkJobCancelled(ctx, job.ID)
				return
			}
		case domain.Notify1d:
			if !settings.Remind1d {
				_ = w.storage.MarkJobCancelled(ctx, job.ID)
				return
			}
		case domain.Notify3h:
			if !settings.Remind3h {
				_ = w.storage.MarkJobCancelled(ctx, job.ID)
				return
			}
		}
	} else if job.NotificationType == domain.NotifyNewLab && !settings.NotifyNewLab {
		_ = w.storage.MarkJobCancelled(ctx, job.ID)
		return
	} else if (job.NotificationType == domain.NotifyLabChanged || job.NotificationType == domain.NotifyLabCancelled) && !settings.NotifyChanges {
		_ = w.storage.MarkJobCancelled(ctx, job.ID)
		return
	}

	// Format notification message and inline buttons
	text, markup := w.formatMessage(job, lab)

	// Send message via Telegram API
	sendErr := w.sender.SendMessage(ctx, job.UserID, text, markup)
	if sendErr == nil {
		if err := w.storage.MarkJobSent(ctx, job.ID, now); err != nil {
			w.logger.Error("Failed to mark job as sent", "job_id", job.ID, "error", err)
		} else {
			w.logger.Info("Notification sent successfully",
				"job_id", job.ID, "user_id", job.UserID, "type", job.NotificationType, "lab_id", job.LabID)
		}
		return
	}

	// Handle Telegram API errors
	w.handleSendError(ctx, job, sendErr, now)
}

func (w *Worker) formatMessage(job domain.NotificationJob, lab *domain.Lab) (string, *tgbotapi.InlineKeyboardMarkup) {
	now := w.clock.Now()
	deadlineStr := w.timeHelper.FormatInGroupTZ(lab.DeadlineAt)
	remainingText, isOverdue := w.timeHelper.RemainingOrOverdue(lab.DeadlineAt, now)

	var sb strings.Builder
	var markup *tgbotapi.InlineKeyboardMarkup

	cardBtn := tgbotapi.NewInlineKeyboardButtonData("📖 Карточка работы", fmt.Sprintf("lab:v:%d", lab.ID))

	switch job.NotificationType {
	case domain.NotifyNewLab:
		sb.WriteString("🆕 <b>Новая лабораторная работа!</b>\n\n")
		sb.WriteString(fmt.Sprintf("📚 <b>Предмет:</b> %s\n", html.EscapeString(lab.SubjectName)))
		sb.WriteString(fmt.Sprintf("📝 <b>Работа:</b> №%s %s\n", html.EscapeString(lab.Number), html.EscapeString(lab.Title)))
		sb.WriteString(fmt.Sprintf("⏰ <b>Дедлайн:</b> %s\n", deadlineStr))
		sb.WriteString(fmt.Sprintf("%s\n", remainingText))
		if lab.Description != "" {
			sb.WriteString(fmt.Sprintf("\nℹ️ <i>%s</i>\n", html.EscapeString(lab.Description)))
		}
		btnRow := tgbotapi.NewInlineKeyboardRow(cardBtn)
		m := tgbotapi.NewInlineKeyboardMarkup(btnRow)
		markup = &m

	case domain.NotifyLabChanged:
		sb.WriteString("🔄 <b>Изменение дедлайна работы!</b>\n\n")
		sb.WriteString(fmt.Sprintf("📚 <b>Предмет:</b> %s\n", html.EscapeString(lab.SubjectName)))
		sb.WriteString(fmt.Sprintf("📝 <b>Работа:</b> №%s %s\n", html.EscapeString(lab.Number), html.EscapeString(lab.Title)))
		sb.WriteString(fmt.Sprintf("⏰ <b>Новый дедлайн:</b> %s\n", deadlineStr))
		sb.WriteString(fmt.Sprintf("%s\n", remainingText))
		btnRow := tgbotapi.NewInlineKeyboardRow(cardBtn)
		m := tgbotapi.NewInlineKeyboardMarkup(btnRow)
		markup = &m

	case domain.NotifyLabCancelled:
		sb.WriteString("❌ <b>Лабораторная работа отменена!</b>\n\n")
		sb.WriteString(fmt.Sprintf("📚 <b>Предмет:</b> %s\n", html.EscapeString(lab.SubjectName)))
		sb.WriteString(fmt.Sprintf("📝 <b>Работа:</b> №%s %s\n", html.EscapeString(lab.Number), html.EscapeString(lab.Title)))
		sb.WriteString("Срок сдачи аннулирован, дальнейшие напоминания отключены.")

	case domain.NotifyOverdue:
		sb.WriteString("⚠️ <b>Внимание: дедлайн наступил!</b>\n\n")
		sb.WriteString(fmt.Sprintf("📚 <b>Предмет:</b> %s\n", html.EscapeString(lab.SubjectName)))
		sb.WriteString(fmt.Sprintf("📝 <b>Работа:</b> №%s %s\n", html.EscapeString(lab.Number), html.EscapeString(lab.Title)))
		sb.WriteString(fmt.Sprintf("⏰ <b>Дедлайн:</b> %s\n", deadlineStr))
		sb.WriteString(fmt.Sprintf("%s\n\n", remainingText))
		sb.WriteString("Если вы уже сдали работу, отметьте её кнопкой ниже.")

		doneBtn := tgbotapi.NewInlineKeyboardButtonData("✅ Отметить как сданную", fmt.Sprintf("lab:done:%d", lab.ID))
		btnRow := tgbotapi.NewInlineKeyboardRow(cardBtn, doneBtn)
		m := tgbotapi.NewInlineKeyboardMarkup(btnRow)
		markup = &m

	default: // 3d, 1d, 3h
		var header string
		switch job.NotificationType {
		case domain.Notify3d:
			header = "🔔 <b>Напоминание: 3 дня до дедлайна!</b>"
		case domain.Notify1d:
			header = "⚡ <b>Напоминание: 1 день до дедлайна!</b>"
		case domain.Notify3h:
			header = "🔥 <b>Срочно: 3 часа до дедлайна!</b>"
		}
		sb.WriteString(fmt.Sprintf("%s\n\n", header))
		sb.WriteString(fmt.Sprintf("📚 <b>Предмет:</b> %s\n", html.EscapeString(lab.SubjectName)))
		sb.WriteString(fmt.Sprintf("📝 <b>Работа:</b> №%s %s\n", html.EscapeString(lab.Number), html.EscapeString(lab.Title)))
		sb.WriteString(fmt.Sprintf("⏰ <b>Точный дедлайн:</b> %s\n", deadlineStr))
		if !isOverdue {
			sb.WriteString(fmt.Sprintf("%s\n", remainingText))
		}

		doneBtn := tgbotapi.NewInlineKeyboardButtonData("✅ Отметить как сданную", fmt.Sprintf("lab:done:%d", lab.ID))
		btnRow := tgbotapi.NewInlineKeyboardRow(cardBtn, doneBtn)
		m := tgbotapi.NewInlineKeyboardMarkup(btnRow)
		markup = &m
	}

	return sb.String(), markup
}

func (w *Worker) handleSendError(ctx context.Context, job domain.NotificationJob, err error, now time.Time) {
	errStr := err.Error()
	w.logger.Warn("Failed to send notification via Telegram",
		"job_id", job.ID, "user_id", job.UserID, "error", errStr)

	// Permanent error: user blocked the bot or chat not found
	if strings.Contains(errStr, "Forbidden: bot was blocked by the user") ||
		strings.Contains(errStr, "chat not found") ||
		strings.Contains(errStr, "user is deactivated") {
		w.logger.Warn("User has blocked the bot or deactivated account. Suspending notifications.", "user_id", job.UserID)
		_ = w.storage.SetUserBlocked(ctx, job.UserID, true, now)
		_ = w.storage.MarkJobFailed(ctx, job.ID, errStr, job.RetryCount+1)
		return
	}

	// Check retry count (max 3 retries)
	const maxRetries = 3
	if job.RetryCount >= maxRetries {
		w.logger.Error("Job exceeded maximum retries, marking failed", "job_id", job.ID, "retries", job.RetryCount)
		_ = w.storage.MarkJobFailed(ctx, job.ID, errStr, job.RetryCount+1)
		return
	}

	// Calculate delay
	retryDelay := time.Duration(15*(1<<job.RetryCount)) * time.Second

	// Check if Telegram returned retry_after
	if strings.Contains(errStr, "Too Many Requests: retry after") {
		parts := strings.Split(errStr, "retry after ")
		if len(parts) > 1 {
			secondsStr := strings.Fields(parts[1])[0]
			if sec, parseErr := strconv.Atoi(secondsStr); parseErr == nil && sec > 0 {
				retryDelay = time.Duration(sec+1) * time.Second
			}
		}
	}

	nextSchedule := now.Add(retryDelay)
	_ = w.storage.MarkJobRetryPending(ctx, job.ID, errStr, job.RetryCount+1, nextSchedule)
	w.logger.Info("Scheduled job retry", "job_id", job.ID, "attempt", job.RetryCount+1, "retry_in", retryDelay.String())
}

// TelegramBotSender implements TelegramSender using tgbotapi.BotAPI
type TelegramBotSender struct {
	bot *tgbotapi.BotAPI
}

func NewTelegramBotSender(bot *tgbotapi.BotAPI) *TelegramBotSender {
	return &TelegramBotSender{bot: bot}
}

func (s *TelegramBotSender) SendMessage(ctx context.Context, chatID int64, text string, replyMarkup *tgbotapi.InlineKeyboardMarkup) error {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeHTML
	msg.DisableWebPagePreview = true
	if replyMarkup != nil {
		msg.ReplyMarkup = replyMarkup
	}

	_, err := s.bot.Send(msg)
	if err != nil {
		return err
	}
	return nil
}

// MockTelegramSender for unit and integration testing
type MockTelegramSender struct {
	mu       sync.Mutex
	Sent     []SentMessage
	FailFunc func(chatID int64, text string) error
}

type SentMessage struct {
	ChatID      int64
	Text        string
	ReplyMarkup *tgbotapi.InlineKeyboardMarkup
}

func NewMockTelegramSender() *MockTelegramSender {
	return &MockTelegramSender{}
}

func (m *MockTelegramSender) SendMessage(ctx context.Context, chatID int64, text string, replyMarkup *tgbotapi.InlineKeyboardMarkup) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.FailFunc != nil {
		if err := m.FailFunc(chatID, text); err != nil {
			return err
		}
	}

	m.Sent = append(m.Sent, SentMessage{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: replyMarkup,
	})
	return nil
}

func (m *MockTelegramSender) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Sent)
}

func (m *MockTelegramSender) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Sent = nil
}
