package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"deadline_bot/internal/domain"
)

func (s *Storage) GetUserByID(ctx context.Context, id int64) (*domain.User, error) {
	query := `
		SELECT id, group_id, username, first_name, last_name, role, status, is_blocked, created_at, updated_at
		FROM users
		WHERE id = ?
	`
	row := s.db.QueryRowContext(ctx, query, id)
	var u domain.User
	var groupID sql.NullInt64
	var roleStr, statusStr string
	var isBlockedInt int
	var createdAt, updatedAt int64

	err := row.Scan(&u.ID, &groupID, &u.Username, &u.FirstName, &u.LastName, &roleStr, &statusStr, &isBlockedInt, &createdAt, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query user by id: %w", err)
	}

	if groupID.Valid {
		gid := groupID.Int64
		u.GroupID = &gid
	}
	u.Role = domain.Role(roleStr)
	u.Status = domain.UserStatus(statusStr)
	u.IsBlocked = isBlockedInt == 1
	u.CreatedAt = time.Unix(createdAt, 0).UTC()
	u.UpdatedAt = time.Unix(updatedAt, 0).UTC()

	return &u, nil
}

func (s *Storage) UpsertUser(ctx context.Context, user *domain.User) error {
	query := `
		INSERT INTO users (id, group_id, username, first_name, last_name, role, status, is_blocked, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			username = excluded.username,
			first_name = excluded.first_name,
			last_name = excluded.last_name,
			updated_at = excluded.updated_at
	`
	var groupID any = nil
	if user.GroupID != nil {
		groupID = *user.GroupID
	}
	isBlockedInt := 0
	if user.IsBlocked {
		isBlockedInt = 1
	}

	_, err := s.db.ExecContext(ctx, query,
		user.ID,
		groupID,
		user.Username,
		user.FirstName,
		user.LastName,
		string(user.Role),
		string(user.Status),
		isBlockedInt,
		user.CreatedAt.Unix(),
		user.UpdatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("failed to upsert user: %w", err)
	}
	return nil
}

func (s *Storage) SetUserGroupAndRole(ctx context.Context, userID int64, groupID int64, role domain.Role, now time.Time) error {
	query := `
		UPDATE users
		SET group_id = ?, role = ?, status = 'active', updated_at = ?
		WHERE id = ?
	`
	_, err := s.db.ExecContext(ctx, query, groupID, string(role), now.Unix(), userID)
	if err != nil {
		return fmt.Errorf("failed to set user group and role: %w", err)
	}
	return nil
}

func (s *Storage) UpdateUserStatus(ctx context.Context, userID int64, status domain.UserStatus, now time.Time) error {
	query := `UPDATE users SET status = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, string(status), now.Unix(), userID)
	if err != nil {
		return fmt.Errorf("failed to update user status: %w", err)
	}
	return nil
}

func (s *Storage) UpdateUserRole(ctx context.Context, userID int64, role domain.Role, now time.Time) error {
	query := `UPDATE users SET role = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, string(role), now.Unix(), userID)
	if err != nil {
		return fmt.Errorf("failed to update user role: %w", err)
	}
	return nil
}

func (s *Storage) SetUserBlocked(ctx context.Context, userID int64, blocked bool, now time.Time) error {
	bInt := 0
	if blocked {
		bInt = 1
	}
	query := `UPDATE users SET is_blocked = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, bInt, now.Unix(), userID)
	if err != nil {
		return fmt.Errorf("failed to update user blocked status: %w", err)
	}
	return nil
}

func (s *Storage) ListGroupUsers(ctx context.Context, groupID int64) ([]domain.User, error) {
	query := `
		SELECT id, group_id, username, first_name, last_name, role, status, is_blocked, created_at, updated_at
		FROM users
		WHERE group_id = ?
		ORDER BY role DESC, first_name ASC
	`
	rows, err := s.db.QueryContext(ctx, query, groupID)
	if err != nil {
		return nil, fmt.Errorf("failed to list group users: %w", err)
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		var u domain.User
		var gID sql.NullInt64
		var roleStr, statusStr string
		var isBlockedInt int
		var createdAt, updatedAt int64

		if err := rows.Scan(&u.ID, &gID, &u.Username, &u.FirstName, &u.LastName, &roleStr, &statusStr, &isBlockedInt, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan user: %w", err)
		}
		if gID.Valid {
			gid := gID.Int64
			u.GroupID = &gid
		}
		u.Role = domain.Role(roleStr)
		u.Status = domain.UserStatus(statusStr)
		u.IsBlocked = isBlockedInt == 1
		u.CreatedAt = time.Unix(createdAt, 0).UTC()
		u.UpdatedAt = time.Unix(updatedAt, 0).UTC()

		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Storage) ListActiveGroupStudents(ctx context.Context, groupID int64) ([]domain.User, error) {
	query := `
		SELECT id, group_id, username, first_name, last_name, role, status, is_blocked, created_at, updated_at
		FROM users
		WHERE group_id = ? AND status = 'active'
		ORDER BY id ASC
	`
	rows, err := s.db.QueryContext(ctx, query, groupID)
	if err != nil {
		return nil, fmt.Errorf("failed to list active group students: %w", err)
	}
	defer rows.Close()

	var users []domain.User
	for rows.Next() {
		var u domain.User
		var gID sql.NullInt64
		var roleStr, statusStr string
		var isBlockedInt int
		var createdAt, updatedAt int64

		if err := rows.Scan(&u.ID, &gID, &u.Username, &u.FirstName, &u.LastName, &roleStr, &statusStr, &isBlockedInt, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan active student: %w", err)
		}
		if gID.Valid {
			gid := gID.Int64
			u.GroupID = &gid
		}
		u.Role = domain.Role(roleStr)
		u.Status = domain.UserStatus(statusStr)
		u.IsBlocked = isBlockedInt == 1
		u.CreatedAt = time.Unix(createdAt, 0).UTC()
		u.UpdatedAt = time.Unix(updatedAt, 0).UTC()

		users = append(users, u)
	}
	return users, rows.Err()
}

// User Settings

func (s *Storage) GetUserSettings(ctx context.Context, userID int64) (*domain.UserSettings, error) {
	query := `
		SELECT user_id, remind_all, remind_3d, remind_1d, remind_3h, notify_new_lab, notify_changes, updated_at
		FROM user_settings
		WHERE user_id = ?
	`
	row := s.db.QueryRowContext(ctx, query, userID)
	var st domain.UserSettings
	var rAll, r3d, r1d, r3h, nNew, nChg int
	var updatedAt int64

	err := row.Scan(&st.UserID, &rAll, &r3d, &r1d, &r3h, &nNew, &nChg, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query user settings: %w", err)
	}

	st.RemindAll = rAll == 1
	st.Remind3d = r3d == 1
	st.Remind1d = r1d == 1
	st.Remind3h = r3h == 1
	st.NotifyNewLab = nNew == 1
	st.NotifyChanges = nChg == 1
	st.UpdatedAt = time.Unix(updatedAt, 0).UTC()

	return &st, nil
}

func (s *Storage) UpsertUserSettings(ctx context.Context, settings *domain.UserSettings) error {
	query := `
		INSERT INTO user_settings (user_id, remind_all, remind_3d, remind_1d, remind_3h, notify_new_lab, notify_changes, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			remind_all = excluded.remind_all,
			remind_3d = excluded.remind_3d,
			remind_1d = excluded.remind_1d,
			remind_3h = excluded.remind_3h,
			notify_new_lab = excluded.notify_new_lab,
			notify_changes = excluded.notify_changes,
			updated_at = excluded.updated_at
	`
	toInt := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	_, err := s.db.ExecContext(ctx, query,
		settings.UserID,
		toInt(settings.RemindAll),
		toInt(settings.Remind3d),
		toInt(settings.Remind1d),
		toInt(settings.Remind3h),
		toInt(settings.NotifyNewLab),
		toInt(settings.NotifyChanges),
		settings.UpdatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("failed to upsert user settings: %w", err)
	}
	return nil
}

// Admin Audit Logs

func (s *Storage) LogAdminAction(ctx context.Context, userID int64, action, details string, now time.Time) error {
	query := `INSERT INTO admin_audit_logs (user_id, action, details, created_at) VALUES (?, ?, ?, ?)`
	_, err := s.db.ExecContext(ctx, query, userID, action, details, now.Unix())
	if err != nil {
		return fmt.Errorf("failed to insert admin audit log: %w", err)
	}
	return nil
}
