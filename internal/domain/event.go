package domain

import "errors"

//go:generate go tool stringer -type=EventType,EventStatus -trimprefix=Event -linecomment -output=event_enum_string.go

const (
	EventTypeUnspecified EventType = iota // unspecified
	EventTypeShortTerm                    // short_term
	EventTypeLongTerm                     // long_term
)

const (
	EventStatusUnspecified EventStatus = iota // unspecified
	EventStatusDraft                          // draft
	EventStatusOpen                           // open
	EventStatusLocked                         // locked
	EventStatusClosed                         // closed
	EventStatusArchived                       // archived
)

var ErrInvalidEventStatusTransition = errors.New("invalid event status transition")

// EventType distinguishes short-lived events from long-lived installations.
type EventType uint8

// EventStatus is the lifecycle state of an event.
type EventStatus uint8

// Event is an immutable event model.
type Event struct {
	id             string
	publicID       string
	tenantID       string
	tenantPublicID string
	name           string
	eventType      EventType
	status         EventStatus
}

func NewEvent(id, publicID, tenantID, tenantPublicID, name string, eventType EventType) Event {
	return Event{
		id:             id,
		publicID:       publicID,
		tenantID:       tenantID,
		tenantPublicID: tenantPublicID,
		name:           name,
		eventType:      eventType,
		status:         EventStatusDraft,
	}
}

func (e Event) ID() string             { return e.id }
func (e Event) PublicID() string       { return e.publicID }
func (e Event) TenantID() string       { return e.tenantID }
func (e Event) TenantPublicID() string { return e.tenantPublicID }
func (e Event) Name() string           { return e.name }
func (e Event) Type() EventType        { return e.eventType }
func (e Event) Status() EventStatus    { return e.status }

// AssignType returns a copy of the event with its type updated.
func (e Event) AssignType(eventType EventType) Event {
	e.eventType = eventType

	return e
}

// TransitionTo returns a copy of the event in the requested status. The
// lifecycle permits its normal progression and the documented recovery paths.
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
		return to == EventStatusOpen
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
