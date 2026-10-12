package domain

import (
	"errors"
	"fmt"
)

//go:generate go tool stringer -type=EventStatus -trimprefix=Event -linecomment -output=event_enum_string.go

const (
	EventStatusUnspecified EventStatus = iota // unspecified
	EventStatusDraft                          // draft
	EventStatusOpen                           // open
	EventStatusLocked                         // locked
	EventStatusClosed                         // closed
	EventStatusArchived                       // archived
)

var ErrInvalidEventStatusTransition = errors.New("invalid event status transition")

// EventStatus is the lifecycle state of an event.
type EventStatus uint8

// ParseEventStatus maps a string representation (as produced by String) back to an EventStatus.
func ParseEventStatus(s string) (EventStatus, error) {
	for st := EventStatusUnspecified; st <= EventStatusArchived; st++ {
		if st.String() == s {
			return st, nil
		}
	}

	return EventStatusUnspecified, fmt.Errorf("invalid event status %q", s)
}

// Event is an immutable event model.
type Event struct {
	id             string
	publicID       string
	tenantID       string
	tenantPublicID string
	name           string
	status         EventStatus
}

func NewEvent(id, publicID, tenantID, tenantPublicID, name string, status EventStatus) Event {
	return Event{
		id:             id,
		publicID:       publicID,
		tenantID:       tenantID,
		tenantPublicID: tenantPublicID,
		name:           name,
		status:         status,
	}
}

func (e Event) ID() string             { return e.id }
func (e Event) PublicID() string       { return e.publicID }
func (e Event) TenantID() string       { return e.tenantID }
func (e Event) TenantPublicID() string { return e.tenantPublicID }
func (e Event) Name() string           { return e.name }
func (e Event) Status() EventStatus    { return e.status }

// TransitionTo returns a copy of the event in the requested status. The
// lifecycle permits its normal progression, discarding a draft, and the
// documented recovery paths.
func (e Event) TransitionTo(status EventStatus) (Event, error) {
	if !canTransitionEventStatus(e.status, status) {
		return Event{}, ErrInvalidEventStatusTransition
	}

	e.status = status

	return e, nil
}

func canTransitionEventStatus(from, to EventStatus) bool {
	switch from {
	case EventStatusUnspecified:
		return false
	case EventStatusDraft:
		// Archiving a draft discards it; archived events come back as closed,
		// never as drafts.
		return to == EventStatusOpen || to == EventStatusArchived
	case EventStatusOpen:
		return to == EventStatusLocked
	case EventStatusLocked:
		return to == EventStatusClosed || to == EventStatusOpen
	case EventStatusClosed:
		return to == EventStatusArchived || to == EventStatusOpen
	case EventStatusArchived:
		return to == EventStatusClosed
	default:
		return false
	}
}
