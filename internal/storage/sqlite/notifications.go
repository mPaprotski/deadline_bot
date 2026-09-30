package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"deadline_bot/internal/domain"
)

func (s *Storage) CreateNotificationJobs(ctx context.Context, jobs []domain.NotificationJob) error {
	if len(jobs) == 0 {
		return nil
	}

	query := `
		INSERT INTO notification_jobs (
			user_id, lab_id, deadline_version, notification_type,
			scheduled_at, status, retry_count, last_error, created_at
		) VALUES (?, ?, ?, ?, ?, 'pending', 0, '', ?)
		ON CONFLICT(user_id, lab_id, deadline_version, notification_type) DO NOTHING
	`

	return s.WithTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, query)
		if err != nil {
			return fmt.Errorf("failed to prepare notification insert: %w", err)
		}
		defer stmt.Close()

		for _, job := range jobs {
			_, err := stmt.ExecContext(ctx,
				job.UserID,
				job.LabID,
				job.DeadlineVersion,
				string(job.NotificationType),
				job.ScheduledAt.Unix(),
				job.CreatedAt.Unix(),
			)
			if err != nil {
				return fmt.Errorf("failed to exec notification job insert: %w", err)
			}
		}
		return nil
	})
}

// UpsertNotificationJobs inserts new jobs or restores cancelled ones to pending.
// Used when restoring reminders (after UnmarkCompleted or re-enabling settings).
// It does NOT override already pending/sent/processing jobs.
func (s *Storage) UpsertNotificationJobs(ctx context.Context, jobs []domain.NotificationJob) error {
	if len(jobs) == 0 {
		return nil
	}

	query := `
		INSERT INTO notification_jobs (
			user_id, lab_id, deadline_version, notification_type,
			scheduled_at, status, retry_count, last_error, created_at
		) VALUES (?, ?, ?, ?, ?, 'pending', 0, '', ?)
		ON CONFLICT(user_id, lab_id, deadline_version, notification_type) DO UPDATE SET
			status = CASE WHEN excluded.status = 'pending' AND status IN ('cancelled', 'failed') THEN 'pending' ELSE status END,
			scheduled_at = CASE WHEN status IN ('cancelled', 'failed') THEN excluded.scheduled_at ELSE scheduled_at END,
			retry_count = CASE WHEN status IN ('cancelled', 'failed') THEN 0 ELSE retry_count END,
			last_error = CASE WHEN status IN ('cancelled', 'failed') THEN '' ELSE last_error END
	`

	return s.WithTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, query)
		if err != nil {
			return fmt.Errorf("failed to prepare notification upsert: %w", err)
		}
		defer stmt.Close()

		for _, job := range jobs {
			_, err := stmt.ExecContext(ctx,
				job.UserID,
				job.LabID,
				job.DeadlineVersion,
				string(job.NotificationType),
				job.ScheduledAt.Unix(),
				job.CreatedAt.Unix(),
			)
			if err != nil {
				return fmt.Errorf("failed to exec notification job upsert: %w", err)
			}
		}
		return nil
	})
}

func (s *Storage) CancelPendingJobsForLab(ctx context.Context, labID int64) error {
	query := `UPDATE notification_jobs SET status = 'cancelled' WHERE lab_id = ? AND status = 'pending'`
	_, err := s.db.ExecContext(ctx, query, labID)
	if err != nil {
		return fmt.Errorf("failed to cancel pending jobs for lab: %w", err)
	}
	return nil
}

func (s *Storage) CancelPendingJobsForLabAndUser(ctx context.Context, labID int64, userID int64) error {
	query := `
		UPDATE notification_jobs
		SET status = 'cancelled'
		WHERE lab_id = ? AND user_id = ? AND status = 'pending'
	`
	_, err := s.db.ExecContext(ctx, query, labID, userID)
	if err != nil {
		return fmt.Errorf("failed to cancel pending jobs for user and lab: %w", err)
	}
	return nil
}

func (s *Storage) CancelPendingOldVersionJobs(ctx context.Context, labID int64, currentVersion int) error {
	query := `
		UPDATE notification_jobs
		SET status = 'cancelled'
		WHERE lab_id = ? AND deadline_version < ? AND status = 'pending'
	`
	_, err := s.db.ExecContext(ctx, query, labID, currentVersion)
	if err != nil {
		return fmt.Errorf("failed to cancel old version pending jobs: %w", err)
	}
	return nil
}

func (s *Storage) RecoverInterruptedProcessingJobs(ctx context.Context) (int64, error) {
	query := `UPDATE notification_jobs SET status = 'pending' WHERE status = 'processing'`
	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("failed to recover interrupted processing jobs: %w", err)
	}
	return res.RowsAffected()
}

func (s *Storage) GetDuePendingJobs(ctx context.Context, now time.Time, limit int) ([]domain.NotificationJob, error) {
	query := `
		SELECT
			id, user_id, lab_id, deadline_version, notification_type,
			scheduled_at, status, retry_count, last_error, sent_at, created_at
		FROM notification_jobs
		WHERE status = 'pending' AND scheduled_at <= ?
		ORDER BY scheduled_at ASC, id ASC
		LIMIT ?
	`
	rows, err := s.db.QueryContext(ctx, query, now.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query due pending jobs: %w", err)
	}
	defer rows.Close()

	var jobs []domain.NotificationJob
	for rows.Next() {
		var j domain.NotificationJob
		var nType, statusStr string
		var scheduledAt, createdAt int64
		var sentAt sql.NullInt64

		err := rows.Scan(
			&j.ID, &j.UserID, &j.LabID, &j.DeadlineVersion, &nType,
			&scheduledAt, &statusStr, &j.RetryCount, &j.LastError,
			&sentAt, &createdAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan notification job: %w", err)
		}
		j.NotificationType = domain.NotificationType(nType)
		j.Status = domain.NotificationStatus(statusStr)
		j.ScheduledAt = time.Unix(scheduledAt, 0).UTC()
		j.CreatedAt = time.Unix(createdAt, 0).UTC()
		if sentAt.Valid {
			t := time.Unix(sentAt.Int64, 0).UTC()
			j.SentAt = &t
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *Storage) MarkJobProcessing(ctx context.Context, id int64) (bool, error) {
	query := `UPDATE notification_jobs SET status = 'processing' WHERE id = ? AND status = 'pending'`
	res, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return false, fmt.Errorf("failed to mark job processing: %w", err)
	}
	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

func (s *Storage) MarkJobSent(ctx context.Context, id int64, sentAt time.Time) error {
	query := `UPDATE notification_jobs SET status = 'sent', sent_at = ?, last_error = '' WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, sentAt.Unix(), id)
	if err != nil {
		return fmt.Errorf("failed to mark job sent: %w", err)
	}
	return nil
}

func (s *Storage) MarkJobFailed(ctx context.Context, id int64, errStr string, retryCount int) error {
	query := `UPDATE notification_jobs SET status = 'failed', last_error = ?, retry_count = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, errStr, retryCount, id)
	if err != nil {
		return fmt.Errorf("failed to mark job failed: %w", err)
	}
	return nil
}

func (s *Storage) MarkJobRetryPending(ctx context.Context, id int64, errStr string, retryCount int, nextSchedule time.Time) error {
	query := `UPDATE notification_jobs SET status = 'pending', last_error = ?, retry_count = ?, scheduled_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, errStr, retryCount, nextSchedule.Unix(), id)
	if err != nil {
		return fmt.Errorf("failed to mark job retry pending: %w", err)
	}
	return nil
}

func (s *Storage) MarkJobCancelled(ctx context.Context, id int64) error {
	query := `UPDATE notification_jobs SET status = 'cancelled' WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to mark job cancelled: %w", err)
	}
	return nil
}

// CancelSupersededDowntimeJobs cancels all other pending reminder jobs for the same user and lab when one is selected.
func (s *Storage) CancelSupersededDowntimeJobs(ctx context.Context, keptJobID int64, userID int64, labID int64) error {
	query := `
		UPDATE notification_jobs
		SET status = 'cancelled', last_error = 'superseded after downtime'
		WHERE user_id = ? AND lab_id = ? AND id != ? AND status = 'pending'
		  AND notification_type IN ('3d', '1d', '3h', 'overdue')
	`
	_, err := s.db.ExecContext(ctx, query, userID, labID, keptJobID)
	if err != nil {
		return fmt.Errorf("failed to cancel superseded downtime jobs: %w", err)
	}
	return nil
}

func (s *Storage) GetJobByID(ctx context.Context, id int64) (*domain.NotificationJob, error) {
	query := `
		SELECT
			id, user_id, lab_id, deadline_version, notification_type,
			scheduled_at, status, retry_count, last_error, sent_at, created_at
		FROM notification_jobs
		WHERE id = ?
	`
	row := s.db.QueryRowContext(ctx, query, id)
	var j domain.NotificationJob
	var nType, statusStr string
	var scheduledAt, createdAt int64
	var sentAt sql.NullInt64

	err := row.Scan(
		&j.ID, &j.UserID, &j.LabID, &j.DeadlineVersion, &nType,
		&scheduledAt, &statusStr, &j.RetryCount, &j.LastError,
		&sentAt, &createdAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to scan job: %w", err)
	}
	j.NotificationType = domain.NotificationType(nType)
	j.Status = domain.NotificationStatus(statusStr)
	j.ScheduledAt = time.Unix(scheduledAt, 0).UTC()
	j.CreatedAt = time.Unix(createdAt, 0).UTC()
	if sentAt.Valid {
		t := time.Unix(sentAt.Int64, 0).UTC()
		j.SentAt = &t
	}
	return &j, nil
}
