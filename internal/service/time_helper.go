package service

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type TimeHelper struct {
	loc *time.Location
}

func NewTimeHelper(loc *time.Location) *TimeHelper {
	if loc == nil {
		loc = time.UTC
	}
	return &TimeHelper{loc: loc}
}

func (th *TimeHelper) Location() *time.Location {
	return th.loc
}

func (th *TimeHelper) TimezoneName() string {
	return th.loc.String()
}

// ParseDeadline parses a user-entered deadline string in the configured timezone.
// If the input contains only a date, it defaults the time to 23:59:00 and returns dateOnly=true.
func (th *TimeHelper) ParseDeadline(input string) (t time.Time, dateOnly bool, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return time.Time{}, false, errors.New("пустая строка с датой")
	}

	formatsWithTime := []string{
		"02.01.2006 15:04",
		"02.01.06 15:04",
		"2006-01-02 15:04",
		"02/01/2006 15:04",
		"02.01.2006 15:04:05",
		"2006-01-02 15:04:05",
	}

	for _, format := range formatsWithTime {
		if parsed, err := time.ParseInLocation(format, input, th.loc); err == nil {
			return parsed.UTC(), false, nil
		}
	}

	formatsDateOnly := []string{
		"02.01.2006",
		"02.01.06",
		"2006-01-02",
		"02/01/2006",
	}

	for _, format := range formatsDateOnly {
		if parsed, err := time.ParseInLocation(format, input, th.loc); err == nil {
			withDefaultTime := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 0, 0, th.loc)
			return withDefaultTime.UTC(), true, nil
		}
	}

	return time.Time{}, false, fmt.Errorf("неверный формат даты. Примеры: 25.10.2026 23:59 или 25.10.2026")
}

// FormatInGroupTZ formats a UTC time in group timezone.
func (th *TimeHelper) FormatInGroupTZ(t time.Time) string {
	inLoc := t.In(th.loc)
	return fmt.Sprintf("%02d.%02d.%d %02d:%02d (%s)",
		inLoc.Day(), inLoc.Month(), inLoc.Year(),
		inLoc.Hour(), inLoc.Minute(),
		th.loc.String(),
	)
}

// FormatShortInGroupTZ formats date and time without timezone string.
func (th *TimeHelper) FormatShortInGroupTZ(t time.Time) string {
	inLoc := t.In(th.loc)
	return fmt.Sprintf("%02d.%02d.%d %02d:%02d",
		inLoc.Day(), inLoc.Month(), inLoc.Year(),
		inLoc.Hour(), inLoc.Minute(),
	)
}

// CalendarWeekBounds returns the start (Monday 00:00:00) and end (Sunday 23:59:59.999999999)
// of the calendar week for the given time in the group timezone.
func (th *TimeHelper) CalendarWeekBounds(t time.Time) (startOfWeek, endOfWeek time.Time) {
	inLoc := t.In(th.loc)
	// Sunday is 0, Monday is 1, ..., Saturday is 6
	weekday := int(inLoc.Weekday())
	// In Russian / ISO calendar, Monday is day 0 of week
	offset := (weekday + 6) % 7

	monday := time.Date(inLoc.Year(), inLoc.Month(), inLoc.Day()-offset, 0, 0, 0, 0, th.loc)
	sundayEnd := monday.AddDate(0, 0, 7).Add(-time.Nanosecond)

	return monday.UTC(), sundayEnd.UTC()
}

// RemainingOrOverdue returns a human-readable Russian string describing time left or overdue.
func (th *TimeHelper) RemainingOrOverdue(deadline, now time.Time) (text string, isOverdue bool) {
	diff := deadline.Sub(now)
	if diff < 0 {
		overdue := -diff
		return fmt.Sprintf("⚠️ Просрочено на %s", formatDurationRU(overdue)), true
	}
	return fmt.Sprintf("⏳ Осталось: %s", formatDurationRU(diff)), false
}

func formatDurationRU(d time.Duration) string {
	if d < time.Minute {
		return "меньше минуты"
	}
	totalMinutes := int(d.Minutes())
	days := totalMinutes / (24 * 60)
	hours := (totalMinutes % (24 * 60)) / 60
	mins := totalMinutes % 60

	parts := []string{}
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%d дн.", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%d ч.", hours))
	}
	if mins > 0 && days == 0 { // show minutes only if less than a day or if hours > 0
		parts = append(parts, fmt.Sprintf("%d мин.", mins))
	}

	if len(parts) == 0 {
		return "менее 1 мин."
	}
	return strings.Join(parts, " ")
}
