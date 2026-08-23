package connect

import (
	"context"
	"errors"
	"net/http/httptest"
	"regexp"
	"slices"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"
	"github.com/google/uuid"

	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
	relationapplication "github.com/pj-hoakari/tolo-tenant-management/internal/relation/application"
	relationdomain "github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	relationdb "github.com/pj-hoakari/tolo-tenant-management/internal/relation/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/infra/db"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/repository"
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

	return newTransportFixtureWithPermissions(t, permissionStub{allowed: true}, options...)
}

// newTransportFixtureWithPermissions builds the fixture with a specific
// current-permission checker, so that a test can decide what the
// administrative writes are told about the caller's current membership.
func newTransportFixtureWithPermissions(t *testing.T, permissions application.CurrentPermissionChecker, options ...application.Option) transportFixture {
	t.Helper()

	repository := newIntegrationTenantRepository(t)
	memberships := &membershipRecorder{}
	handler, jwks := newDynamicTestHandler(t, application.NewTenantService(repository, repository, memberships, permissions, options...))
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	return transportFixture{
		repository:  repository,
		memberships: memberships,
		jwks:        jwks,
		client:      tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL),
	}
}

// newAdministrationFixture wires the relation model's real repository as both
// the membership writer and — through the relation side's Authorizer — the
// current-permission checker, exactly as the server does. The administrative
// writes re-read the caller's membership from the database, so they need the
// real one. The repository is returned as well, so a test can seed the
// caller's membership and change its role.
func newAdministrationFixture(t *testing.T) (transportFixture, *relationdb.PostgresMembershipRepository) {
	t.Helper()

	repository := newIntegrationTenantRepository(t)
	memberships := relationdb.NewPostgresMembershipRepository(integrationDB)
	handler, jwks := newDynamicTestHandler(t, application.NewTenantService(repository, repository, memberships, relationapplication.NewAuthorizer(memberships)))
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)

	fixture := transportFixture{
		repository:  repository,
		memberships: nil,
		jwks:        jwks,
		client:      tenantv1connect.NewTenantServiceClient(httpServer.Client(), httpServer.URL),
	}

	return fixture, memberships
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
	fixture, memberships := newAdministrationFixture(t)

	registration := fixture.startRegistration(t, "Acme")
	tenantID := registration.GetTenant().GetTenantId()

	if _, err := fixture.claim(t, internalJWTs(t).registration, tenantID, registration.GetOwnershipClaimToken()); err != nil {
		t.Fatalf("ClaimTenantOwnership() error = %v", err)
	}

	stored, err := fixture.repository.FindTenantByPublicID(context.Background(), tenantID)
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

	list := func(t *testing.T, includeArchived bool) []*tenantv1.Event {
		t.Helper()

		req := connectrpc.NewRequest(&tenantv1.ListEventsRequest{
			TenantId:        tenant.PublicID(),
			IncludeArchived: includeArchived,
		})
		req.Header().Set("Authorization", token)

		res, err := fixture.client.ListEvents(context.Background(), req)
		if err != nil {
			t.Fatalf("ListEvents(include_archived=%t) error = %v", includeArchived, err)
		}

		return res.Msg.GetEvents()
	}

	events := list(t, false)

	want := []string{first.GetEventId(), second.GetEventId()}
	if len(events) != len(want) {
		t.Fatalf("event count = %d, want %d", len(events), len(want))
	}

	for i, event := range events {
		if got := event.GetEventId(); got != want[i] {
			t.Errorf("Events[%d].EventId = %q, want %q", i, got, want[i])
		}

		if got := event.GetTenantId(); got != tenant.PublicID() {
			t.Errorf("Events[%d].TenantId = %q, want %q", i, got, tenant.PublicID())
		}
	}

	// Archiving the second event takes it out of the default listing; only
	// include_archived brings it back.
	archive := connectrpc.NewRequest(&tenantv1.TransitionEventStatusRequest{
		EventId: second.GetEventId(),
		To:      tenantv1.EventStatus_EVENT_STATUS_ARCHIVED,
	})
	archive.Header().Set("Authorization", token)

	if _, err := fixture.client.TransitionEventStatus(context.Background(), archive); err != nil {
		t.Fatalf("TransitionEventStatus(archived) error = %v", err)
	}

	t.Run("omits archived events by default", func(t *testing.T) {
		want := []string{first.GetEventId()}
		if got := eventIDs(list(t, false)); !slices.Equal(got, want) {
			t.Errorf("ListEvents() = %v, want %v", got, want)
		}
	})

	t.Run("includes archived events when asked", func(t *testing.T) {
		want := []string{first.GetEventId(), second.GetEventId()}
		if got := eventIDs(list(t, true)); !slices.Equal(got, want) {
			t.Errorf("ListEvents(include_archived) = %v, want %v", got, want)
		}
	})

	foreign := connectrpc.NewRequest(&tenantv1.ListEventsRequest{TenantId: other.PublicID()})
	foreign.Header().Set("Authorization", token)

	_, err := fixture.client.ListEvents(context.Background(), foreign)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
		t.Fatalf("ListEvents(foreign tenant) error code = %v, want %v", got, want)
	}
}

// eventIDs projects a listing onto the public IDs its order is asserted on.
func eventIDs(events []*tenantv1.Event) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.GetEventId())
	}

	return ids
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

// archiveEvent walks an event to archived, which is only reachable through the
// lifecycle transitions.
func (f transportFixture) archiveEvent(t *testing.T, token, eventID string) {
	t.Helper()

	for _, to := range []tenantv1.EventStatus{
		tenantv1.EventStatus_EVENT_STATUS_OPEN,
		tenantv1.EventStatus_EVENT_STATUS_LOCKED,
		tenantv1.EventStatus_EVENT_STATUS_CLOSED,
		tenantv1.EventStatus_EVENT_STATUS_ARCHIVED,
	} {
		req := connectrpc.NewRequest(&tenantv1.TransitionEventStatusRequest{EventId: eventID, To: to})
		req.Header().Set("Authorization", token)

		if _, err := f.client.TransitionEventStatus(context.Background(), req); err != nil {
			t.Fatalf("TransitionEventStatus(%v) error = %v", to, err)
		}
	}
}

func (f transportFixture) observationSettings(t *testing.T, token, eventID string) (*tenantv1.ObservationSettings, error) {
	t.Helper()

	req := connectrpc.NewRequest(&tenantv1.GetObservationSettingsRequest{EventId: eventID})
	req.Header().Set("Authorization", token)

	res, err := f.client.GetObservationSettings(context.Background(), req)
	if err != nil {
		return nil, err
	}

	return res.Msg.GetSettings(), nil
}

func (f transportFixture) updateObservationSettings(t *testing.T, token, eventID string, historyWindowDays int32) (*tenantv1.ObservationSettings, error) {
	t.Helper()

	req := connectrpc.NewRequest(&tenantv1.UpdateObservationSettingsRequest{
		EventId:  eventID,
		Settings: &tenantv1.ObservationSettings{HistoryWindowDays: historyWindowDays},
	})
	req.Header().Set("Authorization", token)

	res, err := f.client.UpdateObservationSettings(context.Background(), req)
	if err != nil {
		return nil, err
	}

	return res.Msg.GetSettings(), nil
}

func TestGetObservationSettingsOverTransport(t *testing.T) {
	fixture := newTransportFixture(t)
	tenant := fixture.createTenant(t, "0123456789abcdef", "Observation Host")
	other := fixture.createTenant(t, "fedcba9876543210", "Other Tenant")
	token := mintTenantAccessToken(t, fixture.jwks, tenant.PublicID())
	created := fixture.createEvent(t, token, tenant.PublicID(), "Festival")

	t.Run("answers a machine-origin service token with the default window", func(t *testing.T) {
		// The boundary is not enforced for this read, so a service token
		// without tenant context is accepted.
		settings, err := fixture.observationSettings(t, internalJWTs(t).service, created.GetEventId())
		if err != nil {
			t.Fatalf("GetObservationSettings() error = %v", err)
		}

		if got, want := settings.GetHistoryWindowDays(), int32(domain.DefaultHistoryWindowDays); got != want {
			t.Errorf("HistoryWindowDays = %d, want %d", got, want)
		}
	})

	t.Run("answers for an archived event", func(t *testing.T) {
		archived := fixture.createEvent(t, token, tenant.PublicID(), "Archived Festival")
		fixture.archiveEvent(t, token, archived.GetEventId())

		settings, err := fixture.observationSettings(t, internalJWTs(t).service, archived.GetEventId())
		if err != nil {
			t.Fatalf("GetObservationSettings(archived) error = %v", err)
		}

		if got, want := settings.GetHistoryWindowDays(), int32(domain.DefaultHistoryWindowDays); got != want {
			t.Errorf("HistoryWindowDays = %d, want %d", got, want)
		}
	})

	tests := []struct {
		name    string
		eventID string
		token   string
		want    connectrpc.Code
	}{
		// A service token that does carry a tenant is cross-checked.
		{name: "rejects a service token of another tenant", eventID: created.GetEventId(), token: mintServiceToken(t, fixture.jwks, other.PublicID()), want: connectrpc.CodePermissionDenied},
		{name: "rejects a tenant_access token", eventID: created.GetEventId(), token: token, want: connectrpc.CodeUnauthenticated},
		{name: "reports unknown public IDs as not found", eventID: "0000000000000000", token: internalJWTs(t).service, want: connectrpc.CodeNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fixture.observationSettings(t, tt.token, tt.eventID)
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("GetObservationSettings() error code = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUpdateObservationSettingsOverTransport(t *testing.T) {
	fixture := newTransportFixture(t)
	tenant := fixture.createTenant(t, "0123456789abcdef", "Observation Host")
	token := mintTenantAccessToken(t, fixture.jwks, tenant.PublicID())
	created := fixture.createEvent(t, token, tenant.PublicID(), "Festival")

	settings, err := fixture.updateObservationSettings(t, token, created.GetEventId(), 45)
	if err != nil {
		t.Fatalf("UpdateObservationSettings() error = %v", err)
	}

	if got, want := settings.GetHistoryWindowDays(), int32(45); got != want {
		t.Errorf("HistoryWindowDays = %d, want %d", got, want)
	}

	// The change is what the service-to-service read answers with afterwards.
	stored, err := fixture.observationSettings(t, internalJWTs(t).service, created.GetEventId())
	if err != nil {
		t.Fatalf("GetObservationSettings() after update error = %v", err)
	}

	if got, want := stored.GetHistoryWindowDays(), int32(45); got != want {
		t.Errorf("HistoryWindowDays after update = %d, want %d", got, want)
	}

	t.Run("rejects a window shorter than one day", func(t *testing.T) {
		_, err := fixture.updateObservationSettings(t, token, created.GetEventId(), 0)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument; got != want {
			t.Fatalf("UpdateObservationSettings() error code = %v, want %v", got, want)
		}
	})

	t.Run("rejects a request without settings", func(t *testing.T) {
		req := connectrpc.NewRequest(&tenantv1.UpdateObservationSettingsRequest{EventId: created.GetEventId()})
		req.Header().Set("Authorization", token)

		_, err := fixture.client.UpdateObservationSettings(context.Background(), req)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument; got != want {
			t.Fatalf("UpdateObservationSettings() error code = %v, want %v", got, want)
		}
	})

	t.Run("refuses an archived event", func(t *testing.T) {
		archived := fixture.createEvent(t, token, tenant.PublicID(), "Archived Festival")
		fixture.archiveEvent(t, token, archived.GetEventId())

		_, err := fixture.updateObservationSettings(t, token, archived.GetEventId(), 45)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
			t.Fatalf("UpdateObservationSettings(archived) error code = %v, want %v", got, want)
		}
	})
}

func TestTransitionEventStatusOverTransport(t *testing.T) {
	fixture := newTransportFixture(t)
	tenant := fixture.createTenant(t, "0123456789abcdef", "Status Host")
	token := mintTenantAccessToken(t, fixture.jwks, tenant.PublicID())
	eventID := fixture.createEvent(t, token, tenant.PublicID(), "Status Event").GetEventId()

	transition := func(eventID string, to tenantv1.EventStatus) (*tenantv1.Event, error) {
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
		event, err := transition(eventID, want)
		if err != nil {
			t.Fatalf("TransitionEventStatus(%v) error = %v", want, err)
		}

		if got := event.GetStatus(); got != want {
			t.Errorf("Event.Status = %v, want %v", got, want)
		}
	}

	_, err := transition(eventID, tenantv1.EventStatus_EVENT_STATUS_LOCKED)
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
		t.Errorf("invalid transition error code = %v, want %v", got, want)
	}

	t.Run("discards a draft event and restores it as closed", func(t *testing.T) {
		draftID := fixture.createEvent(t, token, tenant.PublicID(), "Discarded Event").GetEventId()

		for _, want := range []tenantv1.EventStatus{
			tenantv1.EventStatus_EVENT_STATUS_ARCHIVED,
			tenantv1.EventStatus_EVENT_STATUS_CLOSED,
		} {
			event, err := transition(draftID, want)
			if err != nil {
				t.Fatalf("TransitionEventStatus(%v) error = %v", want, err)
			}

			if got := event.GetStatus(); got != want {
				t.Errorf("Event.Status = %v, want %v", got, want)
			}
		}
	})
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

// addTenantMember records the membership of "test-subject" — the subject every
// test token carries — so the administrative writes find a current permission.
func addTenantMember(t *testing.T, memberships *relationdb.PostgresMembershipRepository, tenantID string, role relationdomain.Role) {
	t.Helper()

	if _, err := memberships.AddTenantMember(context.Background(), tenantID, "test-subject", role); err != nil {
		t.Fatalf("AddTenantMember(%v) error = %v", role, err)
	}
}

func changeTenantRole(t *testing.T, memberships *relationdb.PostgresMembershipRepository, tenantID string, role relationdomain.Role) {
	t.Helper()

	if _, err := memberships.ChangeTenantRole(context.Background(), tenantID, "test-subject", role); err != nil {
		t.Fatalf("ChangeTenantRole(%v) error = %v", role, err)
	}
}

func (f transportFixture) changeTenantContract(tenantID, contractPlan, token string) error {
	req := connectrpc.NewRequest(&tenantv1.ChangeTenantContractRequest{TenantId: tenantID, ContractPlan: contractPlan})
	req.Header().Set("Authorization", token)

	_, err := f.client.ChangeTenantContract(context.Background(), req)

	return err
}

func (f transportFixture) archiveTenant(tenantID, token string) (*tenantv1.Tenant, error) {
	req := connectrpc.NewRequest(&tenantv1.ArchiveTenantRequest{TenantId: tenantID})
	req.Header().Set("Authorization", token)

	res, err := f.client.ArchiveTenant(context.Background(), req)
	if err != nil {
		return nil, err
	}

	return res.Msg.GetTenant(), nil
}

func TestChangeTenantContractOverTransport(t *testing.T) {
	fixture, memberships := newAdministrationFixture(t)
	tenant := fixture.createTenant(t, "0123456789abcdef", "Contract Host")
	addTenantMember(t, memberships, tenant.ID(), relationdomain.RoleOwner)
	token := mintTenantAccessToken(t, fixture.jwks, tenant.PublicID())

	req := connectrpc.NewRequest(&tenantv1.ChangeTenantContractRequest{TenantId: tenant.PublicID(), ContractPlan: "enterprise"})
	req.Header().Set("Authorization", token)

	res, err := fixture.client.ChangeTenantContract(context.Background(), req)
	if err != nil {
		t.Fatalf("ChangeTenantContract() error = %v", err)
	}

	if got, want := res.Msg.GetTenant().GetContractPlan(), "enterprise"; got != want {
		t.Errorf("Tenant.ContractPlan = %q, want %q", got, want)
	}

	if got, want := res.Msg.GetTenant().GetTenantId(), tenant.PublicID(); got != want {
		t.Errorf("Tenant.TenantId = %q, want %q", got, want)
	}

	stored, err := fixture.repository.FindTenantByPublicID(context.Background(), tenant.PublicID())
	if err != nil {
		t.Fatalf("FindTenantByPublicID() error = %v", err)
	}

	if got, want := stored.ContractPlan(), "enterprise"; got != want {
		t.Errorf("stored contract plan = %q, want %q", got, want)
	}

	if stored.Archived() {
		t.Error("stored tenant is archived after a contract change, want not archived")
	}
}

// TestArchiveTenantOverTransport covers the soft delete: the tenant keeps its
// identifier, name and events, writes are refused afterwards, and reads keep
// working.
func TestArchiveTenantOverTransport(t *testing.T) {
	fixture, memberships := newAdministrationFixture(t)
	tenant := fixture.createTenant(t, "0123456789abcdef", "Archive Host")
	addTenantMember(t, memberships, tenant.ID(), relationdomain.RoleOwner)
	token := mintTenantAccessToken(t, fixture.jwks, tenant.PublicID())
	event := fixture.createEvent(t, token, tenant.PublicID(), "Festival")

	archived, err := fixture.archiveTenant(tenant.PublicID(), token)
	if err != nil {
		t.Fatalf("ArchiveTenant() error = %v", err)
	}

	if !archived.GetArchived() {
		t.Error("Tenant.Archived = false, want true")
	}

	// The identifier and the name survive the soft delete.
	if got, want := archived.GetTenantId(), tenant.PublicID(); got != want {
		t.Errorf("Tenant.TenantId = %q, want %q", got, want)
	}

	if got, want := archived.GetName(), tenant.Name(); got != want {
		t.Errorf("Tenant.Name = %q, want %q", got, want)
	}

	t.Run("writes are refused", func(t *testing.T) {
		createReq := connectrpc.NewRequest(&tenantv1.CreateEventRequest{TenantId: tenant.PublicID(), Name: "Second Festival"})
		createReq.Header().Set("Authorization", token)

		_, err := fixture.client.CreateEvent(context.Background(), createReq)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
			t.Errorf("CreateEvent() error code = %v, want %v", got, want)
		}

		_, err = fixture.archiveTenant(tenant.PublicID(), token)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
			t.Errorf("second ArchiveTenant() error code = %v, want %v", got, want)
		}
	})

	t.Run("reads keep working", func(t *testing.T) {
		listReq := connectrpc.NewRequest(&tenantv1.ListEventsRequest{TenantId: tenant.PublicID()})
		listReq.Header().Set("Authorization", token)

		list, err := fixture.client.ListEvents(context.Background(), listReq)
		if err != nil {
			t.Fatalf("ListEvents() after archiving error = %v", err)
		}

		if got, want := len(list.Msg.GetEvents()), 1; got != want {
			t.Fatalf("event count = %d, want %d", got, want)
		}

		getReq := connectrpc.NewRequest(&tenantv1.GetEventRequest{EventId: event.GetEventId()})
		getReq.Header().Set("Authorization", mintServiceToken(t, fixture.jwks, tenant.PublicID()))

		got, err := fixture.client.GetEvent(context.Background(), getReq)
		if err != nil {
			t.Fatalf("GetEvent() after archiving error = %v", err)
		}

		// The events of an archived tenant keep their status.
		if got, want := got.Msg.GetEvent().GetStatus(), event.GetStatus(); got != want {
			t.Errorf("Event.Status after archiving = %v, want %v", got, want)
		}
	})
}

// TestAdministrativeTenantWritesOverTransportRejections covers the current
// permission check and the request-level gates of both administrative writes.
// The subtests run in order: they change the caller's membership as they go.
func TestAdministrativeTenantWritesOverTransportRejections(t *testing.T) {
	fixture, memberships := newAdministrationFixture(t)
	tenant := fixture.createTenant(t, "0123456789abcdef", "Contract Host")
	other := fixture.createTenant(t, "fedcba9876543210", "Other Tenant")
	token := mintTenantAccessToken(t, fixture.jwks, tenant.PublicID())
	otherToken := mintTenantAccessToken(t, fixture.jwks, other.PublicID())
	pending := fixture.startRegistration(t, "Pending Tenant").GetTenant().GetTenantId()

	t.Run("subject without a membership", func(t *testing.T) {
		// The token's scope says tenant.write, but the caller belongs to no
		// tenant, so the current permission check refuses the write.
		err := fixture.changeTenantContract(tenant.PublicID(), "enterprise", token)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
			t.Errorf("ChangeTenantContract() error code = %v, want %v", got, want)
		}
	})

	t.Run("role that cannot issue tenant.write", func(t *testing.T) {
		addTenantMember(t, memberships, tenant.ID(), relationdomain.RoleOwner)
		changeTenantRole(t, memberships, tenant.ID(), relationdomain.RoleStaff)

		err := fixture.changeTenantContract(tenant.PublicID(), "enterprise", token)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
			t.Errorf("ChangeTenantContract() error code = %v, want %v", got, want)
		}

		_, err = fixture.archiveTenant(tenant.PublicID(), token)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
			t.Errorf("ArchiveTenant() error code = %v, want %v", got, want)
		}
	})

	// The caller owns the tenant from here on: what is left are the gates on
	// the request and on the tenant itself.
	changeTenantRole(t, memberships, tenant.ID(), relationdomain.RoleOwner)

	tests := []struct {
		name         string
		tenantID     string
		contractPlan string
		token        string
		want         connectrpc.Code
	}{
		{name: "missing contract plan", tenantID: tenant.PublicID(), contractPlan: "", token: token, want: connectrpc.CodeInvalidArgument},
		{name: "token of another tenant", tenantID: tenant.PublicID(), contractPlan: "enterprise", token: otherToken, want: connectrpc.CodePermissionDenied},
		{name: "pending_owner tenant", tenantID: pending, contractPlan: "enterprise", token: mintTenantAccessToken(t, fixture.jwks, pending), want: connectrpc.CodeFailedPrecondition},
		{name: "unknown tenant", tenantID: "0000000000000000", contractPlan: "enterprise", token: mintTenantAccessToken(t, fixture.jwks, "0000000000000000"), want: connectrpc.CodeNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fixture.changeTenantContract(tt.tenantID, tt.contractPlan, tt.token)
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("ChangeTenantContract() error code = %v, want %v", got, tt.want)
			}
		})
	}

	// None of the rejections changed the tenant.
	stored, err := fixture.repository.FindTenantByPublicID(context.Background(), tenant.PublicID())
	if err != nil {
		t.Fatalf("FindTenantByPublicID() error = %v", err)
	}

	if got, want := stored.ContractPlan(), "standard"; got != want {
		t.Errorf("stored contract plan = %q, want %q", got, want)
	}

	if stored.Archived() {
		t.Error("stored tenant is archived after the rejected writes, want not archived")
	}
}
