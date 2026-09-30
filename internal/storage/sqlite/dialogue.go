package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"deadline_bot/internal/domain"
)

func (s *Storage) GetDialogueState(ctx context.Context, userID int64) (*domain.DialogueState, error) {
	query := `SELECT user_id, state, step, draft_data, updated_at FROM dialogue_states WHERE user_id = ?`
	row := s.db.QueryRowContext(ctx, query, userID)

	var st domain.DialogueState
	var updatedAt int64
	err := row.Scan(&st.UserID, &st.State, &st.Step, &st.DraftData, &updatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query dialogue state: %w", err)
	}
	st.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return &st, nil
}

func (s *Storage) SaveDialogueState(ctx context.Context, state *domain.DialogueState) error {
	query := `
		INSERT INTO dialogue_states (user_id, state, step, draft_data, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			state = excluded.state,
			step = excluded.step,
			draft_data = excluded.draft_data,
			updated_at = excluded.updated_at
	`
	_, err := s.db.ExecContext(ctx, query,
		state.UserID,
		state.State,
		state.Step,
		state.DraftData,
		state.UpdatedAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("failed to save dialogue state: %w", err)
	}
	return nil
}

func (s *Storage) ClearDialogueState(ctx context.Context, userID int64) error {
	query := `DELETE FROM dialogue_states WHERE user_id = ?`
	_, err := s.db.ExecContext(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("failed to clear dialogue state: %w", err)
	}
	return nil
}
