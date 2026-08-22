//go:generate go tool mockgen -source=../../repository/tenant.go -destination=mock_tenant_repository_test.go -package=connect

package connect

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	connectrpc "connectrpc.com/connect"
	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
	"go.uber.org/mock/gomock"
)

// passthroughTransactor runs the unit of work directly for mock-backed tests.
type passthroughTransactor struct{}

func (passthroughTransactor) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

// membershipRecorder stands in for the relation model's membership store.
type membershipRecorder struct {
	mu       sync.Mutex
	err      error
	tenantID string
	userID   string
	calls    int
}

func (r *membershipRecorder) AddOwner(_ context.Context, tenantID, userID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.calls++
	r.tenantID, r.userID = tenantID, userID

	return r.err
}

func (r *membershipRecorder) owner() (string, string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.tenantID, r.userID, r.calls
}

// permissionStub stands in for the relation side's current-permission check.
// It holds no mutable state, so the handler goroutines can share one.
type permissionStub struct {
	err     error
	allowed bool
}

func (s permissionStub) Allowed(context.Context, string, string) (bool, error) {
	return s.allowed, s.err
}

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

	ctx := tenantctx.WithTenantPublicID(context.Background(), events[0].TenantPublicID())

	response, err := service.GetEvent(ctx, connectrpc.NewRequest(&tenantv1.GetEventRequest{EventId: events[0].PublicID()}))
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

// TestCreateEventPublicIDCollision pins an event public ID collision to
// already_exists rather than internal (tenant_management_spec.md「エラー」).
func TestCreateEventPublicIDCollision(t *testing.T) {
	t.Parallel()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, false)

	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	repo.EXPECT().FindTenantByPublicID(gomock.Any(), tenant.PublicID()).Return(tenant, nil).AnyTimes()
	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).Return(repository.ErrEventPublicIDExists)

	service := NewService(application.NewTenantService(repo, passthroughTransactor{}, &membershipRecorder{}, permissionStub{allowed: true}))
	ctx := tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID())

	_, err := service.CreateEvent(ctx, connectrpc.NewRequest(&tenantv1.CreateEventRequest{
		TenantId: tenant.PublicID(),
		Name:     "Festival",
		Type:     tenantv1.EventType_EVENT_TYPE_SHORT_TERM,
	}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeAlreadyExists; got != want {
		t.Fatalf("CreateEvent() error code = %v, want %v", got, want)
	}
}

// TestInternalErrorHidesDetail keeps the cause of an internal failure out of
// the response: the client only ever sees the fixed message
// (service_gateway.md「エラー方針」).
func TestInternalErrorHidesDetail(t *testing.T) {
	t.Parallel()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, false)

	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	repo.EXPECT().FindTenantByPublicID(gomock.Any(), tenant.PublicID()).Return(domain.Tenant{}, errors.New("secret detail"))

	service := NewService(application.NewTenantService(repo, passthroughTransactor{}, &membershipRecorder{}, permissionStub{allowed: true}))
	ctx := tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID())

	_, err := service.ListEvents(ctx, connectrpc.NewRequest(&tenantv1.ListEventsRequest{TenantId: tenant.PublicID()}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeInternal; got != want {
		t.Fatalf("ListEvents() error code = %v, want %v", got, want)
	}

	var connectErr *connectrpc.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("ListEvents() error = %v, want a Connect error", err)
	}

	if got, want := connectErr.Message(), "internal error"; got != want {
		t.Errorf("Message() = %q, want %q", got, want)
	}

	if strings.Contains(err.Error(), "secret detail") {
		t.Errorf("error = %q, want it to omit the underlying failure", err)
	}
}

func newReadService(t *testing.T) (*Service, domain.Tenant, []domain.Event) {
	t.Helper()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, false)
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

	return NewService(application.NewTenantService(repository, passthroughTransactor{}, &membershipRecorder{}, permissionStub{allowed: true})), tenant, events
}
