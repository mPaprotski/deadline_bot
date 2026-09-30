package service

import (
	"context"
	"fmt"
	"time"

	"deadline_bot/internal/domain"
	"deadline_bot/internal/storage/sqlite"
)

type SettingsService struct {
	storage *sqlite.Storage
	clock   Clock
}

func NewSettingsService(storage *sqlite.Storage, clock Clock) *SettingsService {
	if clock == nil {
		clock = RealClock{}
	}
	return &SettingsService{
		storage: storage,
		clock:   clock,
	}
}

func (s *SettingsService) GetSettings(ctx context.Context, userID int64) (*domain.UserSettings, error) {
	st, err := s.storage.GetUserSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	if st == nil {
		def := domain.DefaultUserSettings(userID, s.clock.Now())
		if err := s.storage.UpsertUserSettings(ctx, &def); err != nil {
			return nil, err
		}
		return &def, nil
	}
	return st, nil
}

// ToggleSetting toggles one of the setting switches and recalculates future reminders.
// Requirement: "После изменения настроек пересчитываются будущие уведомления; прошедшие напоминания не отправляются повторно."
func (s *SettingsService) ToggleSetting(ctx context.Context, userID int64, key string) (*domain.UserSettings, error) {
	now := s.clock.Now()
	st, err := s.GetSettings(ctx, userID)
	if err != nil {
		return nil, err
	}

	user, err := s.storage.GetUserByID(ctx, userID)
	if err != nil || user == nil || user.GroupID == nil {
		return nil, ErrUserNotFound
	}
	groupID := *user.GroupID

	switch key {
	case "all":
		st.RemindAll = !st.RemindAll
		if !st.RemindAll {
			// Cancel all pending reminders
			query := `
				UPDATE notification_jobs
				SET status = 'cancelled'
				WHERE user_id = ? AND status = 'pending' AND notification_type IN ('3d', '1d', '3h', 'overdue')
			`
			_, _ = s.storage.DB().ExecContext(ctx, query, userID)
		} else {
			// Recalculate future reminders for all active, uncompleted labs
			s.recalculateFutureReminders(ctx, userID, groupID, st, now)
		}

	case "3d":
		st.Remind3d = !st.Remind3d
		if !st.Remind3d {
			_, _ = s.storage.DB().ExecContext(ctx, "UPDATE notification_jobs SET status = 'cancelled' WHERE user_id = ? AND status = 'pending' AND notification_type = '3d'", userID)
		} else if st.RemindAll {
			s.scheduleSpecificReminder(ctx, userID, groupID, domain.Notify3d, -72*time.Hour, now)
		}

	case "1d":
		st.Remind1d = !st.Remind1d
		if !st.Remind1d {
			_, _ = s.storage.DB().ExecContext(ctx, "UPDATE notification_jobs SET status = 'cancelled' WHERE user_id = ? AND status = 'pending' AND notification_type = '1d'", userID)
		} else if st.RemindAll {
			s.scheduleSpecificReminder(ctx, userID, groupID, domain.Notify1d, -24*time.Hour, now)
		}

	case "3h":
		st.Remind3h = !st.Remind3h
		if !st.Remind3h {
			_, _ = s.storage.DB().ExecContext(ctx, "UPDATE notification_jobs SET status = 'cancelled' WHERE user_id = ? AND status = 'pending' AND notification_type = '3h'", userID)
		} else if st.RemindAll {
			s.scheduleSpecificReminder(ctx, userID, groupID, domain.Notify3h, -3*time.Hour, now)
		}

	case "new_lab":
		st.NotifyNewLab = !st.NotifyNewLab

	case "changes":
		st.NotifyChanges = !st.NotifyChanges

	default:
		return nil, fmt.Errorf("unknown setting key %q", key)
	}

	st.UpdatedAt = now
	if err := s.storage.UpsertUserSettings(ctx, st); err != nil {
		return nil, fmt.Errorf("failed to save settings: %w", err)
	}

	return st, nil
}

func (s *SettingsService) recalculateFutureReminders(ctx context.Context, userID int64, groupID int64, st *domain.UserSettings, now time.Time) {
	// Query active labs with future deadlines that are not completed by this user
	query := `
		SELECT l.id, l.deadline_at, l.deadline_version
		FROM labs l
		JOIN subjects s ON l.subject_id = s.id
		WHERE l.group_id = ? AND l.is_cancelled = 0 AND s.is_archived = 0
		  AND l.deadline_at >= ?
		  AND NOT EXISTS (SELECT 1 FROM student_submissions sub WHERE sub.user_id = ? AND sub.lab_id = l.id)
	`
	rows, err := s.storage.DB().QueryContext(ctx, query, groupID, now.Unix(), userID)
	if err != nil {
		return
	}
	defer rows.Close()

	var jobs []domain.NotificationJob
	for rows.Next() {
		var labID int64
		var deadlineAt int64
		var version int
		if err := rows.Scan(&labID, &deadlineAt, &version); err != nil {
			continue
		}
		deadline := time.Unix(deadlineAt, 0).UTC()

		if st.Remind3d {
			t := deadline.Add(-72 * time.Hour)
			if t.After(now) {
				jobs = append(jobs, domain.NotificationJob{
					UserID:           userID,
					LabID:            labID,
					DeadlineVersion:  version,
					NotificationType: domain.Notify3d,
					ScheduledAt:      t,
					Status:           domain.StatusPending,
					CreatedAt:        now,
				})
			}
		}

		if st.Remind1d {
			t := deadline.Add(-24 * time.Hour)
			if t.After(now) {
				jobs = append(jobs, domain.NotificationJob{
					UserID:           userID,
					LabID:            labID,
					DeadlineVersion:  version,
					NotificationType: domain.Notify1d,
					ScheduledAt:      t,
					Status:           domain.StatusPending,
					CreatedAt:        now,
				})
			}
		}

		if st.Remind3h {
			t := deadline.Add(-3 * time.Hour)
			if t.After(now) {
				jobs = append(jobs, domain.NotificationJob{
					UserID:           userID,
					LabID:            labID,
					DeadlineVersion:  version,
					NotificationType: domain.Notify3h,
					ScheduledAt:      t,
					Status:           domain.StatusPending,
					CreatedAt:        now,
				})
			}
		}

		// Overdue
		if deadline.After(now) {
			jobs = append(jobs, domain.NotificationJob{
				UserID:           userID,
				LabID:            labID,
				DeadlineVersion:  version,
				NotificationType: domain.NotifyOverdue,
				ScheduledAt:      deadline,
				Status:           domain.StatusPending,
				CreatedAt:        now,
			})
		}
	}

	if len(jobs) > 0 {
		_ = s.storage.UpsertNotificationJobs(ctx, jobs)
	}
}

func (s *SettingsService) scheduleSpecificReminder(ctx context.Context, userID int64, groupID int64, nType domain.NotificationType, offset time.Duration, now time.Time) {
	query := `
		SELECT l.id, l.deadline_at, l.deadline_version
		FROM labs l
		JOIN subjects s ON l.subject_id = s.id
		WHERE l.group_id = ? AND l.is_cancelled = 0 AND s.is_archived = 0
		  AND l.deadline_at >= ?
		  AND NOT EXISTS (SELECT 1 FROM student_submissions sub WHERE sub.user_id = ? AND sub.lab_id = l.id)
	`
	rows, err := s.storage.DB().QueryContext(ctx, query, groupID, now.Unix(), userID)
	if err != nil {
		return
	}
	defer rows.Close()

	var jobs []domain.NotificationJob
	for rows.Next() {
		var labID int64
		var deadlineAt int64
		var version int
		if err := rows.Scan(&labID, &deadlineAt, &version); err != nil {
			continue
		}
		deadline := time.Unix(deadlineAt, 0).UTC()
		scheduledAt := deadline.Add(offset)

		if scheduledAt.After(now) {
			jobs = append(jobs, domain.NotificationJob{
				UserID:           userID,
				LabID:            labID,
				DeadlineVersion:  version,
				NotificationType: nType,
				ScheduledAt:      scheduledAt,
				Status:           domain.StatusPending,
				CreatedAt:        now,
			})
		}
	}

	if len(jobs) > 0 {
		_ = s.storage.UpsertNotificationJobs(ctx, jobs)
	}
}
