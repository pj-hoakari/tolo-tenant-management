package connect

import (
	"testing"

	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
)

func TestEventStatusConversions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status tenantv1.EventStatus
		want   string
	}{
		{name: "unspecified", status: tenantv1.EventStatus_EVENT_STATUS_UNSPECIFIED, want: ""},
		{name: "draft", status: tenantv1.EventStatus_EVENT_STATUS_DRAFT, want: domain.EventStatusDraft},
		{name: "open", status: tenantv1.EventStatus_EVENT_STATUS_OPEN, want: domain.EventStatusOpen},
		{name: "locked", status: tenantv1.EventStatus_EVENT_STATUS_LOCKED, want: domain.EventStatusLocked},
		{name: "closed", status: tenantv1.EventStatus_EVENT_STATUS_CLOSED, want: domain.EventStatusClosed},
		{name: "archived", status: tenantv1.EventStatus_EVENT_STATUS_ARCHIVED, want: domain.EventStatusArchived},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := eventStatusString(tt.status); got != tt.want {
				t.Errorf("eventStatusString(%v) = %q, want %q", tt.status, got, tt.want)
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
		"short_term",
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
