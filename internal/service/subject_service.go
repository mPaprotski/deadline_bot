package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"deadline_bot/internal/domain"
	"deadline_bot/internal/storage/sqlite"
)

type SubjectService struct {
	storage *sqlite.Storage
	clock   Clock
}

func NewSubjectService(storage *sqlite.Storage, clock Clock) *SubjectService {
	if clock == nil {
		clock = RealClock{}
	}
	return &SubjectService{
		storage: storage,
		clock:   clock,
	}
}

func (s *SubjectService) CreateSubject(ctx context.Context, adminID int64, groupID int64, name string) (*domain.Subject, error) {
	now := s.clock.Now()
	u, err := s.storage.GetUserByID(ctx, adminID)
	if err != nil || u == nil || !u.IsAdminOrOwner() {
		return nil, ErrAccessDenied
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("название предмета не может быть пустым")
	}
	if len([]rune(name)) > 100 {
		return nil, errors.New("название предмета слишком длинное (максимум 100 символов)")
	}

	sub, err := s.storage.CreateSubject(ctx, groupID, name, now)
	if err != nil {
		return nil, fmt.Errorf("failed to create subject: %w", err)
	}

	_ = s.storage.LogAdminAction(ctx, adminID, "create_subject", fmt.Sprintf("subject_id=%d name=%s", sub.ID, name), now)
	return sub, nil
}

func (s *SubjectService) RenameSubject(ctx context.Context, adminID int64, subjectID int64, newName string) error {
	now := s.clock.Now()
	u, err := s.storage.GetUserByID(ctx, adminID)
	if err != nil || u == nil || !u.IsAdminOrOwner() {
		return ErrAccessDenied
	}

	newName = strings.TrimSpace(newName)
	if newName == "" {
		return errors.New("название предмета не может быть пустым")
	}
	if len([]rune(newName)) > 100 {
		return errors.New("название предмета слишком длинное (максимум 100 символов)")
	}

	if err := s.storage.RenameSubject(ctx, subjectID, newName, now); err != nil {
		return fmt.Errorf("failed to rename subject: %w", err)
	}

	_ = s.storage.LogAdminAction(ctx, adminID, "rename_subject", fmt.Sprintf("subject_id=%d new_name=%s", subjectID, newName), now)
	return nil
}

// ArchiveSubject archives or restores a subject.
// Requirement: "Работы архивного предмета не отображаются среди актуальных дедлайнов, напоминания по ним прекращаются."
func (s *SubjectService) ArchiveSubject(ctx context.Context, adminID int64, subjectID int64, archive bool) error {
	now := s.clock.Now()
	u, err := s.storage.GetUserByID(ctx, adminID)
	if err != nil || u == nil || !u.IsAdminOrOwner() {
		return ErrAccessDenied
	}

	if err := s.storage.ArchiveSubject(ctx, subjectID, archive, now); err != nil {
		return fmt.Errorf("failed to archive subject: %w", err)
	}

	if archive {
		// Cancel all pending reminders for labs under this archived subject
		cancelQuery := `
			UPDATE notification_jobs
			SET status = 'cancelled'
			WHERE status = 'pending' AND lab_id IN (SELECT id FROM labs WHERE subject_id = ?)
		`
		if _, err := s.storage.DB().ExecContext(ctx, cancelQuery, subjectID); err != nil {
			return fmt.Errorf("failed to cancel pending jobs for archived subject: %w", err)
		}
	}

	action := "archive_subject"
	if !archive {
		action = "unarchive_subject"
	}
	_ = s.storage.LogAdminAction(ctx, adminID, action, fmt.Sprintf("subject_id=%d", subjectID), now)
	return nil
}

func (s *SubjectService) GetSubject(ctx context.Context, id int64) (*domain.Subject, error) {
	return s.storage.GetSubjectByID(ctx, id)
}

func (s *SubjectService) ListActiveSubjects(ctx context.Context, groupID int64) ([]domain.Subject, error) {
	return s.storage.ListActiveSubjects(ctx, groupID)
}

func (s *SubjectService) ListAllSubjects(ctx context.Context, groupID int64) ([]domain.Subject, error) {
	return s.storage.ListAllSubjects(ctx, groupID)
}
