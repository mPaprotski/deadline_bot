package notifications_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"deadline_bot/internal/domain"
	"deadline_bot/internal/notifications"
	"deadline_bot/internal/service"
	"deadline_bot/internal/storage/sqlite"
)

func setupWorkerEnv(t *testing.T) (*sqlite.Storage, *service.MockClock, *notifications.MockTelegramSender, *notifications.Worker) {
	t.Helper()
	ctx := context.Background()

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "worker_test.db")

	storage, err := sqlite.New(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	t.Cleanup(func() {
		_ = storage.Close()
	})

	if err := storage.Migrate(ctx); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	initialTime := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	clock := service.NewMockClock(initialTime)
	loc, _ := time.LoadLocation("Europe/Minsk")
	th := service.NewTimeHelper(loc)

	sender := notifications.NewMockTelegramSender()
	worker := notifications.NewWorker(storage, sender, clock, th, time.Minute, nil)

	return storage, clock, sender, worker
}

func TestWorker_RestartRecovery(t *testing.T) {
	ctx := context.Background()
	storage, clock, sender, worker := setupWorkerEnv(t)

	// Create group, user, subject, lab
	now := clock.Now()
	grp, _ := storage.CreateGroup(ctx, "Группа 1", "INVITE1", now)
	user := &domain.User{
		ID:        101,
		GroupID:   &grp.ID,
		Username:  "student1",
		FirstName: "Алексей",
		Role:      domain.RoleStudent,
		Status:    domain.UserStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = storage.UpsertUser(ctx, user)
	settings := domain.DefaultUserSettings(101, now)
	_ = storage.UpsertUserSettings(ctx, &settings)

	sub, _ := storage.CreateSubject(ctx, grp.ID, "Философия", now)
	lab, _ := storage.CreateLab(ctx, &domain.Lab{
		GroupID:          grp.ID,
		SubjectID:        sub.ID,
		Number:           "1",
		Title:            "Этика",
		DeadlineAt:       now.Add(24 * time.Hour),
		DeadlineVersion:  1,
		CreatedBy:        101,
		CreatedAt:        now,
		UpdatedAt:        now,
	})

	// Simulate a job interrupted during crash: status = 'processing'
	job := domain.NotificationJob{
		UserID:           101,
		LabID:            lab.ID,
		DeadlineVersion:  1,
		NotificationType: domain.Notify1d,
		ScheduledAt:      now.Add(-time.Minute), // due in the past
		Status:           domain.StatusProcessing,
		CreatedAt:        now,
	}
	_ = storage.CreateNotificationJobs(ctx, []domain.NotificationJob{job})
	// Manually set status to processing in DB
	_, _ = storage.DB().ExecContext(ctx, "UPDATE notification_jobs SET status = 'processing' WHERE user_id = ? AND lab_id = ?", 101, lab.ID)

	// 1. Recover interrupted jobs
	recovered, err := storage.RecoverInterruptedProcessingJobs(ctx)
	if err != nil {
		t.Fatalf("failed to recover: %v", err)
	}
	if recovered != 1 {
		t.Errorf("expected 1 recovered job, got %d", recovered)
	}

	// 2. Process jobs with worker
	worker.ProcessOnce(ctx)

	// Job should now be 'sent' and Telegram sender received 1 message
	if sender.Count() != 1 {
		t.Errorf("expected 1 message sent, got %d", sender.Count())
	}

	var status string
	_ = storage.DB().QueryRowContext(ctx, "SELECT status FROM notification_jobs WHERE user_id = ? AND lab_id = ?", 101, lab.ID).Scan(&status)
	if status != "sent" {
		t.Errorf("expected job status 'sent', got %s", status)
	}

	// 3. Running process again should NOT resend the job
	worker.ProcessOnce(ctx)
	if sender.Count() != 1 {
		t.Errorf("expected still 1 message (no duplicate resend), got %d", sender.Count())
	}
}

func TestWorker_DowntimeCatchup(t *testing.T) {
	ctx := context.Background()
	storage, clock, sender, worker := setupWorkerEnv(t)

	now := clock.Now()
	grp, _ := storage.CreateGroup(ctx, "Группа 1", "INVITE1", now)
	user := &domain.User{
		ID:        102,
		GroupID:   &grp.ID,
		Username:  "student2",
		FirstName: "Борис",
		Role:      domain.RoleStudent,
		Status:    domain.UserStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = storage.UpsertUser(ctx, user)
	settings := domain.DefaultUserSettings(102, now)
	_ = storage.UpsertUserSettings(ctx, &settings)

	sub, _ := storage.CreateSubject(ctx, grp.ID, "Физика", now)
	deadline := now.Add(2 * time.Hour) // deadline in 2 hours
	lab, _ := storage.CreateLab(ctx, &domain.Lab{
		GroupID:          grp.ID,
		SubjectID:        sub.ID,
		Number:           "2",
		Title:            "Оптика",
		DeadlineAt:       deadline,
		DeadlineVersion:  1,
		CreatedBy:        102,
		CreatedAt:        now,
		UpdatedAt:        now,
	})

	// Simulate bot downtime: 3d, 1d, 3h reminders all accumulated in 'pending'
	jobs := []domain.NotificationJob{
		{
			UserID:           102,
			LabID:            lab.ID,
			DeadlineVersion:  1,
			NotificationType: domain.Notify3d,
			ScheduledAt:      now.Add(-72 * time.Hour),
			Status:           domain.StatusPending,
			CreatedAt:        now,
		},
		{
			UserID:           102,
			LabID:            lab.ID,
			DeadlineVersion:  1,
			NotificationType: domain.Notify1d,
			ScheduledAt:      now.Add(-24 * time.Hour),
			Status:           domain.StatusPending,
			CreatedAt:        now,
		},
		{
			UserID:           102,
			LabID:            lab.ID,
			DeadlineVersion:  1,
			NotificationType: domain.Notify3h,
			ScheduledAt:      now.Add(-3 * time.Hour),
			Status:           domain.StatusPending,
			CreatedAt:        now,
		},
	}
	_ = storage.CreateNotificationJobs(ctx, jobs)

	// Run worker cycle
	worker.ProcessOnce(ctx)

	// Requirement: "После простоя отправляется не более одного актуального напоминания по каждой несданной работе вместо серии накопившихся сообщений."
	if sender.Count() != 1 {
		t.Errorf("expected exactly 1 collapsed reminder sent after downtime, got %d", sender.Count())
	}

	// Verify that the other 2 jobs were cancelled with superseded notice
	var cancelledCount int
	_ = storage.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_jobs WHERE user_id = 102 AND lab_id = ? AND status = 'cancelled'", lab.ID).Scan(&cancelledCount)
	if cancelledCount != 2 {
		t.Errorf("expected 2 superseded jobs cancelled, got %d", cancelledCount)
	}
}

func TestWorker_BotBlockedHandling(t *testing.T) {
	ctx := context.Background()
	storage, clock, sender, worker := setupWorkerEnv(t)

	now := clock.Now()
	grp, _ := storage.CreateGroup(ctx, "Группа 1", "INVITE1", now)
	user := &domain.User{
		ID:        103,
		GroupID:   &grp.ID,
		Username:  "blocked_user",
		FirstName: "Василий",
		Role:      domain.RoleStudent,
		Status:    domain.UserStatusActive,
		IsBlocked: false,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = storage.UpsertUser(ctx, user)
	settings := domain.DefaultUserSettings(103, now)
	_ = storage.UpsertUserSettings(ctx, &settings)

	sub, _ := storage.CreateSubject(ctx, grp.ID, "Химия", now)
	lab, _ := storage.CreateLab(ctx, &domain.Lab{
		GroupID:          grp.ID,
		SubjectID:        sub.ID,
		Number:           "1",
		Title:            "Органическая химия",
		DeadlineAt:       now.Add(24 * time.Hour),
		DeadlineVersion:  1,
		CreatedBy:        103,
		CreatedAt:        now,
		UpdatedAt:        now,
	})

	_ = storage.CreateNotificationJobs(ctx, []domain.NotificationJob{
		{
			UserID:           103,
			LabID:            lab.ID,
			DeadlineVersion:  1,
			NotificationType: domain.Notify1d,
			ScheduledAt:      now.Add(-time.Minute),
			Status:           domain.StatusPending,
			CreatedAt:        now,
		},
	})

	// Configure sender to return 403 Forbidden
	sender.FailFunc = func(chatID int64, text string) error {
		return errors.New("Forbidden: bot was blocked by the user")
	}

	worker.ProcessOnce(ctx)

	// User should now be marked is_blocked = true
	u, _ := storage.GetUserByID(ctx, 103)
	if !u.IsBlocked {
		t.Errorf("expected user to be marked as is_blocked = true")
	}
}
