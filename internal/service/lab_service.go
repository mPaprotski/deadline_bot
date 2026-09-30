package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"deadline_bot/internal/domain"
	"deadline_bot/internal/storage/sqlite"
)

type LabService struct {
	storage    *sqlite.Storage
	clock      Clock
	timeHelper *TimeHelper
}

func NewLabService(storage *sqlite.Storage, clock Clock, timeHelper *TimeHelper) *LabService {
	if clock == nil {
		clock = RealClock{}
	}
	return &LabService{
		storage:    storage,
		clock:      clock,
		timeHelper: timeHelper,
	}
}

type CreateLabInput struct {
	AdminID          int64
	GroupID          int64
	SubjectID        int64
	Number           string
	Title            string
	Description      string
	SubmissionURL    string
	FileID           string
	SubmissionMethod string
	TeacherComment   string
	DeadlineAt       time.Time
	AllowPast        bool
}

func (s *LabService) CreateLab(ctx context.Context, in CreateLabInput) (*domain.Lab, error) {
	now := s.clock.Now()
	caller, err := s.storage.GetUserByID(ctx, in.AdminID)
	if err != nil || caller == nil || !caller.IsAdminOrOwner() {
		return nil, ErrAccessDenied
	}

	sub, err := s.storage.GetSubjectByID(ctx, in.SubjectID)
	if err != nil || sub == nil {
		return nil, errors.New("предмет не найден")
	}
	if sub.IsArchived {
		return nil, errors.New("нельзя создать работу в архивном предмете")
	}

	in.Number = strings.TrimSpace(in.Number)
	in.Title = strings.TrimSpace(in.Title)
	if in.Number == "" || in.Title == "" {
		return nil, errors.New("номер и название работы обязательны")
	}

	if in.SubmissionURL != "" {
		in.SubmissionURL = strings.TrimSpace(in.SubmissionURL)
		parsedURL, err := url.ParseRequestURI(in.SubmissionURL)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			return nil, fmt.Errorf("некорректная ссылка на задание: укажите валидный URL с http:// или https://")
		}
	}

	if in.DeadlineAt.Before(now) && !in.AllowPast {
		return nil, errors.New("дедлайн находится в прошлом; требуется подтверждение администратора")
	}

	lab := &domain.Lab{
		GroupID:          in.GroupID,
		SubjectID:        in.SubjectID,
		SubjectName:      sub.Name,
		Number:           in.Number,
		Title:            in.Title,
		Description:      strings.TrimSpace(in.Description),
		SubmissionURL:    in.SubmissionURL,
		FileID:           strings.TrimSpace(in.FileID),
		SubmissionMethod: strings.TrimSpace(in.SubmissionMethod),
		TeacherComment:   strings.TrimSpace(in.TeacherComment),
		DeadlineAt:       in.DeadlineAt.UTC(),
		DeadlineVersion:  1,
		IsCancelled:      false,
		CreatedBy:        in.AdminID,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	createdLab, err := s.storage.CreateLab(ctx, lab)
	if err != nil {
		return nil, fmt.Errorf("failed to save lab: %w", err)
	}

	// Schedule notifications for active group students
	students, err := s.storage.ListActiveGroupStudents(ctx, in.GroupID)
	if err == nil && len(students) > 0 {
		var jobs []domain.NotificationJob
		for _, st := range students {
			cfg, err := s.storage.GetUserSettings(ctx, st.ID)
			if err != nil || cfg == nil {
				def := domain.DefaultUserSettings(st.ID, now)
				cfg = &def
			}

			// New lab broadcast
			if cfg.NotifyNewLab {
				jobs = append(jobs, domain.NotificationJob{
					UserID:           st.ID,
					LabID:            createdLab.ID,
					DeadlineVersion:  1,
					NotificationType: domain.NotifyNewLab,
					ScheduledAt:      now,
					Status:           domain.StatusPending,
					CreatedAt:        now,
				})
			}

			// Reminders (only future)
			if cfg.RemindAll {
				jobs = append(jobs, s.buildFutureReminders(st.ID, createdLab.ID, 1, createdLab.DeadlineAt, now, cfg)...)
			}
		}

		if len(jobs) > 0 {
			_ = s.storage.CreateNotificationJobs(ctx, jobs)
		}
	}

	_ = s.storage.LogAdminAction(ctx, in.AdminID, "create_lab", fmt.Sprintf("lab_id=%d title=%s", createdLab.ID, createdLab.Title), now)
	return createdLab, nil
}

type UpdateLabInput struct {
	AdminID          int64
	LabID            int64
	SubjectID        int64
	Number           string
	Title            string
	Description      string
	SubmissionURL    string
	FileID           string
	SubmissionMethod string
	TeacherComment   string
}

func (s *LabService) UpdateLab(ctx context.Context, in UpdateLabInput) error {
	now := s.clock.Now()
	caller, err := s.storage.GetUserByID(ctx, in.AdminID)
	if err != nil || caller == nil || !caller.IsAdminOrOwner() {
		return ErrAccessDenied
	}

	existing, err := s.storage.GetLabByID(ctx, in.LabID, in.AdminID)
	if err != nil || existing == nil {
		return errors.New("лабораторная работа не найдена")
	}

	sub, err := s.storage.GetSubjectByID(ctx, in.SubjectID)
	if err != nil || sub == nil {
		return errors.New("предмет не найден")
	}

	in.Number = strings.TrimSpace(in.Number)
	in.Title = strings.TrimSpace(in.Title)
	if in.Number == "" || in.Title == "" {
		return errors.New("номер и название работы обязательны")
	}

	if in.SubmissionURL != "" {
		in.SubmissionURL = strings.TrimSpace(in.SubmissionURL)
		parsedURL, err := url.ParseRequestURI(in.SubmissionURL)
		if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
			return fmt.Errorf("некорректная ссылка на задание: укажите валидный URL с http:// или https://")
		}
	}

	existing.SubjectID = in.SubjectID
	existing.Number = in.Number
	existing.Title = in.Title
	existing.Description = strings.TrimSpace(in.Description)
	existing.SubmissionURL = in.SubmissionURL
	existing.FileID = strings.TrimSpace(in.FileID)
	existing.SubmissionMethod = strings.TrimSpace(in.SubmissionMethod)
	existing.TeacherComment = strings.TrimSpace(in.TeacherComment)
	existing.UpdatedAt = now

	if err := s.storage.UpdateLab(ctx, existing); err != nil {
		return fmt.Errorf("failed to update lab: %w", err)
	}

	_ = s.storage.LogAdminAction(ctx, in.AdminID, "update_lab", fmt.Sprintf("lab_id=%d", in.LabID), now)
	return nil
}

// RescheduleLabDeadline changes the deadline, increments version, cancels old reminders, schedules new ones, and notifies users.
func (s *LabService) RescheduleLabDeadline(ctx context.Context, adminID int64, labID int64, newDeadline time.Time, allowPast bool) (time.Time, time.Time, int, error) {
	now := s.clock.Now()
	caller, err := s.storage.GetUserByID(ctx, adminID)
	if err != nil || caller == nil || !caller.IsAdminOrOwner() {
		return time.Time{}, time.Time{}, 0, ErrAccessDenied
	}

	lab, err := s.storage.GetLabByID(ctx, labID, adminID)
	if err != nil || lab == nil {
		return time.Time{}, time.Time{}, 0, errors.New("лабораторная работа не найдена")
	}
	if lab.IsCancelled {
		return time.Time{}, time.Time{}, 0, errors.New("нельзя перенести дедлайн отмененной работы")
	}

	newDeadlineUTC := newDeadline.UTC()
	if newDeadlineUTC.Before(now) && !allowPast {
		return time.Time{}, time.Time{}, 0, errors.New("новый дедлайн находится в прошлом; требуется подтверждение администратора")
	}

	oldDeadline, newVersion, err := s.storage.RescheduleLabDeadline(ctx, labID, newDeadlineUTC, adminID, now)
	if err != nil {
		return time.Time{}, time.Time{}, 0, fmt.Errorf("failed to reschedule lab: %w", err)
	}

	// Cancel old pending reminders
	_ = s.storage.CancelPendingOldVersionJobs(ctx, labID, newVersion)

	// Reschedule for active students
	students, err := s.storage.ListActiveGroupStudents(ctx, lab.GroupID)
	if err == nil && len(students) > 0 {
		var jobs []domain.NotificationJob
		for _, st := range students {
			cfg, err := s.storage.GetUserSettings(ctx, st.ID)
			if err != nil || cfg == nil {
				def := domain.DefaultUserSettings(st.ID, now)
				cfg = &def
			}

			// Broadcast change notification
			if cfg.NotifyChanges {
				jobs = append(jobs, domain.NotificationJob{
					UserID:           st.ID,
					LabID:            labID,
					DeadlineVersion:  newVersion,
					NotificationType: domain.NotifyLabChanged,
					ScheduledAt:      now,
					Status:           domain.StatusPending,
					CreatedAt:        now,
				})
			}

			// If student already marked lab as completed, do not schedule reminders
			isDone, _ := s.storage.IsLabCompleted(ctx, st.ID, labID)
			if !isDone && cfg.RemindAll {
				jobs = append(jobs, s.buildFutureReminders(st.ID, labID, newVersion, newDeadlineUTC, now, cfg)...)
			}
		}

		if len(jobs) > 0 {
			_ = s.storage.CreateNotificationJobs(ctx, jobs)
		}
	}

	_ = s.storage.LogAdminAction(ctx, adminID, "reschedule_lab",
		fmt.Sprintf("lab_id=%d old=%s new=%s version=%d", labID, oldDeadline.Format(time.RFC3339), newDeadlineUTC.Format(time.RFC3339), newVersion), now)

	return oldDeadline, newDeadlineUTC, newVersion, nil
}

// CancelLab cancels the lab, stops all pending reminders, and notifies students with NotifyChanges enabled.
func (s *LabService) CancelLab(ctx context.Context, adminID int64, labID int64) error {
	now := s.clock.Now()
	caller, err := s.storage.GetUserByID(ctx, adminID)
	if err != nil || caller == nil || !caller.IsAdminOrOwner() {
		return ErrAccessDenied
	}

	lab, err := s.storage.GetLabByID(ctx, labID, adminID)
	if err != nil || lab == nil {
		return errors.New("лабораторная работа не найдена")
	}

	if err := s.storage.CancelLab(ctx, labID, adminID, now); err != nil {
		return fmt.Errorf("failed to cancel lab: %w", err)
	}

	// Cancel all pending reminders for this lab
	_ = s.storage.CancelPendingJobsForLab(ctx, labID)

	// Notify students about cancellation
	students, err := s.storage.ListActiveGroupStudents(ctx, lab.GroupID)
	if err == nil && len(students) > 0 {
		var jobs []domain.NotificationJob
		for _, st := range students {
			cfg, err := s.storage.GetUserSettings(ctx, st.ID)
			if err != nil || cfg == nil {
				def := domain.DefaultUserSettings(st.ID, now)
				cfg = &def
			}
			if cfg.NotifyChanges {
				jobs = append(jobs, domain.NotificationJob{
					UserID:           st.ID,
					LabID:            labID,
					DeadlineVersion:  lab.DeadlineVersion,
					NotificationType: domain.NotifyLabCancelled,
					ScheduledAt:      now,
					Status:           domain.StatusPending,
					CreatedAt:        now,
				})
			}
		}
		if len(jobs) > 0 {
			_ = s.storage.CreateNotificationJobs(ctx, jobs)
		}
	}

	_ = s.storage.LogAdminAction(ctx, adminID, "cancel_lab", fmt.Sprintf("lab_id=%d", labID), now)
	return nil
}

// MarkCompleted marks a lab as completed for the student and stops future reminders.
func (s *LabService) MarkCompleted(ctx context.Context, userID int64, labID int64) error {
	now := s.clock.Now()
	user, err := s.storage.GetUserByID(ctx, userID)
	if err != nil || user == nil || user.Status != domain.UserStatusActive {
		return ErrAccessDenied
	}

	if err := s.storage.MarkLabCompleted(ctx, userID, labID, now); err != nil {
		return fmt.Errorf("failed to mark lab completed: %w", err)
	}

	// Requirement: "После отметки «Сдано» дальнейшие напоминания прекращаются."
	if err := s.storage.CancelPendingJobsForLabAndUser(ctx, labID, userID); err != nil {
		return fmt.Errorf("failed to cancel pending jobs: %w", err)
	}

	return nil
}

// UnmarkCompleted removes completion mark and restores future reminders.
func (s *LabService) UnmarkCompleted(ctx context.Context, userID int64, labID int64) error {
	now := s.clock.Now()
	user, err := s.storage.GetUserByID(ctx, userID)
	if err != nil || user == nil || user.Status != domain.UserStatusActive {
		return ErrAccessDenied
	}

	if err := s.storage.UnmarkLabCompleted(ctx, userID, labID); err != nil {
		return fmt.Errorf("failed to unmark lab completed: %w", err)
	}

	// Requirement: "После снятия отметки восстанавливаются только будущие напоминания."
	lab, err := s.storage.GetLabByID(ctx, labID, userID)
	if err == nil && lab != nil && !lab.IsCancelled {
		cfg, err := s.storage.GetUserSettings(ctx, userID)
		if err != nil || cfg == nil {
			def := domain.DefaultUserSettings(userID, now)
			cfg = &def
		}
		if cfg.RemindAll {
			futureJobs := s.buildFutureReminders(userID, labID, lab.DeadlineVersion, lab.DeadlineAt, now, cfg)
			if len(futureJobs) > 0 {
				_ = s.storage.UpsertNotificationJobs(ctx, futureJobs)
			}
		}
	}

	return nil
}

func (s *LabService) GetLab(ctx context.Context, labID int64, userID int64) (*domain.Lab, error) {
	return s.storage.GetLabByID(ctx, labID, userID)
}

func (s *LabService) ListUpcoming(ctx context.Context, groupID int64, userID int64, limit, offset int) ([]domain.Lab, int, error) {
	return s.storage.ListUpcomingDeadlines(ctx, groupID, userID, s.clock.Now(), limit, offset)
}

func (s *LabService) ListThisWeek(ctx context.Context, groupID int64, userID int64, limit, offset int) ([]domain.Lab, int, error) {
	now := s.clock.Now()
	startWeek, endWeek := s.timeHelper.CalendarWeekBounds(now)
	return s.storage.ListWeekDeadlines(ctx, groupID, userID, startWeek, endWeek, limit, offset)
}

func (s *LabService) ListOverdue(ctx context.Context, groupID int64, userID int64, limit, offset int) ([]domain.Lab, int, error) {
	return s.storage.ListOverdueDeadlines(ctx, groupID, userID, s.clock.Now(), limit, offset)
}

func (s *LabService) ListCompleted(ctx context.Context, groupID int64, userID int64, limit, offset int) ([]domain.Lab, int, error) {
	return s.storage.ListCompletedDeadlines(ctx, groupID, userID, limit, offset)
}

func (s *LabService) ListBySubject(ctx context.Context, subjectID int64, userID int64, limit, offset int) ([]domain.Lab, int, error) {
	return s.storage.ListLabsBySubject(ctx, subjectID, userID, limit, offset)
}

func (s *LabService) ListAllForAdmin(ctx context.Context, groupID int64, limit, offset int) ([]domain.Lab, int, error) {
	return s.storage.ListAllActiveLabsForAdmin(ctx, groupID, limit, offset)
}

// buildFutureReminders constructs only reminder jobs whose scheduled time is strictly in the future.
func (s *LabService) buildFutureReminders(userID int64, labID int64, version int, deadline time.Time, now time.Time, cfg *domain.UserSettings) []domain.NotificationJob {
	var jobs []domain.NotificationJob

	// 3 days before
	t3d := deadline.Add(-72 * time.Hour)
	if cfg.Remind3d && t3d.After(now) {
		jobs = append(jobs, domain.NotificationJob{
			UserID:           userID,
			LabID:            labID,
			DeadlineVersion:  version,
			NotificationType: domain.Notify3d,
			ScheduledAt:      t3d,
			Status:           domain.StatusPending,
			CreatedAt:        now,
		})
	}

	// 1 day before
	t1d := deadline.Add(-24 * time.Hour)
	if cfg.Remind1d && t1d.After(now) {
		jobs = append(jobs, domain.NotificationJob{
			UserID:           userID,
			LabID:            labID,
			DeadlineVersion:  version,
			NotificationType: domain.Notify1d,
			ScheduledAt:      t1d,
			Status:           domain.StatusPending,
			CreatedAt:        now,
		})
	}

	// 3 hours before
	t3h := deadline.Add(-3 * time.Hour)
	if cfg.Remind3h && t3h.After(now) {
		jobs = append(jobs, domain.NotificationJob{
			UserID:           userID,
			LabID:            labID,
			DeadlineVersion:  version,
			NotificationType: domain.Notify3h,
			ScheduledAt:      t3h,
			Status:           domain.StatusPending,
			CreatedAt:        now,
		})
	}

	// Overdue reminder: scheduled at deadline time
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

	return jobs
}
