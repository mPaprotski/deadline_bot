package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"deadline_bot/internal/domain"
)

func (s *Storage) CreateLab(ctx context.Context, lab *domain.Lab) (*domain.Lab, error) {
	query := `
		INSERT INTO labs (
			group_id, subject_id, number, title, description, submission_url, file_id,
			submission_method, teacher_comment, deadline_at, deadline_version, is_cancelled,
			created_by, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	cancelledInt := 0
	if lab.IsCancelled {
		cancelledInt = 1
	}
	if lab.DeadlineVersion <= 0 {
		lab.DeadlineVersion = 1
	}

	res, err := s.db.ExecContext(ctx, query,
		lab.GroupID,
		lab.SubjectID,
		lab.Number,
		lab.Title,
		lab.Description,
		lab.SubmissionURL,
		lab.FileID,
		lab.SubmissionMethod,
		lab.TeacherComment,
		lab.DeadlineAt.Unix(),
		lab.DeadlineVersion,
		cancelledInt,
		lab.CreatedBy,
		lab.CreatedAt.Unix(),
		lab.UpdatedAt.Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert lab: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get lab last insert id: %w", err)
	}

	labCopy := *lab
	labCopy.ID = id
	return &labCopy, nil
}

func (s *Storage) UpdateLab(ctx context.Context, lab *domain.Lab) error {
	query := `
		UPDATE labs SET
			subject_id = ?,
			number = ?,
			title = ?,
			description = ?,
			submission_url = ?,
			file_id = ?,
			submission_method = ?,
			teacher_comment = ?,
			updated_at = ?
		WHERE id = ?
	`
	_, err := s.db.ExecContext(ctx, query,
		lab.SubjectID,
		lab.Number,
		lab.Title,
		lab.Description,
		lab.SubmissionURL,
		lab.FileID,
		lab.SubmissionMethod,
		lab.TeacherComment,
		lab.UpdatedAt.Unix(),
		lab.ID,
	)
	if err != nil {
		return fmt.Errorf("failed to update lab: %w", err)
	}
	return nil
}

func (s *Storage) RescheduleLabDeadline(ctx context.Context, labID int64, newDeadline time.Time, changedBy int64, now time.Time) (time.Time, int, error) {
	var oldDeadlineAt int64
	var oldVersion int

	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, "SELECT deadline_at, deadline_version FROM labs WHERE id = ?", labID)
		if err := row.Scan(&oldDeadlineAt, &oldVersion); err != nil {
			return fmt.Errorf("failed to query existing lab deadline: %w", err)
		}

		newVersion := oldVersion + 1
		updateQuery := `UPDATE labs SET deadline_at = ?, deadline_version = ?, updated_at = ? WHERE id = ?`
		if _, err := tx.ExecContext(ctx, updateQuery, newDeadline.Unix(), newVersion, now.Unix(), labID); err != nil {
			return fmt.Errorf("failed to update lab deadline: %w", err)
		}

		historyQuery := `
			INSERT INTO lab_deadline_history (
				lab_id, old_deadline_at, new_deadline_at, version_from, version_to, changed_by, changed_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
		`
		if _, err := tx.ExecContext(ctx, historyQuery, labID, oldDeadlineAt, newDeadline.Unix(), oldVersion, newVersion, changedBy, now.Unix()); err != nil {
			return fmt.Errorf("failed to record lab deadline history: %w", err)
		}

		return nil
	})
	if err != nil {
		return time.Time{}, 0, err
	}

	return time.Unix(oldDeadlineAt, 0).UTC(), oldVersion + 1, nil
}

func (s *Storage) CancelLab(ctx context.Context, labID int64, changedBy int64, now time.Time) error {
	query := `UPDATE labs SET is_cancelled = 1, updated_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, now.Unix(), labID)
	if err != nil {
		return fmt.Errorf("failed to cancel lab: %w", err)
	}
	return nil
}

func (s *Storage) GetLabByID(ctx context.Context, labID int64, userID int64) (*domain.Lab, error) {
	query := `
		SELECT
			l.id, l.group_id, l.subject_id, s.name, l.number, l.title, l.description,
			l.submission_url, l.file_id, l.submission_method, l.teacher_comment,
			l.deadline_at, l.deadline_version, l.is_cancelled, l.created_by,
			l.created_at, l.updated_at,
			(sub.marked_at IS NOT NULL) AS is_completed,
			sub.marked_at
		FROM labs l
		JOIN subjects s ON l.subject_id = s.id
		LEFT JOIN student_submissions sub ON sub.lab_id = l.id AND sub.user_id = ?
		WHERE l.id = ?
	`
	row := s.db.QueryRowContext(ctx, query, userID, labID)

	var lab domain.Lab
	var cancelledInt int
	var isCompletedInt int
	var markedAt sql.NullInt64
	var deadlineAt, createdAt, updatedAt int64

	err := row.Scan(
		&lab.ID, &lab.GroupID, &lab.SubjectID, &lab.SubjectName, &lab.Number, &lab.Title, &lab.Description,
		&lab.SubmissionURL, &lab.FileID, &lab.SubmissionMethod, &lab.TeacherComment,
		&deadlineAt, &lab.DeadlineVersion, &cancelledInt, &lab.CreatedBy,
		&createdAt, &updatedAt,
		&isCompletedInt, &markedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query lab by id: %w", err)
	}

	lab.DeadlineAt = time.Unix(deadlineAt, 0).UTC()
	lab.CreatedAt = time.Unix(createdAt, 0).UTC()
	lab.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	lab.IsCancelled = cancelledInt == 1
	lab.IsCompleted = isCompletedInt == 1
	if markedAt.Valid {
		t := time.Unix(markedAt.Int64, 0).UTC()
		lab.CompletedAt = &t
	}

	return &lab, nil
}

func (s *Storage) ListUpcomingDeadlines(ctx context.Context, groupID int64, userID int64, now time.Time, limit, offset int) ([]domain.Lab, int, error) {
	countQuery := `
		SELECT COUNT(*)
		FROM labs l
		JOIN subjects s ON l.subject_id = s.id
		WHERE l.group_id = ?
		  AND l.is_cancelled = 0
		  AND s.is_archived = 0
		  AND l.deadline_at >= ?
		  AND NOT EXISTS (
			  SELECT 1 FROM student_submissions sub WHERE sub.user_id = ? AND sub.lab_id = l.id
		  )
	`
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, groupID, now.Unix(), userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count upcoming deadlines: %w", err)
	}

	query := `
		SELECT
			l.id, l.group_id, l.subject_id, s.name, l.number, l.title, l.description,
			l.submission_url, l.file_id, l.submission_method, l.teacher_comment,
			l.deadline_at, l.deadline_version, l.is_cancelled, l.created_by,
			l.created_at, l.updated_at
		FROM labs l
		JOIN subjects s ON l.subject_id = s.id
		WHERE l.group_id = ?
		  AND l.is_cancelled = 0
		  AND s.is_archived = 0
		  AND l.deadline_at >= ?
		  AND NOT EXISTS (
			  SELECT 1 FROM student_submissions sub WHERE sub.user_id = ? AND sub.lab_id = l.id
		  )
		ORDER BY l.deadline_at ASC
		LIMIT ? OFFSET ?
	`
	rows, err := s.db.QueryContext(ctx, query, groupID, now.Unix(), userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query upcoming deadlines: %w", err)
	}
	defer rows.Close()

	labs, err := scanLabs(rows)
	if err != nil {
		return nil, 0, err
	}
	return labs, total, nil
}

func (s *Storage) ListWeekDeadlines(ctx context.Context, groupID int64, userID int64, startWeek, endWeek time.Time, limit, offset int) ([]domain.Lab, int, error) {
	countQuery := `
		SELECT COUNT(*)
		FROM labs l
		JOIN subjects s ON l.subject_id = s.id
		WHERE l.group_id = ?
		  AND l.is_cancelled = 0
		  AND s.is_archived = 0
		  AND l.deadline_at >= ?
		  AND l.deadline_at <= ?
	`
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, groupID, startWeek.Unix(), endWeek.Unix()).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count week deadlines: %w", err)
	}

	query := `
		SELECT
			l.id, l.group_id, l.subject_id, s.name, l.number, l.title, l.description,
			l.submission_url, l.file_id, l.submission_method, l.teacher_comment,
			l.deadline_at, l.deadline_version, l.is_cancelled, l.created_by,
			l.created_at, l.updated_at,
			(sub.marked_at IS NOT NULL) AS is_completed,
			sub.marked_at
		FROM labs l
		JOIN subjects s ON l.subject_id = s.id
		LEFT JOIN student_submissions sub ON sub.lab_id = l.id AND sub.user_id = ?
		WHERE l.group_id = ?
		  AND l.is_cancelled = 0
		  AND s.is_archived = 0
		  AND l.deadline_at >= ?
		  AND l.deadline_at <= ?
		ORDER BY l.deadline_at ASC
		LIMIT ? OFFSET ?
	`
	rows, err := s.db.QueryContext(ctx, query, userID, groupID, startWeek.Unix(), endWeek.Unix(), limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query week deadlines: %w", err)
	}
	defer rows.Close()

	labs, err := scanLabsWithSubmissions(rows)
	if err != nil {
		return nil, 0, err
	}
	return labs, total, nil
}

func (s *Storage) ListOverdueDeadlines(ctx context.Context, groupID int64, userID int64, now time.Time, limit, offset int) ([]domain.Lab, int, error) {
	countQuery := `
		SELECT COUNT(*)
		FROM labs l
		JOIN subjects s ON l.subject_id = s.id
		WHERE l.group_id = ?
		  AND l.is_cancelled = 0
		  AND s.is_archived = 0
		  AND l.deadline_at < ?
		  AND NOT EXISTS (
			  SELECT 1 FROM student_submissions sub WHERE sub.user_id = ? AND sub.lab_id = l.id
		  )
	`
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, groupID, now.Unix(), userID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count overdue deadlines: %w", err)
	}

	query := `
		SELECT
			l.id, l.group_id, l.subject_id, s.name, l.number, l.title, l.description,
			l.submission_url, l.file_id, l.submission_method, l.teacher_comment,
			l.deadline_at, l.deadline_version, l.is_cancelled, l.created_by,
			l.created_at, l.updated_at
		FROM labs l
		JOIN subjects s ON l.subject_id = s.id
		WHERE l.group_id = ?
		  AND l.is_cancelled = 0
		  AND s.is_archived = 0
		  AND l.deadline_at < ?
		  AND NOT EXISTS (
			  SELECT 1 FROM student_submissions sub WHERE sub.user_id = ? AND sub.lab_id = l.id
		  )
		ORDER BY l.deadline_at ASC
		LIMIT ? OFFSET ?
	`
	rows, err := s.db.QueryContext(ctx, query, groupID, now.Unix(), userID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query overdue deadlines: %w", err)
	}
	defer rows.Close()

	labs, err := scanLabs(rows)
	if err != nil {
		return nil, 0, err
	}
	return labs, total, nil
}

func (s *Storage) ListCompletedDeadlines(ctx context.Context, groupID int64, userID int64, limit, offset int) ([]domain.Lab, int, error) {
	countQuery := `
		SELECT COUNT(*)
		FROM labs l
		JOIN student_submissions sub ON sub.lab_id = l.id AND sub.user_id = ?
		JOIN subjects s ON l.subject_id = s.id
		WHERE l.group_id = ?
	`
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, userID, groupID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count completed deadlines: %w", err)
	}

	query := `
		SELECT
			l.id, l.group_id, l.subject_id, s.name, l.number, l.title, l.description,
			l.submission_url, l.file_id, l.submission_method, l.teacher_comment,
			l.deadline_at, l.deadline_version, l.is_cancelled, l.created_by,
			l.created_at, l.updated_at,
			1 AS is_completed,
			sub.marked_at
		FROM labs l
		JOIN subjects s ON l.subject_id = s.id
		JOIN student_submissions sub ON sub.lab_id = l.id AND sub.user_id = ?
		WHERE l.group_id = ?
		ORDER BY l.deadline_at DESC
		LIMIT ? OFFSET ?
	`
	rows, err := s.db.QueryContext(ctx, query, userID, groupID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query completed deadlines: %w", err)
	}
	defer rows.Close()

	labs, err := scanLabsWithSubmissions(rows)
	if err != nil {
		return nil, 0, err
	}
	return labs, total, nil
}

func (s *Storage) ListLabsBySubject(ctx context.Context, subjectID int64, userID int64, limit, offset int) ([]domain.Lab, int, error) {
	countQuery := `SELECT COUNT(*) FROM labs WHERE subject_id = ? AND is_cancelled = 0`
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, subjectID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count subject labs: %w", err)
	}

	query := `
		SELECT
			l.id, l.group_id, l.subject_id, s.name, l.number, l.title, l.description,
			l.submission_url, l.file_id, l.submission_method, l.teacher_comment,
			l.deadline_at, l.deadline_version, l.is_cancelled, l.created_by,
			l.created_at, l.updated_at,
			(sub.marked_at IS NOT NULL) AS is_completed,
			sub.marked_at
		FROM labs l
		JOIN subjects s ON l.subject_id = s.id
		LEFT JOIN student_submissions sub ON sub.lab_id = l.id AND sub.user_id = ?
		WHERE l.subject_id = ? AND l.is_cancelled = 0
		ORDER BY l.deadline_at ASC
		LIMIT ? OFFSET ?
	`
	rows, err := s.db.QueryContext(ctx, query, userID, subjectID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query subject labs: %w", err)
	}
	defer rows.Close()

	labs, err := scanLabsWithSubmissions(rows)
	if err != nil {
		return nil, 0, err
	}
	return labs, total, nil
}

func (s *Storage) ListAllActiveLabsForAdmin(ctx context.Context, groupID int64, limit, offset int) ([]domain.Lab, int, error) {
	countQuery := `
		SELECT COUNT(*)
		FROM labs l
		JOIN subjects s ON l.subject_id = s.id
		WHERE l.group_id = ? AND l.is_cancelled = 0
	`
	var total int
	if err := s.db.QueryRowContext(ctx, countQuery, groupID).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("failed to count admin labs: %w", err)
	}

	query := `
		SELECT
			l.id, l.group_id, l.subject_id, s.name, l.number, l.title, l.description,
			l.submission_url, l.file_id, l.submission_method, l.teacher_comment,
			l.deadline_at, l.deadline_version, l.is_cancelled, l.created_by,
			l.created_at, l.updated_at
		FROM labs l
		JOIN subjects s ON l.subject_id = s.id
		WHERE l.group_id = ? AND l.is_cancelled = 0
		ORDER BY l.deadline_at ASC
		LIMIT ? OFFSET ?
	`
	rows, err := s.db.QueryContext(ctx, query, groupID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query admin labs: %w", err)
	}
	defer rows.Close()

	labs, err := scanLabs(rows)
	if err != nil {
		return nil, 0, err
	}
	return labs, total, nil
}

// Submissions

func (s *Storage) MarkLabCompleted(ctx context.Context, userID int64, labID int64, now time.Time) error {
	query := `
		INSERT INTO student_submissions (user_id, lab_id, marked_at)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id, lab_id) DO UPDATE SET marked_at = excluded.marked_at
	`
	_, err := s.db.ExecContext(ctx, query, userID, labID, now.Unix())
	if err != nil {
		return fmt.Errorf("failed to mark lab completed: %w", err)
	}
	return nil
}

func (s *Storage) UnmarkLabCompleted(ctx context.Context, userID int64, labID int64) error {
	query := `DELETE FROM student_submissions WHERE user_id = ? AND lab_id = ?`
	_, err := s.db.ExecContext(ctx, query, userID, labID)
	if err != nil {
		return fmt.Errorf("failed to unmark lab completed: %w", err)
	}
	return nil
}

func (s *Storage) IsLabCompleted(ctx context.Context, userID int64, labID int64) (bool, error) {
	query := `SELECT COUNT(*) FROM student_submissions WHERE user_id = ? AND lab_id = ?`
	var count int
	if err := s.db.QueryRowContext(ctx, query, userID, labID).Scan(&count); err != nil {
		return false, fmt.Errorf("failed to check lab completed: %w", err)
	}
	return count > 0, nil
}

// Helpers

func scanLabs(rows *sql.Rows) ([]domain.Lab, error) {
	var labs []domain.Lab
	for rows.Next() {
		var lab domain.Lab
		var cancelledInt int
		var deadlineAt, createdAt, updatedAt int64

		err := rows.Scan(
			&lab.ID, &lab.GroupID, &lab.SubjectID, &lab.SubjectName, &lab.Number, &lab.Title, &lab.Description,
			&lab.SubmissionURL, &lab.FileID, &lab.SubmissionMethod, &lab.TeacherComment,
			&deadlineAt, &lab.DeadlineVersion, &cancelledInt, &lab.CreatedBy,
			&createdAt, &updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan lab: %w", err)
		}
		lab.DeadlineAt = time.Unix(deadlineAt, 0).UTC()
		lab.CreatedAt = time.Unix(createdAt, 0).UTC()
		lab.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		lab.IsCancelled = cancelledInt == 1
		labs = append(labs, lab)
	}
	return labs, rows.Err()
}

func scanLabsWithSubmissions(rows *sql.Rows) ([]domain.Lab, error) {
	var labs []domain.Lab
	for rows.Next() {
		var lab domain.Lab
		var cancelledInt int
		var isCompletedInt int
		var markedAt sql.NullInt64
		var deadlineAt, createdAt, updatedAt int64

		err := rows.Scan(
			&lab.ID, &lab.GroupID, &lab.SubjectID, &lab.SubjectName, &lab.Number, &lab.Title, &lab.Description,
			&lab.SubmissionURL, &lab.FileID, &lab.SubmissionMethod, &lab.TeacherComment,
			&deadlineAt, &lab.DeadlineVersion, &cancelledInt, &lab.CreatedBy,
			&createdAt, &updatedAt,
			&isCompletedInt, &markedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan lab with submission: %w", err)
		}
		lab.DeadlineAt = time.Unix(deadlineAt, 0).UTC()
		lab.CreatedAt = time.Unix(createdAt, 0).UTC()
		lab.UpdatedAt = time.Unix(updatedAt, 0).UTC()
		lab.IsCancelled = cancelledInt == 1
		lab.IsCompleted = isCompletedInt == 1
		if markedAt.Valid {
			t := time.Unix(markedAt.Int64, 0).UTC()
			lab.CompletedAt = &t
		}
		labs = append(labs, lab)
	}
	return labs, rows.Err()
}
