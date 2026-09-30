package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"deadline_bot/internal/domain"
)

func (s *Storage) GetDefaultGroup(ctx context.Context) (*domain.Group, error) {
	row := s.db.QueryRowContext(ctx, "SELECT id, name, invite_code, created_at FROM groups ORDER BY id ASC LIMIT 1")
	var g domain.Group
	var createdAt int64
	err := row.Scan(&g.ID, &g.Name, &g.InviteCode, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query default group: %w", err)
	}
	g.CreatedAt = time.Unix(createdAt, 0).UTC()
	return &g, nil
}

func (s *Storage) CreateGroup(ctx context.Context, name, inviteCode string, now time.Time) (*domain.Group, error) {
	res, err := s.db.ExecContext(ctx, "INSERT INTO groups (name, invite_code, created_at) VALUES (?, ?, ?)",
		name, inviteCode, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("failed to create group: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("failed to get group last insert id: %w", err)
	}
	return &domain.Group{
		ID:         id,
		Name:       name,
		InviteCode: inviteCode,
		CreatedAt:  now.UTC(),
	}, nil
}

func (s *Storage) GetGroupByInviteCode(ctx context.Context, code string) (*domain.Group, error) {
	row := s.db.QueryRowContext(ctx, "SELECT id, name, invite_code, created_at FROM groups WHERE invite_code = ?", code)
	var g domain.Group
	var createdAt int64
	err := row.Scan(&g.ID, &g.Name, &g.InviteCode, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query group by invite code: %w", err)
	}
	g.CreatedAt = time.Unix(createdAt, 0).UTC()
	return &g, nil
}

func (s *Storage) GetGroupByID(ctx context.Context, id int64) (*domain.Group, error) {
	row := s.db.QueryRowContext(ctx, "SELECT id, name, invite_code, created_at FROM groups WHERE id = ?", id)
	var g domain.Group
	var createdAt int64
	err := row.Scan(&g.ID, &g.Name, &g.InviteCode, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query group by id: %w", err)
	}
	g.CreatedAt = time.Unix(createdAt, 0).UTC()
	return &g, nil
}

func (s *Storage) UpdateGroup(ctx context.Context, id int64, name, inviteCode string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE groups SET name = ?, invite_code = ? WHERE id = ?", name, inviteCode, id)
	if err != nil {
		return fmt.Errorf("failed to update group: %w", err)
	}
	return nil
}

func (s *Storage) UpdateInviteCode(ctx context.Context, groupID int64, newCode string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE groups SET invite_code = ? WHERE id = ?", newCode, groupID)
	if err != nil {
		return fmt.Errorf("failed to update group invite code: %w", err)
	}
	return nil
}
