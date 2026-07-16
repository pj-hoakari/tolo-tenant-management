package domain

const EventStatusDraft = "draft"

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
