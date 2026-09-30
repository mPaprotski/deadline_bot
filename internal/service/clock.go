package service

import "time"

type Clock interface {
	Now() time.Time
}

type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now().UTC()
}

type MockClock struct {
	CurrentTime time.Time
}

func NewMockClock(t time.Time) *MockClock {
	return &MockClock{CurrentTime: t.UTC()}
}

func (m *MockClock) Now() time.Time {
	return m.CurrentTime
}

func (m *MockClock) Set(t time.Time) {
	m.CurrentTime = t.UTC()
}

func (m *MockClock) Add(d time.Duration) {
	m.CurrentTime = m.CurrentTime.Add(d)
}
