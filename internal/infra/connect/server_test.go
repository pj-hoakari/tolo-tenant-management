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

func TestTenantServiceAuthorizationAndSkeleton(t *testing.T) {
	t.Parallel()

	relationTransport := &relationTransportSpy{}
	registerTenant := application.NewTenantService(
		infra.NewInMemoryTenantRepository(),
		infra.NewRelationServiceWithTransport(relationTransport),
	)
	httpServer := httptest.NewServer(NewHandler(registerTenant))
	t.Cleanup(httpServer.Close)
	client := tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)

	t.Run("rejects missing bearer token", func(t *testing.T) {
		t.Parallel()

		_, err := client.RegisterTenant(context.Background(), connectrpc.NewRequest(&tenantv1.RegisterTenantRequest{Name: "Acme"}))
		if connectrpc.CodeOf(err) != connectrpc.CodeUnauthenticated {
			t.Fatalf("RegisterTenant() error code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeUnauthenticated)
		}
	})

	t.Run("registers tenant in the repository", func(t *testing.T) {
		t.Parallel()

		req := connectrpc.NewRequest(&tenantv1.RegisterTenantRequest{Name: "Acme", ContractPlan: "standard"})
		req.Header().Set("Authorization", exampleTenantAuthorizationHeader())

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
		if got, want := authorization, exampleTenantAuthorizationHeader(); got != want {
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
		t.Parallel()

		req := connectrpc.NewRequest(&tenantv1.RegisterTenantRequest{ContractPlan: "standard"})
		req.Header().Set("Authorization", exampleTenantAuthorizationHeader())

		_, err := client.RegisterTenant(context.Background(), req)
		if connectrpc.CodeOf(err) != connectrpc.CodeInvalidArgument {
			t.Fatalf("RegisterTenant() error code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument)
		}
	})
}

func TestRegisterTenantRejectsDuplicateName(t *testing.T) {
	tenantRepository := infra.NewInMemoryTenantRepository()
	registerTenant := application.NewTenantService(tenantRepository, infra.NewRelationService())
	httpServer := httptest.NewServer(NewHandler(registerTenant))
	t.Cleanup(httpServer.Close)
	client := tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)

	register := func(t *testing.T) error {
		t.Helper()

		req := connectrpc.NewRequest(&tenantv1.RegisterTenantRequest{Name: "Acme", ContractPlan: "standard"})
		req.Header().Set("Authorization", exampleTenantAuthorizationHeader())
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

func TestCreateEvent(t *testing.T) {
	tenantRepository := infra.NewInMemoryTenantRepository()
	tenantService := application.NewTenantService(tenantRepository, infra.NewRelationService())
	httpServer := httptest.NewServer(NewHandler(tenantService))
	t.Cleanup(httpServer.Close)
	client := tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)

	registerRequest := connectrpc.NewRequest(&tenantv1.RegisterTenantRequest{
		Name:         "Event Host",
		ContractPlan: "standard",
	})
	registerRequest.Header().Set("Authorization", exampleTenantAuthorizationHeader())

	registeredTenant, err := client.RegisterTenant(context.Background(), registerRequest)
	if err != nil {
		t.Fatalf("RegisterTenant() error = %v", err)
	}

	createRequest := connectrpc.NewRequest(&tenantv1.CreateEventRequest{
		TenantPublicId: registeredTenant.Msg.GetTenant().GetTenantPublicId(),
		Name:           "Summer Festival",
		Type:           tenantv1.EventType_EVENT_TYPE_SHORT_TERM,
	})
	createRequest.Header().Set("Authorization", exampleTenantAuthorizationHeader())

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

func TestCreateEventRejectsUnknownTenant(t *testing.T) {
	tenantService := application.NewTenantService(infra.NewInMemoryTenantRepository(), infra.NewRelationService())
	httpServer := httptest.NewServer(NewHandler(tenantService))
	t.Cleanup(httpServer.Close)
	client := tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)

	req := connectrpc.NewRequest(&tenantv1.CreateEventRequest{
		TenantPublicId: "0000000000000000",
		Name:           "Summer Festival",
	})
	req.Header().Set("Authorization", exampleTenantAuthorizationHeader())

	_, err := client.CreateEvent(context.Background(), req)
	if connectrpc.CodeOf(err) != connectrpc.CodeNotFound {
		t.Fatalf("CreateEvent() error code = %v, want %v", connectrpc.CodeOf(err), connectrpc.CodeNotFound)
	}
}
