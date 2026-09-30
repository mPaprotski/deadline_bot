package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"deadline_bot/internal/domain"
)

func (s *Storage) CreateSubject(ctx context.Context, groupID int64, name string, now time.Time) (*domain.Subject, error) {
	query := `
		INSERT INTO subjects (group_id, name, is_archived, created_at, updated_at)
		VALUES (?, ?, 0, ?, ?)
	`
	res, err := s.db.ExecContext(ctx, query, groupID, name, now.Unix(), now.Unix())
	if err != nil {
		return nil, fmt.Errorf("failed to create subject: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get subject last insert id: %w", err)
	}

	return &domain.Subject{
		ID:         id,
		GroupID:    groupID,
		Name:       name,
		IsArchived: false,
		CreatedAt:  now.UTC(),
		UpdatedAt:  now.UTC(),
	}, nil
}

func (s *Storage) RenameSubject(ctx context.Context, id int64, newName string, now time.Time) error {
	query := `UPDATE subjects SET name = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, newName, now.Unix(), id)
	if err != nil {
		return fmt.Errorf("failed to rename subject: %w", err)
	}
	return nil
}

func (s *Storage) ArchiveSubject(ctx context.Context, id int64, archive bool, now time.Time) error {
	archivedInt := 0
	if archive {
		archivedInt = 1
	}
	query := `UPDATE subjects SET is_archived = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, archivedInt, now.Unix(), id)
	if err != nil {
		return fmt.Errorf("failed to set subject archive status: %w", err)
	}
	return nil
}

func (s *Storage) GetSubjectByID(ctx context.Context, id int64) (*domain.Subject, error) {
	query := `SELECT id, group_id, name, is_archived, created_at, updated_at FROM subjects WHERE id = ?`
	row := s.db.QueryRowContext(ctx, query, id)
	var sub domain.Subject
	var isArchivedInt int
	var createdAt, updatedAt int64

	err := row.Scan(&sub.ID, &sub.GroupID, &sub.Name, &isArchivedInt, &createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query subject by id: %w", err)
	}
	sub.IsArchived = isArchivedInt == 1
	sub.CreatedAt = time.Unix(createdAt, 0).UTC()
	sub.UpdatedAt = time.Unix(updatedAt, 0).UTC()

	return &sub, nil
}

func (s *Storage) ListActiveSubjects(ctx context.Context, groupID int64) ([]domain.Subject, error) {
	query := `
		SELECT id, group_id, name, is_archived, created_at, updated_at
		FROM subjects
		WHERE group_id = ? AND is_archived = 0
		ORDER BY name COLLATE NOCASE ASC
	`
	rows, err := s.db.QueryContext(ctx, query, groupID)
	if err != nil {
		return nil, fmt.Errorf("failed to list active subjects: %w", err)
	}
	defer rows.Close()

	var subs []domain.Subject
	for rows.Next() {
		var sub domain.Subject
		var isArchivedInt int
		var createdAt, updatedAt int64

		if err := rows.Scan(&sub.ID, &sub.GroupID, &sub.Name, &isArchivedInt, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan subject: %w", err)
		}
		sub.IsArchived = isArchivedInt == 1
		sub.CreatedAt = time.Unix(createdAt, 0).UTC()
		sub.UpdatedAt = time.Unix(updatedAt, 0).UTC()

		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

func (s *Storage) ListAllSubjects(ctx context.Context, groupID int64) ([]domain.Subject, error) {
	query := `
		SELECT id, group_id, name, is_archived, created_at, updated_at
		FROM subjects
		WHERE group_id = ?
		ORDER BY is_archived ASC, name COLLATE NOCASE ASC
	`
	rows, err := s.db.QueryContext(ctx, query, groupID)
	if err != nil {
		return nil, fmt.Errorf("failed to list all subjects: %w", err)
	}
	defer rows.Close()

	var subs []domain.Subject
	for rows.Next() {
		var sub domain.Subject
		var isArchivedInt int
		var createdAt, updatedAt int64

		if err := rows.Scan(&sub.ID, &sub.GroupID, &sub.Name, &isArchivedInt, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan subject: %w", err)
		}
		sub.IsArchived = isArchivedInt == 1
		sub.CreatedAt = time.Unix(createdAt, 0).UTC()
		sub.UpdatedAt = time.Unix(updatedAt, 0).UTC()

		subs = append(subs, sub)
	}
	return subs, rows.Err()
}
