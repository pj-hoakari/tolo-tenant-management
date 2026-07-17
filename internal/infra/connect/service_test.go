package connect

import (
	"context"
	"testing"

	connectrpc "connectrpc.com/connect"
	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/infra"
)

func TestEventStatusConversions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status tenantv1.EventStatus
		want   domain.EventStatus
	}{
		{name: "unspecified", status: tenantv1.EventStatus_EVENT_STATUS_UNSPECIFIED, want: domain.EventStatusUnspecified},
		{name: "draft", status: tenantv1.EventStatus_EVENT_STATUS_DRAFT, want: domain.EventStatusDraft},
		{name: "open", status: tenantv1.EventStatus_EVENT_STATUS_OPEN, want: domain.EventStatusOpen},
		{name: "locked", status: tenantv1.EventStatus_EVENT_STATUS_LOCKED, want: domain.EventStatusLocked},
		{name: "closed", status: tenantv1.EventStatus_EVENT_STATUS_CLOSED, want: domain.EventStatusClosed},
		{name: "archived", status: tenantv1.EventStatus_EVENT_STATUS_ARCHIVED, want: domain.EventStatusArchived},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := eventStatusDomain(tt.status); got != tt.want {
				t.Errorf("eventStatusDomain(%v) = %v, want %v", tt.status, got, tt.want)
			}

			if got := eventStatusProto(tt.want); got != tt.status {
				t.Errorf("eventStatusProto(%q) = %v, want %v", tt.want, got, tt.status)
			}
		})
	}
}

func TestEventProto(t *testing.T) {
	t.Parallel()

	event := domain.NewEvent(
		"event-id",
		"event-public-id",
		"tenant-id",
		"tenant-public-id",
		"Festival",
		domain.EventTypeShortTerm,
		domain.EventStatusDraft,
	)

	openEvent, err := event.TransitionTo(domain.EventStatusOpen)
	if err != nil {
		t.Fatalf("TransitionTo() error = %v", err)
	}

	got := eventProto(openEvent)
	if got.GetObservationSettings() != nil {
		t.Errorf("ObservationSettings = %#v, want nil", got.GetObservationSettings())
	}

	if got, want := got.GetEventId(), openEvent.ID(); got != want {
		t.Errorf("EventId = %q, want %q", got, want)
	}

	if got, want := got.GetTenantId(), openEvent.TenantID(); got != want {
		t.Errorf("TenantId = %q, want %q", got, want)
	}

	if got, want := got.GetStatus(), tenantv1.EventStatus_EVENT_STATUS_OPEN; got != want {
		t.Errorf("Status = %v, want %v", got, want)
	}
}

func TestGetEvent(t *testing.T) {
	t.Parallel()

	service, _, events := newReadService(t)

	response, err := service.GetEvent(context.Background(), connectrpc.NewRequest(&tenantv1.GetEventRequest{EventId: events[0].ID()}))
	if err != nil {
		t.Fatalf("GetEvent() error = %v", err)
	}

	if got, want := response.Msg.GetEvent().GetEventId(), events[0].ID(); got != want {
		t.Errorf("EventId = %q, want %q", got, want)
	}
}

func TestAssignEventType(t *testing.T) {
	t.Parallel()

	service, _, events := newReadService(t)

	response, err := service.AssignEventType(context.Background(), connectrpc.NewRequest(&tenantv1.AssignEventTypeRequest{
		EventId: events[0].ID(),
		Type:    tenantv1.EventType_EVENT_TYPE_LONG_TERM,
	}))
	if err != nil {
		t.Fatalf("AssignEventType() error = %v", err)
	}

	if got, want := response.Msg.GetEvent().GetType(), tenantv1.EventType_EVENT_TYPE_LONG_TERM; got != want {
		t.Errorf("Event.Type = %v, want %v", got, want)
	}
}

func TestListEvents(t *testing.T) {
	t.Parallel()

	service, tenant, events := newReadService(t)

	response, err := service.ListEvents(context.Background(), connectrpc.NewRequest(&tenantv1.ListEventsRequest{TenantId: tenant.ID()}))
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}

	if got, want := len(response.Msg.GetEvents()), len(events); got != want {
		t.Fatalf("event count = %d, want %d", got, want)
	}

	for i, event := range events {
		if got, want := response.Msg.GetEvents()[i].GetEventId(), event.ID(); got != want {
			t.Errorf("Events[%d].EventId = %q, want %q", i, got, want)
		}
	}
}

func newReadService(t *testing.T) (*Service, domain.Tenant, []domain.Event) {
	t.Helper()

	repository := infra.NewInMemoryTenantRepository()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", false)
	if err := repository.CreateTenant(context.Background(), tenant); err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}

	events := []domain.Event{
		domain.NewEvent("event-1", "event-public-id-1", tenant.ID(), tenant.PublicID(), "Festival 1", domain.EventTypeShortTerm, domain.EventStatusDraft),
		domain.NewEvent("event-2", "event-public-id-2", tenant.ID(), tenant.PublicID(), "Festival 2", domain.EventTypeLongTerm, domain.EventStatusDraft),
	}
	for _, event := range events {
		if err := repository.CreateEvent(context.Background(), event); err != nil {
			t.Fatalf("CreateEvent() error = %v", err)
		}
	}

	return NewService(application.NewTenantService(repository, infra.NewRelationService())), tenant, events
}
