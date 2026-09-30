package service

import (
	"testing"
	"time"
)

func TestTimeHelper_ParseDeadline(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Minsk")
	if err != nil {
		t.Fatalf("failed to load Europe/Minsk: %v", err)
	}
	th := NewTimeHelper(loc)

	t.Run("valid datetime with time", func(t *testing.T) {
		parsed, dateOnly, err := th.ParseDeadline("25.10.2026 15:30")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dateOnly {
			t.Errorf("expected dateOnly=false, got true")
		}

		inLoc := parsed.In(loc)
		if inLoc.Year() != 2026 || inLoc.Month() != 10 || inLoc.Day() != 25 || inLoc.Hour() != 15 || inLoc.Minute() != 30 {
			t.Errorf("unexpected parsed time in loc: %v", inLoc)
		}
	})

	t.Run("date only defaults to 23:59", func(t *testing.T) {
		parsed, dateOnly, err := th.ParseDeadline("25.10.2026")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !dateOnly {
			t.Errorf("expected dateOnly=true, got false")
		}

		inLoc := parsed.In(loc)
		if inLoc.Year() != 2026 || inLoc.Month() != 10 || inLoc.Day() != 25 || inLoc.Hour() != 23 || inLoc.Minute() != 59 {
			t.Errorf("expected 23:59, got: %02d:%02d", inLoc.Hour(), inLoc.Minute())
		}
	})

	t.Run("invalid format", func(t *testing.T) {
		_, _, err := th.ParseDeadline("not-a-date")
		if err == nil {
			t.Errorf("expected error for invalid date, got nil")
		}
	})
}

func TestTimeHelper_CalendarWeekBounds(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Minsk")
	if err != nil {
		t.Fatalf("failed to load Europe/Minsk: %v", err)
	}
	th := NewTimeHelper(loc)

	// Wednesday, Oct 21, 2026 at 14:00 Minsk time
	wednesday := time.Date(2026, 10, 21, 14, 0, 0, 0, loc)
	startOfWeek, endOfWeek := th.CalendarWeekBounds(wednesday)

	startInLoc := startOfWeek.In(loc)
	endInLoc := endOfWeek.In(loc)

	// Monday Oct 19, 00:00:00
	if startInLoc.Weekday() != time.Monday || startInLoc.Day() != 19 || startInLoc.Hour() != 0 || startInLoc.Minute() != 0 {
		t.Errorf("expected Monday Oct 19 00:00, got: %v", startInLoc)
	}

	// Sunday Oct 25, 23:59:59
	if endInLoc.Weekday() != time.Sunday || endInLoc.Day() != 25 || endInLoc.Hour() != 23 || endInLoc.Minute() != 59 {
		t.Errorf("expected Sunday Oct 25 23:59, got: %v", endInLoc)
	}
}

func TestTimeHelper_RemainingOrOverdue(t *testing.T) {
	loc := time.UTC
	th := NewTimeHelper(loc)

	now := time.Date(2026, 10, 20, 12, 0, 0, 0, loc)

	// Future deadline (2 days 4 hours)
	dlFuture := now.Add(52 * time.Hour)
	text, isOverdue := th.RemainingOrOverdue(dlFuture, now)
	if isOverdue {
		t.Errorf("expected future deadline, got overdue")
	}
	if text != "⏳ Осталось: 2 дн. 4 ч." {
		t.Errorf("unexpected text: %s", text)
	}

	// Past deadline (1 day 2 hours overdue)
	dlPast := now.Add(-26 * time.Hour)
	text, isOverdue = th.RemainingOrOverdue(dlPast, now)
	if !isOverdue {
		t.Errorf("expected overdue, got future")
	}
	if text != "⚠️ Просрочено на 1 дн. 2 ч." {
		t.Errorf("unexpected text: %s", text)
	}
}
