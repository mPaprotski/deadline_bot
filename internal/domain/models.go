package domain

import (
	"time"
)

type Role string

const (
	RoleStudent Role = "student"
	RoleAdmin   Role = "admin"
	RoleOwner   Role = "owner"
)

type UserStatus string

const (
	UserStatusActive  UserStatus = "active"
	UserStatusRevoked UserStatus = "revoked"
)

type User struct {
	ID        int64      `json:"id"` // Telegram User ID
	GroupID   *int64     `json:"group_id"`
	Username  string     `json:"username"`
	FirstName string     `json:"first_name"`
	LastName  string     `json:"last_name"`
	Role      Role       `json:"role"`
	Status    UserStatus `json:"status"`
	IsBlocked bool       `json:"is_blocked"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func (u *User) FullName() string {
	name := u.FirstName
	if u.LastName != "" {
		if name != "" {
			name += " " + u.LastName
		} else {
			name = u.LastName
		}
	}
	if name == "" {
		if u.Username != "" {
			return "@" + u.Username
		}
		return "Пользователь"
	}
	return name
}

func (u *User) IsAdminOrOwner() bool {
	return u.Role == RoleAdmin || u.Role == RoleOwner
}

func (u *User) IsOwner() bool {
	return u.Role == RoleOwner
}

type UserSettings struct {
	UserID        int64     `json:"user_id"`
	RemindAll     bool      `json:"remind_all"`
	Remind3d      bool      `json:"remind_3d"`
	Remind1d      bool      `json:"remind_1d"`
	Remind3h      bool      `json:"remind_3h"`
	NotifyNewLab  bool      `json:"notify_new_lab"`
	NotifyChanges bool      `json:"notify_changes"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func DefaultUserSettings(userID int64, now time.Time) UserSettings {
	return UserSettings{
		UserID:        userID,
		RemindAll:     true,
		Remind3d:      true,
		Remind1d:      true,
		Remind3h:      true,
		NotifyNewLab:  true,
		NotifyChanges: true,
		UpdatedAt:     now,
	}
}

type Group struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	InviteCode string    `json:"invite_code"`
	CreatedAt  time.Time `json:"created_at"`
}

type Subject struct {
	ID         int64     `json:"id"`
	GroupID    int64     `json:"group_id"`
	Name       string    `json:"name"`
	IsArchived bool      `json:"is_archived"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Lab struct {
	ID               int64     `json:"id"`
	GroupID          int64     `json:"group_id"`
	SubjectID        int64     `json:"subject_id"`
	SubjectName      string    `json:"subject_name,omitempty"`
	Number           string    `json:"number"`
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	SubmissionURL    string    `json:"submission_url"`
	FileID           string    `json:"file_id"`
	SubmissionMethod string    `json:"submission_method"`
	TeacherComment   string    `json:"teacher_comment"`
	DeadlineAt       time.Time `json:"deadline_at"`
	DeadlineVersion  int       `json:"deadline_version"`
	IsCancelled      bool      `json:"is_cancelled"`
	CreatedBy        int64     `json:"created_by"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`

	// Enriched fields for viewing
	IsCompleted bool       `json:"is_completed,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

type LabDeadlineHistory struct {
	ID            int64     `json:"id"`
	LabID         int64     `json:"lab_id"`
	OldDeadlineAt time.Time `json:"old_deadline_at"`
	NewDeadlineAt time.Time `json:"new_deadline_at"`
	VersionFrom   int       `json:"version_from"`
	VersionTo     int       `json:"version_to"`
	ChangedBy     int64     `json:"changed_by"`
	ChangedAt     time.Time `json:"changed_at"`
}

type StudentSubmission struct {
	UserID   int64     `json:"user_id"`
	LabID    int64     `json:"lab_id"`
	MarkedAt time.Time `json:"marked_at"`
}

type NotificationType string

const (
	Notify3d           NotificationType = "3d"
	Notify1d           NotificationType = "1d"
	Notify3h           NotificationType = "3h"
	NotifyOverdue      NotificationType = "overdue"
	NotifyNewLab       NotificationType = "new_lab"
	NotifyLabChanged   NotificationType = "lab_changed"
	NotifyLabCancelled NotificationType = "lab_cancelled"
)

type NotificationStatus string

const (
	StatusPending    NotificationStatus = "pending"
	StatusProcessing NotificationStatus = "processing"
	StatusSent       NotificationStatus = "sent"
	StatusCancelled  NotificationStatus = "cancelled"
	StatusFailed     NotificationStatus = "failed"
)

type NotificationJob struct {
	ID              int64              `json:"id"`
	UserID          int64              `json:"user_id"`
	LabID           int64              `json:"lab_id"`
	DeadlineVersion int                `json:"deadline_version"`
	NotificationType NotificationType  `json:"notification_type"`
	ScheduledAt     time.Time          `json:"scheduled_at"`
	Status          NotificationStatus `json:"status"`
	RetryCount      int                `json:"retry_count"`
	LastError       string             `json:"last_error"`
	SentAt          *time.Time         `json:"sent_at"`
	CreatedAt       time.Time          `json:"created_at"`
}

type DialogueState struct {
	UserID    int64     `json:"user_id"`
	State     string    `json:"state"`
	Step      string    `json:"step"`
	DraftData string    `json:"draft_data"` // JSON payload
	UpdatedAt time.Time `json:"updated_at"`
}

type AdminAuditLog struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	Action    string    `json:"action"`
	Details   string    `json:"details"`
	CreatedAt time.Time `json:"created_at"`
}
