package domain

import (
	"errors"
	"testing"
)

func TestNewEvent(t *testing.T) {
	t.Parallel()

	event := NewEvent(
		"event-id",
		"event-public-id",
		"tenant-id",
		"tenant-public-id",
		"Summer Festival",
		EventTypeShortTerm,
	)

	if got, want := event.ID(), "event-id"; got != want {
		t.Errorf("ID() = %q, want %q", got, want)
	}

	if got, want := event.PublicID(), "event-public-id"; got != want {
		t.Errorf("PublicID() = %q, want %q", got, want)
	}

	if got, want := event.TenantID(), "tenant-id"; got != want {
		t.Errorf("TenantID() = %q, want %q", got, want)
	}

	if got, want := event.TenantPublicID(), "tenant-public-id"; got != want {
		t.Errorf("TenantPublicID() = %q, want %q", got, want)
	}

	if got, want := event.Name(), "Summer Festival"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}

	if got, want := event.Type(), EventTypeShortTerm; got != want {
		t.Errorf("Type() = %v, want %v", got, want)
	}

	if got, want := event.Type().String(), "short_term"; got != want {
		t.Errorf("Type().String() = %q, want %q", got, want)
	}

	if got, want := event.Status(), EventStatusDraft; got != want {
		t.Errorf("Status() = %q, want %q", got, want)
	}

	if got, want := event.Status().String(), "draft"; got != want {
		t.Errorf("Status().String() = %q, want %q", got, want)
	}
}

func TestEventTransitionTo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from EventStatus
		to   EventStatus
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

func TestEventAssignType(t *testing.T) {
	t.Parallel()

	event := NewEvent("event-id", "event-public-id", "tenant-id", "tenant-public-id", "Festival", EventTypeShortTerm)
	updatedEvent := event.AssignType(EventTypeLongTerm)

	if got, want := updatedEvent.Type(), EventTypeLongTerm; got != want {
		t.Errorf("updated event type = %v, want %v", got, want)
	}

	if got, want := event.Type(), EventTypeShortTerm; got != want {
		t.Errorf("original event type = %v, want %v", got, want)
	}
}

func TestEventTransitionToRejectsInvalidTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from EventStatus
		to   EventStatus
	}{
		{name: "cannot lock a draft event", from: EventStatusDraft, to: EventStatusLocked},
		{name: "cannot close an open event", from: EventStatusOpen, to: EventStatusClosed},
		{name: "cannot reopen an archived event directly", from: EventStatusArchived, to: EventStatusOpen},
		{name: "cannot transition to an unspecified status", from: EventStatusDraft, to: EventStatusUnspecified},
		{name: "cannot transition from an unknown status", from: EventStatus(99), to: EventStatusOpen},
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
