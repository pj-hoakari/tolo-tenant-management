package domain

import (
	"errors"
	"testing"
)

func TestEventTransitionTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from string
		to   string
	}{
		{name: "opens a draft event", from: EventStatusDraft, to: EventStatusOpen},
		{name: "locks an open event", from: EventStatusOpen, to: EventStatusLocked},
		{name: "closes a locked event", from: EventStatusLocked, to: EventStatusClosed},
		{name: "unlocks a locked event", from: EventStatusLocked, to: EventStatusOpen},
		{name: "reopens a closed event", from: EventStatusClosed, to: EventStatusOpen},
		{name: "archives a closed event", from: EventStatusClosed, to: EventStatusArchived},
		{name: "unarchives an archived event", from: EventStatusArchived, to: EventStatusClosed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := Event{status: tt.from}

			updatedEvent, err := event.TransitionTo(tt.to)
			if err != nil {
				t.Fatalf("TransitionTo(%q) error = %v", tt.to, err)
			}

			if got := updatedEvent.Status(); got != tt.to {
				t.Errorf("updated event status = %q, want %q", got, tt.to)
			}

			if got := event.Status(); got != tt.from {
				t.Errorf("original event status = %q, want %q", got, tt.from)
			}
		})
	}
}

func TestEventTransitionToRejectsInvalidTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from string
		to   string
	}{
		{name: "cannot lock a draft event", from: EventStatusDraft, to: EventStatusLocked},
		{name: "cannot close an open event", from: EventStatusOpen, to: EventStatusClosed},
		{name: "cannot reopen an archived event directly", from: EventStatusArchived, to: EventStatusOpen},
		{name: "cannot transition to an unspecified status", from: EventStatusDraft, to: ""},
		{name: "cannot transition from an unknown status", from: "unknown", to: EventStatusOpen},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := Event{status: tt.from}

			_, err := event.TransitionTo(tt.to)
			if !errors.Is(err, ErrInvalidEventStatusTransition) {
				t.Errorf("TransitionTo(%q) error = %v, want %v", tt.to, err, ErrInvalidEventStatusTransition)
			}

			if got := event.Status(); got != tt.from {
				t.Errorf("original event status = %q, want %q", got, tt.from)
			}
		})
	}
}
