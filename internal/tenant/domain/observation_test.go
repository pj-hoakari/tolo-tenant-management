package domain

import (
	"errors"
	"testing"
)

func TestNewObservationSettings(t *testing.T) {
	t.Parallel()

	settings, err := NewObservationSettings(45)
	if err != nil {
		t.Fatalf("NewObservationSettings() error = %v", err)
	}

	if got, want := settings.HistoryWindowDays(), 45; got != want {
		t.Errorf("HistoryWindowDays() = %d, want %d", got, want)
	}
}

func TestNewObservationSettingsRejectsNonPositiveWindows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		historyWindowDays int
	}{
		{name: "zero", historyWindowDays: 0},
		{name: "negative", historyWindowDays: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings, err := NewObservationSettings(tt.historyWindowDays)
			if !errors.Is(err, ErrHistoryWindowDaysInvalid) {
				t.Fatalf("NewObservationSettings(%d) error = %v, want %v", tt.historyWindowDays, err, ErrHistoryWindowDaysInvalid)
			}

			if settings != (ObservationSettings{}) {
				t.Errorf("NewObservationSettings(%d) = %#v, want zero", tt.historyWindowDays, settings)
			}
		})
	}
}

func TestDefaultObservationSettings(t *testing.T) {
	t.Parallel()

	if got, want := DefaultObservationSettings().HistoryWindowDays(), DefaultHistoryWindowDays; got != want {
		t.Errorf("HistoryWindowDays() = %d, want %d", got, want)
	}

	if got, want := DefaultHistoryWindowDays, 30; got != want {
		t.Errorf("DefaultHistoryWindowDays = %d, want %d", got, want)
	}
}
