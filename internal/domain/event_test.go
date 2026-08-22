package domain

import (
	"errors"
	"fmt"
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
		EventStatusDraft,
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

// eventStatusTransition is one (from, to) pair of the status transition table
// in docs/tenant_management_spec.md.
type eventStatusTransition struct {
	from EventStatus
	to   EventStatus
}

func TestEventTransitionTo(t *testing.T) {
	t.Parallel()

	// The spec allows exactly these eight transitions. Every other pair,
	// including a transition to the same status, must be rejected.
	allowed := make(map[eventStatusTransition]bool)
	for _, transition := range []eventStatusTransition{
		{from: EventStatusDraft, to: EventStatusOpen},
		{from: EventStatusDraft, to: EventStatusArchived},
		{from: EventStatusOpen, to: EventStatusLocked},
		{from: EventStatusLocked, to: EventStatusOpen},
		{from: EventStatusLocked, to: EventStatusClosed},
		{from: EventStatusClosed, to: EventStatusOpen},
		{from: EventStatusClosed, to: EventStatusArchived},
		{from: EventStatusArchived, to: EventStatusClosed},
	} {
		allowed[transition] = true
	}

	statuses := []EventStatus{
		EventStatusUnspecified,
		EventStatusDraft,
		EventStatusOpen,
		EventStatusLocked,
		EventStatusClosed,
		EventStatusArchived,
	}

	for _, from := range statuses {
		for _, to := range statuses {
			wantAllowed := allowed[eventStatusTransition{from: from, to: to}]

			t.Run(fmt.Sprintf("%s to %s", from, to), func(t *testing.T) {
				t.Parallel()

				event := Event{status: from}

				updatedEvent, err := event.TransitionTo(to)

				switch {
				case wantAllowed && err != nil:
					t.Fatalf("TransitionTo(%q) error = %v, want success", to, err)
				case wantAllowed:
					if got := updatedEvent.Status(); got != to {
						t.Errorf("updated event status = %q, want %q", got, to)
					}
				case !errors.Is(err, ErrInvalidEventStatusTransition):
					t.Fatalf("TransitionTo(%q) error = %v, want %v", to, err, ErrInvalidEventStatusTransition)
				}

				if got := event.Status(); got != from {
					t.Errorf("original event status = %q, want %q", got, from)
				}
			})
		}
	}
}

func TestEventAssignType(t *testing.T) {
	t.Parallel()

	event := NewEvent("event-id", "event-public-id", "tenant-id", "tenant-public-id", "Festival", EventTypeShortTerm, EventStatusDraft)
	updatedEvent := event.AssignType(EventTypeLongTerm)

	if got, want := updatedEvent.Type(), EventTypeLongTerm; got != want {
		t.Errorf("updated event type = %v, want %v", got, want)
	}

	if got, want := event.Type(), EventTypeShortTerm; got != want {
		t.Errorf("original event type = %v, want %v", got, want)
	}
}

func TestEventTransitionToRejectsUnknownStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		from EventStatus
		to   EventStatus
	}{
		{name: "cannot transition from an unknown status", from: EventStatus(99), to: EventStatusOpen},
		{name: "cannot transition to an unknown status", from: EventStatusDraft, to: EventStatus(99)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

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
