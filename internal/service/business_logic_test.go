package service_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"deadline_bot/internal/domain"
	"deadline_bot/internal/service"
	"deadline_bot/internal/storage/sqlite"
)

func setupTestEnv(t *testing.T) (*sqlite.Storage, *service.MockClock, *service.UserService, *service.SubjectService, *service.LabService, *service.SettingsService) {
	t.Helper()
	ctx := context.Background()

	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "test.db")

	storage, err := sqlite.New(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("failed to create test storage: %v", err)
	}
	t.Cleanup(func() {
		_ = storage.Close()
	})

	if err := storage.Migrate(ctx); err != nil {
		t.Fatalf("failed to run migrations: %v", err)
	}

	initialTime := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	clock := service.NewMockClock(initialTime)

	loc, _ := time.LoadLocation("Europe/Minsk")
	th := service.NewTimeHelper(loc)

	ownerID := int64(1001)
	userService := service.NewUserService(storage, clock, ownerID)
	subjectService := service.NewSubjectService(storage, clock)
	labService := service.NewLabService(storage, clock, th)
	settingsService := service.NewSettingsService(storage, clock)

	return storage, clock, userService, subjectService, labService, settingsService
}

func TestAccessControl(t *testing.T) {
	ctx := context.Background()
	storage, _, userService, subjectService, labService, _ := setupTestEnv(t)

	// Create default group
	grp, err := userService.EnsureDefaultGroup(ctx, "ИВТ-21", "CODE2026")
	if err != nil {
		t.Fatalf("failed to create group: %v", err)
	}

	ownerID := int64(1001)
	adminID := int64(2001)
	studentID := int64(3001)

	// Register users
	_, _ = userService.EnsureUser(ctx, ownerID, "owner", "Главный", "Староста")
	_, _ = userService.EnsureUser(ctx, adminID, "admin", "Заместитель", "Старосты")
	_, _ = userService.EnsureUser(ctx, studentID, "student", "Иван", "Иванов")

	// Join group
	_, _ = userService.JoinGroup(ctx, adminID, "CODE2026")
	_, _ = userService.JoinGroup(ctx, studentID, "CODE2026")

	// Owner promotes adminUser to Admin
	if err := userService.PromoteToAdmin(ctx, ownerID, adminID); err != nil {
		t.Fatalf("owner should be able to promote admin: %v", err)
	}

	// 1. Student permissions: should NOT be able to create subject or lab
	_, err = subjectService.CreateSubject(ctx, studentID, grp.ID, "Физика")
	if err == nil {
		t.Errorf("student must not be allowed to create subjects")
	}

	_, err = labService.CreateLab(ctx, service.CreateLabInput{
		AdminID:    studentID,
		GroupID:    grp.ID,
		SubjectID:  1,
		Number:     "1",
		Title:      "Тест",
		DeadlineAt: time.Now().Add(24 * time.Hour),
	})
	if err == nil {
		t.Errorf("student must not be allowed to create labs")
	}

	// 2. Admin permissions: can create subject and lab
	_, err = subjectService.CreateSubject(ctx, adminID, grp.ID, "Математика")
	if err != nil {
		t.Fatalf("admin should be able to create subject: %v", err)
	}

	// 3. Admin cannot promote or demote other users
	err = userService.PromoteToAdmin(ctx, adminID, studentID)
	if err == nil {
		t.Errorf("admin must not be allowed to promote users to admin")
	}

	// 4. Admin cannot revoke Owner
	err = userService.RevokeStudentAccess(ctx, adminID, ownerID)
	if err == nil {
		t.Errorf("admin must not be allowed to revoke owner")
	}

	// 5. Admin can revoke student
	err = userService.RevokeStudentAccess(ctx, adminID, studentID)
	if err != nil {
		t.Fatalf("admin should be able to revoke student: %v", err)
	}

	u, _ := storage.GetUserByID(ctx, studentID)
	if u.Status != domain.UserStatusRevoked {
		t.Errorf("expected student status to be revoked, got %s", u.Status)
	}
}

func TestNotificationScheduling_LabCreation(t *testing.T) {
	ctx := context.Background()
	storage, clock, userService, subjectService, labService, _ := setupTestEnv(t)

	grp, _ := userService.EnsureDefaultGroup(ctx, "ИВТ-21", "CODE2026")
	ownerID := int64(1001)
	studentID := int64(3001)

	_, _ = userService.EnsureUser(ctx, ownerID, "owner", "Староста", "")
	_, _ = userService.EnsureUser(ctx, studentID, "student", "Студент", "")
	_, _ = userService.JoinGroup(ctx, studentID, "CODE2026")

	sub, _ := subjectService.CreateSubject(ctx, ownerID, grp.ID, "Информатика")

	// Current time: 2026-10-01 10:00:00 UTC
	now := clock.Now()
	// Deadline: 2026-10-10 10:00:00 UTC (9 days in future)
	deadline := now.Add(9 * 24 * time.Hour)

	lab, err := labService.CreateLab(ctx, service.CreateLabInput{
		AdminID:    ownerID,
		GroupID:    grp.ID,
		SubjectID:  sub.ID,
		Number:     "1",
		Title:      "Алгоритмы",
		DeadlineAt: deadline,
	})
	if err != nil {
		t.Fatalf("failed to create lab: %v", err)
	}

	// Check scheduled jobs for student
	dueJobs, err := storage.GetDuePendingJobs(ctx, deadline.Add(time.Hour), 100)
	if err != nil {
		t.Fatalf("failed to query jobs: %v", err)
	}

	var foundNewLab, found3d, found1d, found3h, foundOverdue bool
	for _, j := range dueJobs {
		if j.UserID == studentID && j.LabID == lab.ID {
			switch j.NotificationType {
			case domain.NotifyNewLab:
				foundNewLab = true
				if !j.ScheduledAt.Equal(now) {
					t.Errorf("new_lab should be scheduled at now, got %v", j.ScheduledAt)
				}
			case domain.Notify3d:
				found3d = true
				expected := deadline.Add(-72 * time.Hour)
				if !j.ScheduledAt.Equal(expected) {
					t.Errorf("3d reminder expected at %v, got %v", expected, j.ScheduledAt)
				}
			case domain.Notify1d:
				found1d = true
				expected := deadline.Add(-24 * time.Hour)
				if !j.ScheduledAt.Equal(expected) {
					t.Errorf("1d reminder expected at %v, got %v", expected, j.ScheduledAt)
				}
			case domain.Notify3h:
				found3h = true
				expected := deadline.Add(-3 * time.Hour)
				if !j.ScheduledAt.Equal(expected) {
					t.Errorf("3h reminder expected at %v, got %v", expected, j.ScheduledAt)
				}
			case domain.NotifyOverdue:
				foundOverdue = true
				if !j.ScheduledAt.Equal(deadline) {
					t.Errorf("overdue expected at %v, got %v", deadline, j.ScheduledAt)
				}
			}
		}
	}

	if !foundNewLab || !found3d || !found1d || !found3h || !foundOverdue {
		t.Errorf("missing scheduled reminders: new=%v 3d=%v 1d=%v 3h=%v overdue=%v",
			foundNewLab, found3d, found1d, found3h, foundOverdue)
	}
}

func TestRescheduleAndCancellation(t *testing.T) {
	ctx := context.Background()
	storage, clock, userService, subjectService, labService, _ := setupTestEnv(t)

	grp, _ := userService.EnsureDefaultGroup(ctx, "ИВТ-21", "CODE2026")
	ownerID := int64(1001)
	studentID := int64(3001)

	_, _ = userService.EnsureUser(ctx, ownerID, "owner", "Староста", "")
	_, _ = userService.EnsureUser(ctx, studentID, "student", "Студент", "")
	_, _ = userService.JoinGroup(ctx, studentID, "CODE2026")
	sub, _ := subjectService.CreateSubject(ctx, ownerID, grp.ID, "Сети")

	now := clock.Now()
	deadline1 := now.Add(5 * 24 * time.Hour)

	lab, _ := labService.CreateLab(ctx, service.CreateLabInput{
		AdminID:    ownerID,
		GroupID:    grp.ID,
		SubjectID:  sub.ID,
		Number:     "2",
		Title:      "TCP/IP",
		DeadlineAt: deadline1,
	})

	// 1. Reschedule lab to a new date
	deadline2 := now.Add(10 * 24 * time.Hour)
	oldDL, newDL, newVer, err := labService.RescheduleLabDeadline(ctx, ownerID, lab.ID, deadline2, false)
	if err != nil {
		t.Fatalf("failed to reschedule lab: %v", err)
	}
	if newVer != 2 {
		t.Errorf("expected version 2, got %d", newVer)
	}
	if !oldDL.Equal(deadline1) || !newDL.Equal(deadline2) {
		t.Errorf("unexpected dates: old=%v new=%v", oldDL, newDL)
	}

	// Verify old version (v1) pending reminder jobs were cancelled
	var v1PendingCount int
	_ = storage.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_jobs WHERE lab_id = ? AND deadline_version = 1 AND status = 'pending' AND notification_type != 'new_lab'", lab.ID).Scan(&v1PendingCount)
	if v1PendingCount > 0 {
		t.Errorf("expected 0 v1 pending reminders, got %d", v1PendingCount)
	}

	// Verify lab_changed notification was scheduled
	var changedNotifCount int
	_ = storage.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_jobs WHERE lab_id = ? AND deadline_version = 2 AND notification_type = 'lab_changed' AND status = 'pending'", lab.ID).Scan(&changedNotifCount)
	if changedNotifCount != 1 {
		t.Errorf("expected 1 lab_changed notification job, got %d", changedNotifCount)
	}

	// 2. Cancel lab
	err = labService.CancelLab(ctx, ownerID, lab.ID)
	if err != nil {
		t.Fatalf("failed to cancel lab: %v", err)
	}

	updatedLab, _ := labService.GetLab(ctx, lab.ID, studentID)
	if !updatedLab.IsCancelled {
		t.Errorf("expected lab to be marked cancelled")
	}

	// Verify all pending reminder jobs for this lab are cancelled
	var pendingReminderCount int
	_ = storage.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_jobs WHERE lab_id = ? AND status = 'pending' AND notification_type IN ('3d', '1d', '3h', 'overdue')", lab.ID).Scan(&pendingReminderCount)
	if pendingReminderCount > 0 {
		t.Errorf("expected 0 pending reminders after cancellation, got %d", pendingReminderCount)
	}

	// Verify lab_cancelled notification was scheduled
	var cancelledNotifCount int
	_ = storage.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_jobs WHERE lab_id = ? AND notification_type = 'lab_cancelled' AND status = 'pending'", lab.ID).Scan(&cancelledNotifCount)
	if cancelledNotifCount != 1 {
		t.Errorf("expected 1 lab_cancelled notification, got %d", cancelledNotifCount)
	}
}

func TestPersonalCompletionMarks(t *testing.T) {
	ctx := context.Background()
	storage, clock, userService, subjectService, labService, _ := setupTestEnv(t)

	grp, _ := userService.EnsureDefaultGroup(ctx, "ИВТ-21", "CODE2026")
	ownerID := int64(1001)
	studentID := int64(3001)

	_, _ = userService.EnsureUser(ctx, ownerID, "owner", "Староста", "")
	_, _ = userService.EnsureUser(ctx, studentID, "student", "Студент", "")
	_, _ = userService.JoinGroup(ctx, studentID, "CODE2026")
	sub, _ := subjectService.CreateSubject(ctx, ownerID, grp.ID, "БД")

	now := clock.Now()
	deadline := now.Add(5 * 24 * time.Hour)

	lab, _ := labService.CreateLab(ctx, service.CreateLabInput{
		AdminID:    ownerID,
		GroupID:    grp.ID,
		SubjectID:  sub.ID,
		Number:     "3",
		Title:      "SQL",
		DeadlineAt: deadline,
	})

	// Student marks completed
	err := labService.MarkCompleted(ctx, studentID, lab.ID)
	if err != nil {
		t.Fatalf("failed to mark lab completed: %v", err)
	}

	isCompleted, _ := storage.IsLabCompleted(ctx, studentID, lab.ID)
	if !isCompleted {
		t.Errorf("expected lab to be marked completed")
	}

	// Pending reminders for student should be cancelled
	var pendingCount int
	_ = storage.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_jobs WHERE user_id = ? AND lab_id = ? AND status = 'pending' AND notification_type IN ('3d', '1d', '3h', 'overdue')", studentID, lab.ID).Scan(&pendingCount)
	if pendingCount > 0 {
		t.Errorf("expected 0 pending reminders after marking completed, got %d", pendingCount)
	}

	// Student unmarks completed: future reminders must be restored
	err = labService.UnmarkCompleted(ctx, studentID, lab.ID)
	if err != nil {
		t.Fatalf("failed to unmark lab completed: %v", err)
	}

	isCompleted, _ = storage.IsLabCompleted(ctx, studentID, lab.ID)
	if isCompleted {
		t.Errorf("expected lab to be unmarked")
	}

	_ = storage.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_jobs WHERE user_id = ? AND lab_id = ? AND status = 'pending' AND notification_type IN ('3d', '1d', '3h', 'overdue')", studentID, lab.ID).Scan(&pendingCount)
	if pendingCount == 0 {
		t.Errorf("expected future reminders to be restored after unmarking completed")
	}
}

func TestNotificationSettingsToggles(t *testing.T) {
	ctx := context.Background()
	storage, clock, userService, subjectService, labService, settingsService := setupTestEnv(t)

	grp, _ := userService.EnsureDefaultGroup(ctx, "ИВТ-21", "CODE2026")
	ownerID := int64(1001)
	studentID := int64(3001)

	_, _ = userService.EnsureUser(ctx, ownerID, "owner", "Староста", "")
	_, _ = userService.EnsureUser(ctx, studentID, "student", "Студент", "")
	_, _ = userService.JoinGroup(ctx, studentID, "CODE2026")
	sub, _ := subjectService.CreateSubject(ctx, ownerID, grp.ID, "ОС")

	now := clock.Now()
	deadline := now.Add(5 * 24 * time.Hour)

	lab, _ := labService.CreateLab(ctx, service.CreateLabInput{
		AdminID:    ownerID,
		GroupID:    grp.ID,
		SubjectID:  sub.ID,
		Number:     "4",
		Title:      "Процессы",
		DeadlineAt: deadline,
	})

	// Disable 3d reminders
	_, err := settingsService.ToggleSetting(ctx, studentID, "3d")
	if err != nil {
		t.Fatalf("failed to toggle 3d: %v", err)
	}

	var count3d int
	_ = storage.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_jobs WHERE user_id = ? AND lab_id = ? AND notification_type = '3d' AND status = 'pending'", studentID, lab.ID).Scan(&count3d)
	if count3d > 0 {
		t.Errorf("expected 0 pending 3d jobs after disabling 3d, got %d", count3d)
	}

	// Disable all reminders
	_, err = settingsService.ToggleSetting(ctx, studentID, "all")
	if err != nil {
		t.Fatalf("failed to toggle all: %v", err)
	}

	var countAll int
	_ = storage.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_jobs WHERE user_id = ? AND lab_id = ? AND status = 'pending' AND notification_type IN ('3d', '1d', '3h', 'overdue')", studentID, lab.ID).Scan(&countAll)
	if countAll > 0 {
		t.Errorf("expected 0 pending reminders after disabling remind_all, got %d", countAll)
	}

	// Enable all reminders again: should recalculate future reminders
	_, err = settingsService.ToggleSetting(ctx, studentID, "all")
	if err != nil {
		t.Fatalf("failed to enable remind_all: %v", err)
	}

	_ = storage.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_jobs WHERE user_id = ? AND lab_id = ? AND status = 'pending' AND notification_type IN ('1d', '3h', 'overdue')", studentID, lab.ID).Scan(&countAll)
	if countAll == 0 {
		t.Errorf("expected future reminders to be restored after re-enabling remind_all")
	}
}

func TestJobDeduplication(t *testing.T) {
	ctx := context.Background()
	storage, clock, userService, subjectService, labService, _ := setupTestEnv(t)

	grp, _ := userService.EnsureDefaultGroup(ctx, "ИВТ-21", "CODE2026")
	ownerID := int64(1001)
	studentID := int64(3001)

	_, _ = userService.EnsureUser(ctx, ownerID, "owner", "Староста", "")
	_, _ = userService.EnsureUser(ctx, studentID, "student", "Студент", "")
	_, _ = userService.JoinGroup(ctx, studentID, "CODE2026")
	sub, _ := subjectService.CreateSubject(ctx, ownerID, grp.ID, "Архитектура")

	now := clock.Now()
	lab, _ := labService.CreateLab(ctx, service.CreateLabInput{
		AdminID:    ownerID,
		GroupID:    grp.ID,
		SubjectID:  sub.ID,
		Number:     "5",
		Title:      "RISC-V",
		DeadlineAt: now.Add(5 * 24 * time.Hour),
	})

	// Attempt to insert duplicate jobs
	duplicateJobs := []domain.NotificationJob{
		{
			UserID:           studentID,
			LabID:            lab.ID,
			DeadlineVersion:  1,
			NotificationType: domain.Notify3d,
			ScheduledAt:      now.Add(24 * time.Hour),
			Status:           domain.StatusPending,
			CreatedAt:        now,
		},
		{
			UserID:           studentID,
			LabID:            lab.ID,
			DeadlineVersion:  1,
			NotificationType: domain.Notify3d,
			ScheduledAt:      now.Add(24 * time.Hour),
			Status:           domain.StatusPending,
			CreatedAt:        now,
		},
	}

	err := storage.CreateNotificationJobs(ctx, duplicateJobs)
	if err != nil {
		t.Fatalf("expected duplicate jobs insertion to succeed silently via ON CONFLICT, got: %v", err)
	}

	var count int
	_ = storage.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_jobs WHERE user_id = ? AND lab_id = ? AND deadline_version = 1 AND notification_type = '3d'", studentID, lab.ID).Scan(&count)
	if count != 1 {
		t.Errorf("expected exactly 1 job for (user, lab, version, type), got %d", count)
	}
}
