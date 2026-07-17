package domain

import "errors"

const (
	EventStatusDraft    = "draft"
	EventStatusOpen     = "open"
	EventStatusLocked   = "locked"
	EventStatusClosed   = "closed"
	EventStatusArchived = "archived"
)

var ErrInvalidEventStatusTransition = errors.New("invalid event status transition")

// Event is an immutable event model.
type Event struct {
	id             string
	publicID       string
	tenantID       string
	tenantPublicID string
	name           string
	eventType      string
	status         string
}

func NewEvent(id, publicID, tenantID, tenantPublicID, name, eventType string) Event {
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
func (e Event) Type() string           { return e.eventType }
func (e Event) Status() string         { return e.status }

// TransitionTo returns a copy of the event in the requested status. The
// lifecycle permits its normal progression and the documented recovery paths.
func (e Event) TransitionTo(status string) (Event, error) {
	if !canTransitionEventStatus(e.status, status) {
		return Event{}, ErrInvalidEventStatusTransition
	}

	e.status = status

	return e, nil
}

func canTransitionEventStatus(from, to string) bool {
	switch from {
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
