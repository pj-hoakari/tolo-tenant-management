package connect

import (
	"context"
	"errors"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	"github.com/google/uuid"

	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/infra/db"
	relationdomain "github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	relationdb "github.com/pj-hoakari/tolo-tenant-management/internal/relation/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
)

var publicIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

type transportFixture struct {
	repository  *db.PostgresTenantRepository
	memberships *membershipRecorder
	jwks        *jwksRegistry
	client      tenantv1connect.TenantServiceClient
}

func newTransportFixture(t *testing.T, options ...application.Option) transportFixture {
	t.Helper()

	repository := newIntegrationTenantRepository(t)
	memberships := &membershipRecorder{}
	handler, jwks := newDynamicTestHandler(t, application.NewTenantService(repository, repository, memberships, options...))
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	return transportFixture{
		repository:  repository,
		memberships: memberships,
		jwks:        jwks,
		client:      tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL),
	}
}

// createTenant stores an owned tenant directly, standing in for the onboarding
// flow that is not part of this transport's responsibilities.
func (f transportFixture) createTenant(t *testing.T, publicID, name string) domain.Tenant {
	t.Helper()

	tenant := domain.NewTenant(uuid.Must(uuid.NewV7()).String(), publicID, name, "standard", domain.TenantOwnershipStateOwned, false)
	if err := f.repository.CreateTenant(context.Background(), tenant); err != nil {
		t.Fatalf("CreateTenant(%q) error = %v", name, err)
	}

	return tenant
}

func (f transportFixture) createEvent(t *testing.T, token string, tenantPublicID, name string) *tenantv1.Event {
	t.Helper()

	req := connectrpc.NewRequest(&tenantv1.CreateEventRequest{TenantId: tenantPublicID, Name: name})
	req.Header().Set("Authorization", token)

	res, err := f.client.CreateEvent(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateEvent(%q) error = %v", name, err)
	}

	return res.Msg.GetEvent()
}

var claimTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func TestStartTenantRegistrationOverTransport(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	fixture := newTransportFixture(t, application.WithClock(func() time.Time { return now }))

	// The public RPC is served without a bearer token.
	res, err := fixture.client.StartTenantRegistration(context.Background(), connectrpc.NewRequest(&tenantv1.StartTenantRegistrationRequest{Name: "Acme", ContractPlan: "standard"}))
	if err != nil {
		t.Fatalf("StartTenantRegistration() error = %v", err)
	}

	tenant := res.Msg.GetTenant()
	if got, want := tenant.GetOwnershipState(), tenantv1.TenantOwnershipState_TENANT_OWNERSHIP_STATE_PENDING_OWNER; got != want {
		t.Errorf("Tenant.OwnershipState = %v, want %v", got, want)
	}

	if got := tenant.GetTenantId(); !publicIDPattern.MatchString(got) {
		t.Errorf("Tenant.TenantId = %q, want 16-character hex", got)
	}

	if got := res.Msg.GetOwnershipClaimToken(); !claimTokenPattern.MatchString(got) {
		t.Errorf("OwnershipClaimToken = %q, want 43-character base64url", got)
	}

	if got, want := res.Msg.GetExpiresAt().AsTime(), now.Add(application.DefaultOwnershipClaimTTL); !got.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", got, want)
	}

	t.Run("rejects duplicate names while the registration is pending", func(t *testing.T) {
		_, err := fixture.client.StartTenantRegistration(context.Background(), connectrpc.NewRequest(&tenantv1.StartTenantRegistrationRequest{Name: "Acme", ContractPlan: "standard"}))
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeAlreadyExists; got != want {
			t.Fatalf("StartTenantRegistration() error code = %v, want %v", got, want)
		}
	})

	t.Run("rejects missing required fields", func(t *testing.T) {
		_, err := fixture.client.StartTenantRegistration(context.Background(), connectrpc.NewRequest(&tenantv1.StartTenantRegistrationRequest{Name: "Acme"}))
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument; got != want {
			t.Fatalf("StartTenantRegistration() error code = %v, want %v", got, want)
		}
	})
}

func TestPendingOwnerTenantRejectsTenantScopedCalls(t *testing.T) {
	fixture := newTransportFixture(t)

	res, err := fixture.client.StartTenantRegistration(context.Background(), connectrpc.NewRequest(&tenantv1.StartTenantRegistrationRequest{Name: "Acme", ContractPlan: "standard"}))
	if err != nil {
		t.Fatalf("StartTenantRegistration() error = %v", err)
	}

	tenantID := res.Msg.GetTenant().GetTenantId()
	token := mintTenantAccessToken(t, fixture.jwks, tenantID)

	createReq := connectrpc.NewRequest(&tenantv1.CreateEventRequest{TenantId: tenantID, Name: "Festival"})
	createReq.Header().Set("Authorization", token)

	_, err = fixture.client.CreateEvent(context.Background(), createReq)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
		t.Errorf("CreateEvent() error code = %v, want %v", got, want)
	}

	listReq := connectrpc.NewRequest(&tenantv1.ListEventsRequest{TenantId: tenantID})
	listReq.Header().Set("Authorization", token)

	_, err = fixture.client.ListEvents(context.Background(), listReq)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
		t.Errorf("ListEvents() error code = %v, want %v", got, want)
	}
}

func TestStartTenantRegistrationOverTransportReleasesExpiredNames(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	fixture := newTransportFixture(t, application.WithClock(func() time.Time { return now }), application.WithOwnershipClaimTTL(time.Hour))

	first, err := fixture.client.StartTenantRegistration(context.Background(), connectrpc.NewRequest(&tenantv1.StartTenantRegistrationRequest{Name: "Acme", ContractPlan: "standard"}))
	if err != nil {
		t.Fatalf("first StartTenantRegistration() error = %v", err)
	}

	// Once the first registration expires, the next registration sweeps it
	// and the name is free again.
	now = now.Add(2 * time.Hour)

	second, err := fixture.client.StartTenantRegistration(context.Background(), connectrpc.NewRequest(&tenantv1.StartTenantRegistrationRequest{Name: "Acme", ContractPlan: "standard"}))
	if err != nil {
		t.Fatalf("second StartTenantRegistration() error = %v", err)
	}

	if first.Msg.GetTenant().GetTenantId() == second.Msg.GetTenant().GetTenantId() {
		t.Error("second registration reused the expired tenant instead of creating a new one")
	}

	if _, err := fixture.repository.FindTenantByPublicID(context.Background(), first.Msg.GetTenant().GetTenantId()); !errors.Is(err, repository.ErrTenantNotFound) {
		t.Errorf("expired tenant lookup error = %v, want %v", err, repository.ErrTenantNotFound)
	}
}

func (f transportFixture) startRegistration(t *testing.T, name string) *tenantv1.StartTenantRegistrationResponse {
	t.Helper()

	res, err := f.client.StartTenantRegistration(context.Background(), connectrpc.NewRequest(&tenantv1.StartTenantRegistrationRequest{Name: name, ContractPlan: "standard"}))
	if err != nil {
		t.Fatalf("StartTenantRegistration(%q) error = %v", name, err)
	}

	return res.Msg
}

func (f transportFixture) claim(t *testing.T, token, tenantID, claimToken string) (*tenantv1.Tenant, error) {
	t.Helper()

	req := connectrpc.NewRequest(&tenantv1.ClaimTenantOwnershipRequest{TenantId: tenantID, OwnershipClaimToken: claimToken})
	if token != "" {
		req.Header().Set("Authorization", token)
	}

	res, err := f.client.ClaimTenantOwnership(context.Background(), req)
	if err != nil {
		return nil, err
	}

	return res.Msg.GetTenant(), nil
}

func TestClaimTenantOwnershipOverTransport(t *testing.T) {
	fixture := newTransportFixture(t)
	registration := fixture.startRegistration(t, "Acme")
	tenantID := registration.GetTenant().GetTenantId()

	owned, err := fixture.claim(t, internalJWTs(t).registration, tenantID, registration.GetOwnershipClaimToken())
	if err != nil {
		t.Fatalf("ClaimTenantOwnership() error = %v", err)
	}

	if got, want := owned.GetOwnershipState(), tenantv1.TenantOwnershipState_TENANT_OWNERSHIP_STATE_OWNED; got != want {
		t.Errorf("Tenant.OwnershipState = %v, want %v", got, want)
	}

	stored, err := fixture.repository.FindTenantByPublicID(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("FindTenantByPublicID() error = %v", err)
	}

	// The owner membership is recorded for the claiming subject and the
	// tenant's internal ID.
	if gotTenant, gotUser, calls := fixture.memberships.owner(); gotTenant != stored.ID() || gotUser != "test-subject" || calls != 1 {
		t.Errorf("owner membership = (%q, %q) x%d, want (%q, %q) x1", gotTenant, gotUser, calls, stored.ID(), "test-subject")
	}

	t.Run("the owned tenant accepts tenant-scoped calls", func(t *testing.T) {
		fixture.createEvent(t, mintTenantAccessToken(t, fixture.jwks, tenantID), tenantID, "Festival")
	})

	t.Run("the claim token is consumed", func(t *testing.T) {
		_, err := fixture.claim(t, internalJWTs(t).registration, tenantID, registration.GetOwnershipClaimToken())
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnauthenticated; got != want {
			t.Fatalf("second ClaimTenantOwnership() error code = %v, want %v", got, want)
		}
	})
}

// TestClaimTenantOwnershipRecordsOwnerMembership wires the real membership
// repository, as the server does, and checks that claiming ownership leaves an
// owner membership for the claiming subject.
func TestClaimTenantOwnershipRecordsOwnerMembership(t *testing.T) {
	repository := newIntegrationTenantRepository(t)
	memberships := relationdb.NewPostgresMembershipRepository(integrationDB)
	handler, jwks := newDynamicTestHandler(t, application.NewTenantService(repository, repository, memberships))
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	fixture := transportFixture{repository: repository, memberships: nil, jwks: jwks, client: tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL)}

	registration := fixture.startRegistration(t, "Acme")
	tenantID := registration.GetTenant().GetTenantId()

	if _, err := fixture.claim(t, internalJWTs(t).registration, tenantID, registration.GetOwnershipClaimToken()); err != nil {
		t.Fatalf("ClaimTenantOwnership() error = %v", err)
	}

	stored, err := repository.FindTenantByPublicID(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("FindTenantByPublicID() error = %v", err)
	}

	membership, err := memberships.FindMembership(context.Background(), stored.ID(), "test-subject")
	if err != nil {
		t.Fatalf("FindMembership() error = %v", err)
	}

	if got, want := membership.TenantRole(), relationdomain.RoleOwner; got != want {
		t.Errorf("TenantRole() = %v, want %v", got, want)
	}

	if got, want := membership.TenantPublicID(), tenantID; got != want {
		t.Errorf("TenantPublicID() = %q, want %q", got, want)
	}
}

func TestClaimTenantOwnershipOverTransportRejections(t *testing.T) {
	fixture := newTransportFixture(t)
	registration := fixture.startRegistration(t, "Acme")
	tenantID := registration.GetTenant().GetTenantId()
	claimToken := registration.GetOwnershipClaimToken()

	tests := []struct {
		name       string
		token      string
		tenantID   string
		claimToken string
		want       connectrpc.Code
	}{
		{name: "missing bearer token", token: "", tenantID: tenantID, claimToken: claimToken, want: connectrpc.CodeUnauthenticated},
		{name: "tenant_access token", token: internalJWTs(t).tenantAccess, tenantID: tenantID, claimToken: claimToken, want: connectrpc.CodeUnauthenticated},
		{name: "wrong claim token", token: internalJWTs(t).registration, tenantID: tenantID, claimToken: claimToken + "x", want: connectrpc.CodeUnauthenticated},
		{name: "unknown tenant", token: internalJWTs(t).registration, tenantID: "0000000000000000", claimToken: claimToken, want: connectrpc.CodeNotFound},
		{name: "missing claim token", token: internalJWTs(t).registration, tenantID: tenantID, claimToken: "", want: connectrpc.CodeInvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fixture.claim(t, tt.token, tt.tenantID, tt.claimToken)
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("ClaimTenantOwnership() error code = %v, want %v", got, tt.want)
			}
		})
	}

	// None of the rejections consumed the token: the valid claim still works.
	if _, err := fixture.claim(t, internalJWTs(t).registration, tenantID, claimToken); err != nil {
		t.Fatalf("ClaimTenantOwnership() after rejections error = %v", err)
	}
}

func TestClaimTenantOwnershipOverTransportRollsBackWhenMembershipFails(t *testing.T) {
	fixture := newTransportFixture(t)
	registration := fixture.startRegistration(t, "Acme")
	tenantID := registration.GetTenant().GetTenantId()

	fixture.memberships.mu.Lock()
	fixture.memberships.err = errors.New("membership store down")
	fixture.memberships.mu.Unlock()

	_, err := fixture.claim(t, internalJWTs(t).registration, tenantID, registration.GetOwnershipClaimToken())
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeInternal; got != want {
		t.Fatalf("ClaimTenantOwnership() error code = %v, want %v", got, want)
	}

	stored, err := fixture.repository.FindTenantByPublicID(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("FindTenantByPublicID() error = %v", err)
	}

	if got, want := stored.OwnershipState(), domain.TenantOwnershipStatePendingOwner; got != want {
		t.Fatalf("OwnershipState() after failed claim = %v, want %v", got, want)
	}

	// Once the membership store is back, the same token completes the claim.
	fixture.memberships.mu.Lock()
	fixture.memberships.err = nil
	fixture.memberships.mu.Unlock()

	if _, err := fixture.claim(t, internalJWTs(t).registration, tenantID, registration.GetOwnershipClaimToken()); err != nil {
		t.Fatalf("ClaimTenantOwnership() after recovery error = %v", err)
	}
}

func TestCreateEventOverTransport(t *testing.T) {
	fixture := newTransportFixture(t)
	tenant := fixture.createTenant(t, "0123456789abcdef", "Event Host")
	token := mintTenantAccessToken(t, fixture.jwks, tenant.PublicID())

	req := connectrpc.NewRequest(&tenantv1.CreateEventRequest{
		TenantId: tenant.PublicID(),
		Name:     "Summer Festival",
		Type:     tenantv1.EventType_EVENT_TYPE_SHORT_TERM,
	})
	req.Header().Set("Authorization", token)

	res, err := fixture.client.CreateEvent(context.Background(), req)
	if err != nil {
		t.Fatalf("CreateEvent() error = %v", err)
	}

	event := res.Msg.GetEvent()
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

func TestCreateEventOverTransportRejectsInvalidTenantSelection(t *testing.T) {
	fixture := newTransportFixture(t)
	tenant := fixture.createTenant(t, "0123456789abcdef", "Own Tenant")
	other := fixture.createTenant(t, "fedcba9876543210", "Other Tenant")
	token := mintTenantAccessToken(t, fixture.jwks, tenant.PublicID())

	tests := []struct {
		name     string
		tenantID string
		token    string
		want     connectrpc.Code
	}{
		{name: "missing tenant_id", tenantID: "", token: token, want: connectrpc.CodeInvalidArgument},
		{name: "tenant_id of another tenant", tenantID: other.PublicID(), token: token, want: connectrpc.CodePermissionDenied},
		{name: "tenant_id of a nonexistent tenant", tenantID: "0000000000000000", token: token, want: connectrpc.CodePermissionDenied},
		{name: "token for a nonexistent tenant", tenantID: "0000000000000000", token: mintTenantAccessToken(t, fixture.jwks, "0000000000000000"), want: connectrpc.CodeNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := connectrpc.NewRequest(&tenantv1.CreateEventRequest{TenantId: tt.tenantID, Name: "Festival"})
			req.Header().Set("Authorization", tt.token)

			_, err := fixture.client.CreateEvent(context.Background(), req)
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("CreateEvent() error code = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListEventsOverTransport(t *testing.T) {
	fixture := newTransportFixture(t)
	tenant := fixture.createTenant(t, "0123456789abcdef", "List Host")
	other := fixture.createTenant(t, "fedcba9876543210", "Other Tenant")
	token := mintTenantAccessToken(t, fixture.jwks, tenant.PublicID())
	otherToken := mintTenantAccessToken(t, fixture.jwks, other.PublicID())

	first := fixture.createEvent(t, token, tenant.PublicID(), "Festival 1")
	second := fixture.createEvent(t, token, tenant.PublicID(), "Festival 2")
	fixture.createEvent(t, otherToken, other.PublicID(), "Other Festival")

	req := connectrpc.NewRequest(&tenantv1.ListEventsRequest{TenantId: tenant.PublicID()})
	req.Header().Set("Authorization", token)

	res, err := fixture.client.ListEvents(context.Background(), req)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}

	want := []string{first.GetEventId(), second.GetEventId()}
	if got := res.Msg.GetEvents(); len(got) != len(want) {
		t.Fatalf("event count = %d, want %d", len(got), len(want))
	}

	for i, event := range res.Msg.GetEvents() {
		if got := event.GetEventId(); got != want[i] {
			t.Errorf("Events[%d].EventId = %q, want %q", i, got, want[i])
		}

		if got := event.GetTenantId(); got != tenant.PublicID() {
			t.Errorf("Events[%d].TenantId = %q, want %q", i, got, tenant.PublicID())
		}
	}

	foreign := connectrpc.NewRequest(&tenantv1.ListEventsRequest{TenantId: other.PublicID()})
	foreign.Header().Set("Authorization", token)

	_, err = fixture.client.ListEvents(context.Background(), foreign)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
		t.Fatalf("ListEvents(foreign tenant) error code = %v, want %v", got, want)
	}
}

func TestGetEventOverTransport(t *testing.T) {
	fixture := newTransportFixture(t)
	tenant := fixture.createTenant(t, "0123456789abcdef", "Get Host")
	other := fixture.createTenant(t, "fedcba9876543210", "Other Tenant")
	token := mintTenantAccessToken(t, fixture.jwks, tenant.PublicID())
	created := fixture.createEvent(t, token, tenant.PublicID(), "Festival")

	t.Run("returns the event to a service token of its tenant", func(t *testing.T) {
		req := connectrpc.NewRequest(&tenantv1.GetEventRequest{EventId: created.GetEventId()})
		req.Header().Set("Authorization", mintServiceToken(t, fixture.jwks, tenant.PublicID()))

		res, err := fixture.client.GetEvent(context.Background(), req)
		if err != nil {
			t.Fatalf("GetEvent() error = %v", err)
		}

		if got, want := res.Msg.GetEvent().GetEventId(), created.GetEventId(); got != want {
			t.Errorf("Event.EventId = %q, want %q", got, want)
		}

		if got, want := res.Msg.GetEvent().GetTenantId(), tenant.PublicID(); got != want {
			t.Errorf("Event.TenantId = %q, want %q", got, want)
		}
	})

	tests := []struct {
		name    string
		eventID string
		token   string
		want    connectrpc.Code
	}{
		{name: "rejects tenant_access token", eventID: created.GetEventId(), token: token, want: connectrpc.CodeUnauthenticated},
		{name: "rejects service token without tenant context", eventID: created.GetEventId(), token: internalJWTs(t).service, want: connectrpc.CodeUnauthenticated},
		{name: "rejects service token of another tenant", eventID: created.GetEventId(), token: mintServiceToken(t, fixture.jwks, other.PublicID()), want: connectrpc.CodePermissionDenied},
		{name: "reports unknown public IDs as not found", eventID: "0000000000000000", token: mintServiceToken(t, fixture.jwks, tenant.PublicID()), want: connectrpc.CodeNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := connectrpc.NewRequest(&tenantv1.GetEventRequest{EventId: tt.eventID})
			req.Header().Set("Authorization", tt.token)

			_, err := fixture.client.GetEvent(context.Background(), req)
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("GetEvent() error code = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetObservationSettingsOverTransportAcceptsMachineOriginServiceToken(t *testing.T) {
	fixture := newTransportFixture(t)

	// The boundary is not enforced for this read, so a service token without
	// tenant context passes authentication and reaches the (still
	// unimplemented) handler.
	req := connectrpc.NewRequest(&tenantv1.GetObservationSettingsRequest{EventId: "0000000000000000"})
	req.Header().Set("Authorization", internalJWTs(t).service)

	_, err := fixture.client.GetObservationSettings(context.Background(), req)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnimplemented; got != want {
		t.Fatalf("GetObservationSettings() error code = %v, want %v", got, want)
	}
}

func TestTransitionEventStatusOverTransport(t *testing.T) {
	fixture := newTransportFixture(t)
	tenant := fixture.createTenant(t, "0123456789abcdef", "Status Host")
	token := mintTenantAccessToken(t, fixture.jwks, tenant.PublicID())
	eventID := fixture.createEvent(t, token, tenant.PublicID(), "Status Event").GetEventId()

	transition := func(to tenantv1.EventStatus) (*tenantv1.Event, error) {
		t.Helper()

		req := connectrpc.NewRequest(&tenantv1.TransitionEventStatusRequest{EventId: eventID, To: to})
		req.Header().Set("Authorization", token)

		response, err := fixture.client.TransitionEventStatus(context.Background(), req)
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

	_, err := transition(tenantv1.EventStatus_EVENT_STATUS_LOCKED)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
		t.Errorf("invalid transition error code = %v, want %v", got, want)
	}
}

func TestTransitionEventStatusOverTransportRejectsForeignEvent(t *testing.T) {
	fixture := newTransportFixture(t)
	tenant := fixture.createTenant(t, "0123456789abcdef", "Own Tenant")
	other := fixture.createTenant(t, "fedcba9876543210", "Other Tenant")
	otherEvent := fixture.createEvent(t, mintTenantAccessToken(t, fixture.jwks, other.PublicID()), other.PublicID(), "Other Festival")

	req := connectrpc.NewRequest(&tenantv1.TransitionEventStatusRequest{
		EventId: otherEvent.GetEventId(),
		To:      tenantv1.EventStatus_EVENT_STATUS_OPEN,
	})
	req.Header().Set("Authorization", mintTenantAccessToken(t, fixture.jwks, tenant.PublicID()))

	_, err := fixture.client.TransitionEventStatus(context.Background(), req)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
		t.Fatalf("TransitionEventStatus(foreign event) error code = %v, want %v", got, want)
	}
}

func TestTransitionEventStatusOverTransportRejectsInvalidRequest(t *testing.T) {
	fixture := newTransportFixture(t)

	req := connectrpc.NewRequest(&tenantv1.TransitionEventStatusRequest{})
	req.Header().Set("Authorization", internalJWTs(t).tenantAccess)

	_, err := fixture.client.TransitionEventStatus(context.Background(), req)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument; got != want {
		t.Fatalf("TransitionEventStatus() error code = %v, want %v", got, want)
	}

	req = connectrpc.NewRequest(&tenantv1.TransitionEventStatusRequest{
		EventId: "0000000000000000",
		To:      tenantv1.EventStatus_EVENT_STATUS_OPEN,
	})
	req.Header().Set("Authorization", internalJWTs(t).tenantAccess)

	_, err = fixture.client.TransitionEventStatus(context.Background(), req)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeNotFound; got != want {
		t.Fatalf("TransitionEventStatus() error code = %v, want %v", got, want)
	}
}
