package connect

import (
	"context"
	"net/http/httptest"
	"regexp"
	"testing"

	connectrpc "connectrpc.com/connect"
	"github.com/google/uuid"

	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/infra/db"
)

var publicIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

// createTenant stores an owned tenant directly, standing in for the onboarding
// flow that is not part of this transport's responsibilities.
func createTenant(t *testing.T, repository *db.PostgresTenantRepository, publicID, name string) domain.Tenant {
	t.Helper()

	tenant := domain.NewTenant(uuid.Must(uuid.NewV7()).String(), publicID, name, "standard", false)
	if err := repository.CreateTenant(context.Background(), tenant); err != nil {
		t.Fatalf("CreateTenant(%q) error = %v", name, err)
	}

	return tenant
}

func TestCreateEventOverTransport(t *testing.T) {
	tenantRepository := newIntegrationTenantRepository(t)
	tenantService := application.NewTenantService(tenantRepository)
	handler, jwks := newDynamicTestHandler(t, tenantService)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	client := tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)

	tenant := createTenant(t, tenantRepository, "0123456789abcdef", "Event Host")

	createRequest := connectrpc.NewRequest(&tenantv1.CreateEventRequest{
		Name: "Summer Festival",
		Type: tenantv1.EventType_EVENT_TYPE_SHORT_TERM,
	})
	createRequest.Header().Set("Authorization", mintTenantAccessToken(t, jwks, tenant.PublicID()))

	eventResponse, err := client.CreateEvent(context.Background(), createRequest)
	if err != nil {
		t.Fatalf("CreateEvent() error = %v", err)
	}

	event := eventResponse.Msg.GetEvent()
	if got, want := event.GetTenantId(), tenant.PublicID(); got != want {
		t.Errorf("Event.TenantId = %q, want %q", got, want)
	}

	if got, want := event.GetName(), "Summer Festival"; got != want {
		t.Errorf("Event.Name = %q, want %q", got, want)
	}

	if got, want := event.GetType(), tenantv1.EventType_EVENT_TYPE_SHORT_TERM; got != want {
		t.Errorf("Event.Type = %v, want %v", got, want)
	}

	if got, want := event.GetStatus(), tenantv1.EventStatus_EVENT_STATUS_DRAFT; got != want {
		t.Errorf("Event.Status = %v, want %v", got, want)
	}

	// Only the public ID is exposed; the internal UUIDv7 never leaves the
	// service.
	if got := event.GetEventId(); !publicIDPattern.MatchString(got) {
		t.Errorf("Event.EventId = %q, want 16-character hex", got)
	}
}

func TestCreateEventOverTransportRejectsUnknownTenant(t *testing.T) {
	tenantService := application.NewTenantService(newIntegrationTenantRepository(t))
	handler, jwks := newDynamicTestHandler(t, tenantService)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	client := tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)

	// The JWT tenant does not exist, so the repository lookup rejects it.
	req := connectrpc.NewRequest(&tenantv1.CreateEventRequest{
		Name: "Summer Festival",
	})
	req.Header().Set("Authorization", mintTenantAccessToken(t, jwks, "0000000000000000"))

	_, err := client.CreateEvent(context.Background(), req)
	if connectrpc.CodeOf(err) != connectrpc.CodeNotFound {
		t.Fatalf("CreateEvent() error code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeNotFound)
	}
}

func TestCreateEventOverTransportRejectsForeignTenant(t *testing.T) {
	tenantService := application.NewTenantService(newIntegrationTenantRepository(t))
	handler, jwks := newDynamicTestHandler(t, tenantService)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	client := tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)

	// The request has no tenant selector, so this token can only act as its own
	// tenant. Because that tenant does not exist, it cannot create an event.
	foreignToken := mintTenantAccessToken(t, jwks, "ffffffffffffffff")

	createRequest := connectrpc.NewRequest(&tenantv1.CreateEventRequest{
		Name: "Intruder Event",
		Type: tenantv1.EventType_EVENT_TYPE_SHORT_TERM,
	})
	createRequest.Header().Set("Authorization", foreignToken)

	_, err := client.CreateEvent(context.Background(), createRequest)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeNotFound; got != want {
		t.Fatalf("CreateEvent() error code = %v, want %v", got, want)
	}
}

func TestTransitionEventStatusOverTransport(t *testing.T) {
	tenantRepository := newIntegrationTenantRepository(t)
	tenantService := application.NewTenantService(tenantRepository)
	handler, jwks := newDynamicTestHandler(t, tenantService)
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	client := tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)

	tenant := createTenant(t, tenantRepository, "0123456789abcdef", "Status Host")
	tenantAccess := mintTenantAccessToken(t, jwks, tenant.PublicID())

	createRequest := connectrpc.NewRequest(&tenantv1.CreateEventRequest{
		Name: "Status Event",
	})
	createRequest.Header().Set("Authorization", tenantAccess)

	eventResponse, err := client.CreateEvent(context.Background(), createRequest)
	if err != nil {
		t.Fatalf("CreateEvent() error = %v", err)
	}

	eventID := eventResponse.Msg.GetEvent().GetEventId()

	transition := func(to tenantv1.EventStatus) (*tenantv1.Event, error) {
		t.Helper()

		req := connectrpc.NewRequest(&tenantv1.TransitionEventStatusRequest{EventId: eventID, To: to})
		req.Header().Set("Authorization", tenantAccess)

		response, err := client.TransitionEventStatus(context.Background(), req)
		if err != nil {
			return nil, err
		}

		return response.Msg.GetEvent(), nil
	}

	for _, want := range []tenantv1.EventStatus{
		tenantv1.EventStatus_EVENT_STATUS_OPEN,
		tenantv1.EventStatus_EVENT_STATUS_LOCKED,
		tenantv1.EventStatus_EVENT_STATUS_OPEN,
		tenantv1.EventStatus_EVENT_STATUS_LOCKED,
		tenantv1.EventStatus_EVENT_STATUS_CLOSED,
		tenantv1.EventStatus_EVENT_STATUS_OPEN,
		tenantv1.EventStatus_EVENT_STATUS_LOCKED,
		tenantv1.EventStatus_EVENT_STATUS_CLOSED,
		tenantv1.EventStatus_EVENT_STATUS_ARCHIVED,
		tenantv1.EventStatus_EVENT_STATUS_CLOSED,
	} {
		event, err := transition(want)
		if err != nil {
			t.Fatalf("TransitionEventStatus(%v) error = %v", want, err)
		}

		if got := event.GetStatus(); got != want {
			t.Errorf("Event.Status = %v, want %v", got, want)
		}
	}

	_, err = transition(tenantv1.EventStatus_EVENT_STATUS_LOCKED)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
		t.Errorf("invalid transition error code = %v, want %v", got, want)
	}
}

func TestTransitionEventStatusOverTransportRejectsInvalidRequest(t *testing.T) {
	tenantService := application.NewTenantService(newIntegrationTenantRepository(t))
	httpServer := httptest.NewServer(newTestHandler(t, tenantService))
	t.Cleanup(httpServer.Close)
	client := tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)

	req := connectrpc.NewRequest(&tenantv1.TransitionEventStatusRequest{})
	req.Header().Set("Authorization", internalJWTs(t).tenantAccess)

	_, err := client.TransitionEventStatus(context.Background(), req)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument; got != want {
		t.Fatalf("TransitionEventStatus() error code = %v, want %v", got, want)
	}

	req = connectrpc.NewRequest(&tenantv1.TransitionEventStatusRequest{
		EventId: "0000000000000000",
		To:      tenantv1.EventStatus_EVENT_STATUS_OPEN,
	})
	req.Header().Set("Authorization", internalJWTs(t).tenantAccess)

	_, err = client.TransitionEventStatus(context.Background(), req)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeNotFound; got != want {
		t.Fatalf("TransitionEventStatus() error code = %v, want %v", got, want)
	}
}
