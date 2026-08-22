package domain

import "errors"

// DefaultHistoryWindowDays is the history window an event starts with. It is
// an initial value meant to be tuned in operation.
const DefaultHistoryWindowDays = 30

// ErrHistoryWindowDaysInvalid rejects a history window shorter than one day.
var ErrHistoryWindowDaysInvalid = errors.New("history window days must be at least 1")

// ObservationSettings holds the values Observation reads for an event. Only
// the history window is part of it; further values are added when a need for
// them arises.
type ObservationSettings struct {
	historyWindowDays int
}

// NewObservationSettings builds settings from a requested history window.
func NewObservationSettings(historyWindowDays int) (ObservationSettings, error) {
	if historyWindowDays < 1 {
		return ObservationSettings{}, ErrHistoryWindowDaysInvalid
	}

	return ObservationSettings{historyWindowDays: historyWindowDays}, nil
}

// DefaultObservationSettings returns the settings of an event that has never
// had them changed.
func DefaultObservationSettings() ObservationSettings {
	return ObservationSettings{historyWindowDays: DefaultHistoryWindowDays}
}

func (s ObservationSettings) HistoryWindowDays() int { return s.historyWindowDays }
