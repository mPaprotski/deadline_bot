package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"deadline_bot/internal/domain"
	"deadline_bot/internal/storage/sqlite"
)

type LabDraft struct {
	SubjectID        int64     `json:"subject_id"`
	SubjectName      string    `json:"subject_name"`
	Number           string    `json:"number"`
	Title            string    `json:"title"`
	Description      string    `json:"description"`
	DeadlineAt       time.Time `json:"deadline_at"`
	IsDateOnly       bool      `json:"is_date_only"`
	ConfirmedPast    bool      `json:"confirmed_past"`
	SubmissionURL    string    `json:"submission_url"`
	FileID           string    `json:"file_id"`
	FileName         string    `json:"file_name"`
	SubmissionMethod string    `json:"submission_method"`
	TeacherComment   string    `json:"teacher_comment"`
}

type RescheduleDraft struct {
	LabID         int64     `json:"lab_id"`
	LabTitle      string    `json:"lab_title"`
	NewDeadlineAt time.Time `json:"new_deadline_at"`
	ConfirmedPast bool      `json:"confirmed_past"`
}

type GroupSetupDraft struct {
	GroupID    int64  `json:"group_id"`
	GroupName  string `json:"group_name"`
	InviteCode string `json:"invite_code"`
}

type FSMManager struct {
	storage *sqlite.Storage
}

func NewFSMManager(storage *sqlite.Storage) *FSMManager {
	return &FSMManager{storage: storage}
}

func (m *FSMManager) GetState(ctx context.Context, userID int64) (*domain.DialogueState, error) {
	return m.storage.GetDialogueState(ctx, userID)
}

func (m *FSMManager) SetState(ctx context.Context, userID int64, state, step string, draft any) error {
	var draftJSON string
	if draft != nil {
		bytes, err := json.Marshal(draft)
		if err != nil {
			return fmt.Errorf("failed to marshal draft data: %w", err)
		}
		draftJSON = string(bytes)
	}

	ds := &domain.DialogueState{
		UserID:    userID,
		State:     state,
		Step:      step,
		DraftData: draftJSON,
		UpdatedAt: time.Now().UTC(),
	}
	return m.storage.SaveDialogueState(ctx, ds)
}

func (m *FSMManager) Clear(ctx context.Context, userID int64) error {
	return m.storage.ClearDialogueState(ctx, userID)
}

func (m *FSMManager) GetLabDraft(ds *domain.DialogueState) (*LabDraft, error) {
	if ds == nil || ds.DraftData == "" {
		return &LabDraft{}, nil
	}
	var draft LabDraft
	if err := json.Unmarshal([]byte(ds.DraftData), &draft); err != nil {
		return nil, err
	}
	return &draft, nil
}

func (m *FSMManager) GetRescheduleDraft(ds *domain.DialogueState) (*RescheduleDraft, error) {
	if ds == nil || ds.DraftData == "" {
		return &RescheduleDraft{}, nil
	}
	var draft RescheduleDraft
	if err := json.Unmarshal([]byte(ds.DraftData), &draft); err != nil {
		return nil, err
	}
	return &draft, nil
}

func (m *FSMManager) GetGroupSetupDraft(ds *domain.DialogueState) (*GroupSetupDraft, error) {
	if ds == nil || ds.DraftData == "" {
		return &GroupSetupDraft{}, nil
	}
	var draft GroupSetupDraft
	if err := json.Unmarshal([]byte(ds.DraftData), &draft); err != nil {
		return nil, err
	}
	return &draft, nil
}

// ParseNumberAndTitle extracts number and title from user input.
// E.g. "1. Основы Go" -> ("1", "Основы Go")
// "Лаб 2 Введение" -> ("2", "Введение")
// "Проект" -> ("1", "Проект")
func ParseNumberAndTitle(input string) (string, string) {
	input = strings.TrimSpace(input)
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return "1", ""
	}
	if len(fields) == 1 {
		return "1", fields[0]
	}

	first := strings.TrimRight(fields[0], ".:#№")
	if strings.EqualFold(first, "лаб") || strings.EqualFold(first, "лр") {
		if len(fields) >= 3 {
			num := strings.TrimRight(fields[1], ".:#№")
			title := strings.Join(fields[2:], " ")
			return num, title
		}
	}

	// Check if first token starts with or is a number
	num := strings.TrimRight(fields[0], ".:#№")
	title := strings.Join(fields[1:], " ")
	return num, title
}
