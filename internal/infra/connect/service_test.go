//go:generate go tool mockgen -source=../../repository/tenant.go -destination=mock_tenant_repository_test.go -package=connect

package connect

import (
	"context"
	"testing"

	connectrpc "connectrpc.com/connect"
	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
	"go.uber.org/mock/gomock"
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

	// The wire representation carries public IDs only.
	if got, want := got.GetEventId(), openEvent.PublicID(); got != want {
		t.Errorf("EventId = %q, want %q", got, want)
	}

	if got, want := got.GetTenantId(), openEvent.TenantPublicID(); got != want {
		t.Errorf("TenantId = %q, want %q", got, want)
	}

	if got, want := got.GetStatus(), tenantv1.EventStatus_EVENT_STATUS_OPEN; got != want {
		t.Errorf("Status = %v, want %v", got, want)
	}
}

func TestGetEvent(t *testing.T) {
	t.Parallel()

	service, _, events := newReadService(t)

	response, err := service.GetEvent(context.Background(), connectrpc.NewRequest(&tenantv1.GetEventRequest{EventId: events[0].PublicID()}))
	if err != nil {
		t.Fatalf("GetEvent() error = %v", err)
	}

	if got, want := response.Msg.GetEvent().GetEventId(), events[0].PublicID(); got != want {
		t.Errorf("EventId = %q, want %q", got, want)
	}
}

func TestAssignEventType(t *testing.T) {
	t.Parallel()

	service, _, events := newReadService(t)

	ctx := tenantctx.WithTenantPublicID(context.Background(), events[0].TenantPublicID())

	response, err := service.AssignEventType(ctx, connectrpc.NewRequest(&tenantv1.AssignEventTypeRequest{
		EventId: events[0].PublicID(),
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

	ctx := tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID())

	response, err := service.ListEvents(ctx, connectrpc.NewRequest(&tenantv1.ListEventsRequest{TenantId: tenant.PublicID()}))
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}

	if got, want := len(response.Msg.GetEvents()), len(events); got != want {
		t.Fatalf("event count = %d, want %d", got, want)
	}

	for i, event := range events {
		if got, want := response.Msg.GetEvents()[i].GetEventId(), event.PublicID(); got != want {
			t.Errorf("Events[%d].EventId = %q, want %q", i, got, want)
		}
	}
}

func newReadService(t *testing.T) (*Service, domain.Tenant, []domain.Event) {
	t.Helper()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", false)
	events := []domain.Event{
		domain.NewEvent("event-1", "event-public-id-1", tenant.ID(), tenant.PublicID(), "Festival 1", domain.EventTypeShortTerm, domain.EventStatusDraft),
		domain.NewEvent("event-2", "event-public-id-2", tenant.ID(), tenant.PublicID(), "Festival 2", domain.EventTypeLongTerm, domain.EventStatusDraft),
	}

	ctrl := gomock.NewController(t)
	repository := NewMockTenantRepository(ctrl)
	repository.EXPECT().FindEventByPublicID(gomock.Any(), events[0].PublicID()).Return(events[0], nil).AnyTimes()
	repository.EXPECT().FindTenantByPublicID(gomock.Any(), tenant.PublicID()).Return(tenant, nil).AnyTimes()
	repository.EXPECT().ListEventsByTenantID(gomock.Any(), tenant.ID()).Return(events, nil).AnyTimes()
	repository.EXPECT().UpdateEvent(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	return NewService(application.NewTenantService(repository)), tenant, events
}
