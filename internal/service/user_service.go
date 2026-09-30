package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"deadline_bot/internal/domain"
	"deadline_bot/internal/storage/sqlite"
)

var (
	ErrAccessDenied  = errors.New("доступ запрещен")
	ErrGroupNotFound = errors.New("группа не найдена")
	ErrInvalidInvite = errors.New("неверный код приглашения")
	ErrUserRevoked   = errors.New("доступ к группе отозван")
	ErrUserNotFound  = errors.New("пользователь не найден")
)

type UserService struct {
	storage *sqlite.Storage
	clock   Clock
	ownerID int64
}

func NewUserService(storage *sqlite.Storage, clock Clock, ownerID int64) *UserService {
	if clock == nil {
		clock = RealClock{}
	}
	return &UserService{
		storage: storage,
		clock:   clock,
		ownerID: ownerID,
	}
}

func (s *UserService) OwnerID() int64 {
	return s.ownerID
}

// EnsureUser loads or creates a user from Telegram info, ensuring correct owner role and settings.
func (s *UserService) EnsureUser(ctx context.Context, tgID int64, username, firstName, lastName string) (*domain.User, error) {
	now := s.clock.Now()
	u, err := s.storage.GetUserByID(ctx, tgID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	role := domain.RoleStudent
	if tgID == s.ownerID {
		role = domain.RoleOwner
	}

	if u == nil {
		u = &domain.User{
			ID:        tgID,
			GroupID:   nil,
			Username:  username,
			FirstName: firstName,
			LastName:  lastName,
			Role:      role,
			Status:    domain.UserStatusActive,
			IsBlocked: false,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := s.storage.UpsertUser(ctx, u); err != nil {
			return nil, fmt.Errorf("failed to create user: %w", err)
		}
		// Create default user settings
		settings := domain.DefaultUserSettings(tgID, now)
		if err := s.storage.UpsertUserSettings(ctx, &settings); err != nil {
			return nil, fmt.Errorf("failed to create user settings: %w", err)
		}
	} else {
		// Update user info and ensure Owner role
		u.Username = username
		u.FirstName = firstName
		u.LastName = lastName
		u.UpdatedAt = now
		if tgID == s.ownerID && u.Role != domain.RoleOwner {
			u.Role = domain.RoleOwner
		}
		if u.IsBlocked {
			u.IsBlocked = false
			_ = s.storage.SetUserBlocked(ctx, tgID, false, now)
		}
		if err := s.storage.UpsertUser(ctx, u); err != nil {
			return nil, fmt.Errorf("failed to update user info: %w", err)
		}

		// Ensure settings exist
		settings, err := s.storage.GetUserSettings(ctx, tgID)
		if err != nil {
			return nil, err
		}
		if settings == nil {
			defSettings := domain.DefaultUserSettings(tgID, now)
			_ = s.storage.UpsertUserSettings(ctx, &defSettings)
		}
	}

	return u, nil
}

// EnsureDefaultGroup ensures that an initial group exists and links the owner if not linked yet.
func (s *UserService) EnsureDefaultGroup(ctx context.Context, defaultName, defaultInviteCode string) (*domain.Group, error) {
	now := s.clock.Now()
	grp, err := s.storage.GetDefaultGroup(ctx)
	if err != nil {
		return nil, err
	}
	if grp == nil {
		grp, err = s.storage.CreateGroup(ctx, defaultName, defaultInviteCode, now)
		if err != nil {
			return nil, fmt.Errorf("failed to create initial group: %w", err)
		}
	}

	// Link owner to this group if not linked
	owner, err := s.storage.GetUserByID(ctx, s.ownerID)
	if err == nil && owner != nil && (owner.GroupID == nil || *owner.GroupID != grp.ID) {
		_ = s.storage.SetUserGroupAndRole(ctx, s.ownerID, grp.ID, domain.RoleOwner, now)
	}

	return grp, nil
}

// JoinGroup connects a user to a group via invite code.
func (s *UserService) JoinGroup(ctx context.Context, userID int64, inviteCode string) (*domain.Group, error) {
	now := s.clock.Now()
	code := strings.TrimSpace(inviteCode)
	if code == "" {
		return nil, ErrInvalidInvite
	}

	grp, err := s.storage.GetGroupByInviteCode(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup invite code: %w", err)
	}
	if grp == nil {
		return nil, ErrInvalidInvite
	}

	u, err := s.storage.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrUserNotFound
	}

	role := domain.RoleStudent
	if userID == s.ownerID {
		role = domain.RoleOwner
	} else if u.Role == domain.RoleAdmin && u.GroupID != nil && *u.GroupID == grp.ID {
		role = domain.RoleAdmin
	}

	if err := s.storage.SetUserGroupAndRole(ctx, userID, grp.ID, role, now); err != nil {
		return nil, fmt.Errorf("failed to assign user to group: %w", err)
	}

	return grp, nil
}

// SetupGroup allows owner to update group name and invite code.
func (s *UserService) SetupGroup(ctx context.Context, ownerID int64, groupID int64, name, inviteCode string) error {
	now := s.clock.Now()
	u, err := s.storage.GetUserByID(ctx, ownerID)
	if err != nil || u == nil || u.Role != domain.RoleOwner {
		return ErrAccessDenied
	}

	name = strings.TrimSpace(name)
	inviteCode = strings.TrimSpace(inviteCode)
	if name == "" || inviteCode == "" {
		return errors.New("название группы и код приглашения не могут быть пустыми")
	}

	if err := s.storage.UpdateGroup(ctx, groupID, name, inviteCode); err != nil {
		return fmt.Errorf("failed to update group: %w", err)
	}

	_ = s.storage.LogAdminAction(ctx, ownerID, "setup_group", fmt.Sprintf("group_id=%d name=%s invite=%s", groupID, name, inviteCode), now)
	return nil
}

// UpdateInviteCode allows admin or owner to change the invite code.
func (s *UserService) UpdateInviteCode(ctx context.Context, adminID int64, groupID int64, newCode string) error {
	now := s.clock.Now()
	u, err := s.storage.GetUserByID(ctx, adminID)
	if err != nil || u == nil || !u.IsAdminOrOwner() {
		return ErrAccessDenied
	}

	newCode = strings.TrimSpace(newCode)
	if newCode == "" {
		return errors.New("код приглашения не может быть пустым")
	}

	if err := s.storage.UpdateInviteCode(ctx, groupID, newCode); err != nil {
		return fmt.Errorf("failed to update invite code: %w", err)
	}

	_ = s.storage.LogAdminAction(ctx, adminID, "change_invite_code", fmt.Sprintf("group_id=%d new_code=%s", groupID, newCode), now)
	return nil
}

// PromoteToAdmin promotes a student to admin (Owner only).
func (s *UserService) PromoteToAdmin(ctx context.Context, ownerID int64, targetUserID int64) error {
	now := s.clock.Now()
	caller, err := s.storage.GetUserByID(ctx, ownerID)
	if err != nil || caller == nil || caller.Role != domain.RoleOwner {
		return ErrAccessDenied
	}

	target, err := s.storage.GetUserByID(ctx, targetUserID)
	if err != nil || target == nil {
		return ErrUserNotFound
	}
	if target.Role == domain.RoleOwner {
		return errors.New("пользователь уже является владельцем")
	}

	if err := s.storage.UpdateUserRole(ctx, targetUserID, domain.RoleAdmin, now); err != nil {
		return fmt.Errorf("failed to promote user to admin: %w", err)
	}

	_ = s.storage.LogAdminAction(ctx, ownerID, "promote_admin", fmt.Sprintf("target_user_id=%d", targetUserID), now)
	return nil
}

// DemoteToStudent demotes an admin to student (Owner only).
func (s *UserService) DemoteToStudent(ctx context.Context, ownerID int64, targetUserID int64) error {
	now := s.clock.Now()
	caller, err := s.storage.GetUserByID(ctx, ownerID)
	if err != nil || caller == nil || caller.Role != domain.RoleOwner {
		return ErrAccessDenied
	}
	if targetUserID == ownerID {
		return errors.New("владелец не может разжаловать самого себя")
	}

	target, err := s.storage.GetUserByID(ctx, targetUserID)
	if err != nil || target == nil {
		return ErrUserNotFound
	}

	if err := s.storage.UpdateUserRole(ctx, targetUserID, domain.RoleStudent, now); err != nil {
		return fmt.Errorf("failed to demote user to student: %w", err)
	}

	_ = s.storage.LogAdminAction(ctx, ownerID, "demote_admin", fmt.Sprintf("target_user_id=%d", targetUserID), now)
	return nil
}

// RevokeStudentAccess revokes user's access to the group.
// Requirement: "После отзыва доступа пользователь перестаёт получать уведомления и видеть данные группы."
func (s *UserService) RevokeStudentAccess(ctx context.Context, adminID int64, targetUserID int64) error {
	now := s.clock.Now()
	caller, err := s.storage.GetUserByID(ctx, adminID)
	if err != nil || caller == nil || !caller.IsAdminOrOwner() {
		return ErrAccessDenied
	}

	if targetUserID == s.ownerID {
		return errors.New("нельзя отозвать доступ у владельца группы")
	}

	target, err := s.storage.GetUserByID(ctx, targetUserID)
	if err != nil || target == nil {
		return ErrUserNotFound
	}
	// Admin cannot revoke another admin or owner
	if caller.Role == domain.RoleAdmin && (target.Role == domain.RoleAdmin || target.Role == domain.RoleOwner) {
		return ErrAccessDenied
	}

	if err := s.storage.UpdateUserStatus(ctx, targetUserID, domain.UserStatusRevoked, now); err != nil {
		return fmt.Errorf("failed to revoke user access: %w", err)
	}

	// Cancel all pending notification jobs for this user
	if _, err := s.storage.DB().ExecContext(ctx, "UPDATE notification_jobs SET status = 'cancelled' WHERE user_id = ? AND status = 'pending'", targetUserID); err != nil {
		return fmt.Errorf("failed to cancel pending jobs for revoked user: %w", err)
	}

	_ = s.storage.LogAdminAction(ctx, adminID, "revoke_access", fmt.Sprintf("target_user_id=%d", targetUserID), now)
	return nil
}

// RestoreStudentAccess restores user's active status.
func (s *UserService) RestoreStudentAccess(ctx context.Context, adminID int64, targetUserID int64) error {
	now := s.clock.Now()
	caller, err := s.storage.GetUserByID(ctx, adminID)
	if err != nil || caller == nil || !caller.IsAdminOrOwner() {
		return ErrAccessDenied
	}

	if err := s.storage.UpdateUserStatus(ctx, targetUserID, domain.UserStatusActive, now); err != nil {
		return fmt.Errorf("failed to restore user access: %w", err)
	}

	_ = s.storage.LogAdminAction(ctx, adminID, "restore_access", fmt.Sprintf("target_user_id=%d", targetUserID), now)
	return nil
}

func (s *UserService) GetGroup(ctx context.Context, groupID int64) (*domain.Group, error) {
	return s.storage.GetGroupByID(ctx, groupID)
}

func (s *UserService) ListGroupUsers(ctx context.Context, groupID int64) ([]domain.User, error) {
	return s.storage.ListGroupUsers(ctx, groupID)
}
