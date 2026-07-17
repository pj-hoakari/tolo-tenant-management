package connect

import (
	"context"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"

	connectrpc "connectrpc.com/connect"

	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/infra"
)

var (
	publicIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)
	uuidV7Pattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
)

type relationTransportSpy struct {
	mu            sync.Mutex
	authorization string
	input         application.AddTenantMemberInput
}

func (s *relationTransportSpy) AddTenantMember(_ context.Context, authorization string, input application.AddTenantMemberInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.authorization = authorization
	s.input = input

	return nil
}

func (s *relationTransportSpy) call() (string, application.AddTenantMemberInput) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.authorization, s.input
}

func TestRegisterTenantOverTransport(t *testing.T) {
	relationTransport := &relationTransportSpy{}
	tenantService := application.NewTenantService(
		newIntegrationTenantRepository(t),
		infra.NewRelationServiceWithTransport(relationTransport),
	)
	httpServer := httptest.NewServer(newTestHandler(t, tenantService))
	t.Cleanup(httpServer.Close)
	client := tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)

	t.Run("rejects missing bearer token", func(t *testing.T) {
		_, err := client.RegisterTenant(context.Background(), connectrpc.NewRequest(&tenantv1.RegisterTenantRequest{Name: "Acme"}))
		if connectrpc.CodeOf(err) != connectrpc.CodeUnauthenticated {
			t.Fatalf("RegisterTenant() error code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeUnauthenticated)
		}
	})

	t.Run("registers tenant in the repository", func(t *testing.T) {
		req := connectrpc.NewRequest(&tenantv1.RegisterTenantRequest{Name: "Acme", ContractPlan: "standard"})
		req.Header().Set("Authorization", internalJWTs(t).registration)

		res, err := client.RegisterTenant(context.Background(), req)
		if err != nil {
			t.Fatalf("RegisterTenant() error = %v", err)
		}

		if got, want := res.Msg.GetTenant().GetName(), "Acme"; got != want {
			t.Errorf("Tenant.Name = %q, want %q", got, want)
		}

		if got, want := res.Msg.GetTenant().GetContractPlan(), "standard"; got != want {
			t.Errorf("Tenant.ContractPlan = %q, want %q", got, want)
		}

		if got := res.Msg.GetTenant().GetTenantId(); !uuidV7Pattern.MatchString(got) {
			t.Errorf("Tenant.TenantId = %q, want UUIDv7", got)
		}

		if got := res.Msg.GetTenant().GetTenantPublicId(); !publicIDPattern.MatchString(got) {
			t.Errorf("Tenant.TenantPublicId = %q, want 16-character hex", got)
		}

		if res.Msg.GetTenant().GetArchived() {
			t.Error("Tenant.Archived = true, want false")
		}

		authorization, input := relationTransport.call()
		if got, want := authorization, internalJWTs(t).registration; got != want {
			t.Errorf("Relation authorization = %q, want %q", got, want)
		}

		if got := input.TenantID; !uuidV7Pattern.MatchString(got) {
			t.Errorf("Relation tenant ID = %q, want UUIDv7", got)
		}

		if got, want := input.TenantID, res.Msg.GetTenant().GetTenantId(); got != want {
			t.Errorf("Relation tenant ID = %q, want %q", got, want)
		}

		if got, want := input.Role, application.TenantOwnerRole; got != want {
			t.Errorf("Relation role = %q, want %q", got, want)
		}
	})

	t.Run("rejects missing required fields", func(t *testing.T) {
		req := connectrpc.NewRequest(&tenantv1.RegisterTenantRequest{ContractPlan: "standard"})
		req.Header().Set("Authorization", internalJWTs(t).registration)

		_, err := client.RegisterTenant(context.Background(), req)
		if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
			t.Fatalf("RegisterTenant() error code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument)
		}
	})
}

func TestRegisterTenantOverTransportRejectsDuplicateName(t *testing.T) {
	tenantRepository := newIntegrationTenantRepository(t)
	tenantService := application.NewTenantService(tenantRepository, infra.NewRelationService())
	httpServer := httptest.NewServer(newTestHandler(t, tenantService))
	t.Cleanup(httpServer.Close)
	client := tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)

	register := func(t *testing.T) error {
		t.Helper()

		req := connectrpc.NewRequest(&tenantv1.RegisterTenantRequest{Name: "Acme", ContractPlan: "standard"})
		req.Header().Set("Authorization", internalJWTs(t).registration)
		_, err := client.RegisterTenant(context.Background(), req)

		return err
	}

	if err := register(t); err != nil {
		t.Fatalf("first RegisterTenant() error = %v", err)
	}

	if err := register(t); connectrpc.CodeOf(err) != connectrpc.CodeAlreadyExists {
		t.Fatalf("second RegisterTenant() error code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeAlreadyExists)
	}
}

func TestCreateEventOverTransport(t *testing.T) {
	tenantRepository := newIntegrationTenantRepository(t)
	tenantService := application.NewTenantService(tenantRepository, infra.NewRelationService())
	httpServer := httptest.NewServer(newTestHandler(t, tenantService))
	t.Cleanup(httpServer.Close)
	client := tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)

	registerRequest := connectrpc.NewRequest(&tenantv1.RegisterTenantRequest{
		Name:         "Event Host",
		ContractPlan: "standard",
	})
	registerRequest.Header().Set("Authorization", internalJWTs(t).registration)

	registeredTenant, err := client.RegisterTenant(context.Background(), registerRequest)
	if err != nil {
		t.Fatalf("RegisterTenant() error = %v", err)
	}

	createRequest := connectrpc.NewRequest(&tenantv1.CreateEventRequest{
		TenantPublicId: registeredTenant.Msg.GetTenant().GetTenantPublicId(),
		Name:           "Summer Festival",
		Type:           tenantv1.EventType_EVENT_TYPE_SHORT_TERM,
	})
	createRequest.Header().Set("Authorization", internalJWTs(t).tenantAccess)

	eventResponse, err := client.CreateEvent(context.Background(), createRequest)
	if err != nil {
		t.Fatalf("CreateEvent() error = %v", err)
	}

	event := eventResponse.Msg.GetEvent()
	if got, want := event.GetTenantId(), registeredTenant.Msg.GetTenant().GetTenantId(); got != want {
		t.Errorf("Event.TenantId = %q, want %q", got, want)
	}

	if got, want := event.GetTenantPublicId(), registeredTenant.Msg.GetTenant().GetTenantPublicId(); got != want {
		t.Errorf("Event.TenantPublicId = %q, want %q", got, want)
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

	if got := event.GetEventId(); !uuidV7Pattern.MatchString(got) {
		t.Errorf("Event.EventId = %q, want UUIDv7", got)
	}

	if got := event.GetEventPublicId(); !publicIDPattern.MatchString(got) {
		t.Errorf("Event.EventPublicId = %q, want 16-character hex", got)
	}
}

func TestCreateEventOverTransportRejectsUnknownTenant(t *testing.T) {
	tenantService := application.NewTenantService(newIntegrationTenantRepository(t), infra.NewRelationService())
	httpServer := httptest.NewServer(newTestHandler(t, tenantService))
	t.Cleanup(httpServer.Close)
	client := tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)

	req := connectrpc.NewRequest(&tenantv1.CreateEventRequest{
		TenantPublicId: "0000000000000000",
		Name:           "Summer Festival",
	})
	req.Header().Set("Authorization", internalJWTs(t).tenantAccess)

	_, err := client.CreateEvent(context.Background(), req)
	if connectrpc.CodeOf(err) != connectrpc.CodeNotFound {
		t.Fatalf("CreateEvent() error code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeNotFound)
	}
}

func TestTransitionEventStatusOverTransport(t *testing.T) {
	tenantService := application.NewTenantService(newIntegrationTenantRepository(t), infra.NewRelationService())
	httpServer := httptest.NewServer(newTestHandler(t, tenantService))
	t.Cleanup(httpServer.Close)
	client := tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)

	registerRequest := connectrpc.NewRequest(&tenantv1.RegisterTenantRequest{
		Name:         "Status Host",
		ContractPlan: "standard",
	})
	registerRequest.Header().Set("Authorization", internalJWTs(t).registration)

	tenantResponse, err := client.RegisterTenant(context.Background(), registerRequest)
	if err != nil {
		t.Fatalf("RegisterTenant() error = %v", err)
	}

	createRequest := connectrpc.NewRequest(&tenantv1.CreateEventRequest{
		TenantPublicId: tenantResponse.Msg.GetTenant().GetTenantPublicId(),
		Name:           "Status Event",
	})
	createRequest.Header().Set("Authorization", internalJWTs(t).tenantAccess)

	eventResponse, err := client.CreateEvent(context.Background(), createRequest)
	if err != nil {
		t.Fatalf("CreateEvent() error = %v", err)
	}

	eventID := eventResponse.Msg.GetEvent().GetEventId()

	transition := func(to tenantv1.EventStatus) (*tenantv1.Event, error) {
		t.Helper()

		req := connectrpc.NewRequest(&tenantv1.TransitionEventStatusRequest{EventId: eventID, To: to})
		req.Header().Set("Authorization", internalJWTs(t).tenantAccess)

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
	tenantService := application.NewTenantService(newIntegrationTenantRepository(t), infra.NewRelationService())
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
		EventId: "0197f1ce-cad0-7f00-8000-000000000000",
		To:      tenantv1.EventStatus_EVENT_STATUS_OPEN,
	})
	req.Header().Set("Authorization", internalJWTs(t).tenantAccess)

	_, err = client.TransitionEventStatus(context.Background(), req)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeNotFound; got != want {
		t.Fatalf("TransitionEventStatus() error code = %v, want %v", got, want)
	}
}
