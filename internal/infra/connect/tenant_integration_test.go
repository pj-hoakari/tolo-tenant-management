package connect_test

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect"

	relationv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/relation/v1"
	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	relationdomain "github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenant/application"
	tenantdomain "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/domain"
	tenantrepository "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/repository"
)

var (
	publicIDPattern   = regexp.MustCompile(`^[0-9a-f]{16}$`)
	claimTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
)

func TestStartTenantRegistrationOverTransport(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	p := newProcess(t, application.WithClock(func() time.Time { return now }))

	// The public RPC is served without a bearer token.
	res, err := p.tenantClient.StartTenantRegistration(context.Background(), connectrpc.NewRequest(&tenantv1.StartTenantRegistrationRequest{Name: "Acme", ContractPlan: "standard"}))
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
		_, err := p.tenantClient.StartTenantRegistration(context.Background(), connectrpc.NewRequest(&tenantv1.StartTenantRegistrationRequest{Name: "Acme", ContractPlan: "standard"}))
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeAlreadyExists; got != want {
			t.Fatalf("StartTenantRegistration() error code = %v, want %v", got, want)
		}
	})

	t.Run("rejects missing required fields", func(t *testing.T) {
		_, err := p.tenantClient.StartTenantRegistration(context.Background(), connectrpc.NewRequest(&tenantv1.StartTenantRegistrationRequest{Name: "Acme"}))
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument; got != want {
			t.Fatalf("StartTenantRegistration() error code = %v, want %v", got, want)
		}
	})
}

func TestPendingOwnerTenantRejectsTenantScopedCalls(t *testing.T) {
	p := newProcess(t)

	tenantID := p.startRegistration(t, "Acme").GetTenant().GetTenantId()
	token := p.mintTenantAccessToken(t, tenantID)

	_, err := p.tenantClient.CreateEvent(context.Background(), authorized(token, &tenantv1.CreateEventRequest{TenantId: tenantID, Name: "Festival"}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
		t.Errorf("CreateEvent() error code = %v, want %v", got, want)
	}

	_, err = p.tenantClient.ListEvents(context.Background(), authorized(token, &tenantv1.ListEventsRequest{TenantId: tenantID}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
		t.Errorf("ListEvents() error code = %v, want %v", got, want)
	}
}

func TestStartTenantRegistrationOverTransportReleasesExpiredNames(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	p := newProcess(t, application.WithClock(func() time.Time { return now }), application.WithOwnershipClaimTTL(time.Hour))

	first := p.startRegistration(t, "Acme")

	// Once the first registration expires, the next registration sweeps it
	// and the name is free again.
	now = now.Add(2 * time.Hour)

	second := p.startRegistration(t, "Acme")

	if first.GetTenant().GetTenantId() == second.GetTenant().GetTenantId() {
		t.Error("second registration reused the expired tenant instead of creating a new one")
	}

	if _, err := p.tenants.FindTenantByPublicID(context.Background(), first.GetTenant().GetTenantId()); !errors.Is(err, tenantrepository.ErrTenantNotFound) {
		t.Errorf("expired tenant lookup error = %v, want %v", err, tenantrepository.ErrTenantNotFound)
	}
}

// TestClaimTenantOwnershipOverTransport walks the onboarding across both
// services: the claim made through TenantService leaves an owner membership
// that RelationAdminService lists for the claiming subject.
func TestClaimTenantOwnershipOverTransport(t *testing.T) {
	p := newProcess(t)
	registration := p.startRegistration(t, "Acme")
	tenantID := registration.GetTenant().GetTenantId()

	owned, err := p.claim(t, internalJWTs(t).registration, tenantID, registration.GetOwnershipClaimToken())
	if err != nil {
		t.Fatalf("ClaimTenantOwnership() error = %v", err)
	}

	if got, want := owned.GetOwnershipState(), tenantv1.TenantOwnershipState_TENANT_OWNERSHIP_STATE_OWNED; got != want {
		t.Errorf("Tenant.OwnershipState = %v, want %v", got, want)
	}

	token := p.mintTenantAccessToken(t, tenantID)

	t.Run("the claiming subject owns the tenant", func(t *testing.T) {
		res, err := p.relationClient.ListMemberships(context.Background(), authorized(token, &relationv1.ListMembershipsRequest{Filter: &relationv1.ListMembershipsRequest_TenantId{TenantId: tenantID}}))
		if err != nil {
			t.Fatalf("ListMemberships() error = %v", err)
		}

		memberships := res.Msg.GetMemberships()
		if len(memberships) != 1 || memberships[0].GetUserId() != callerSubject || memberships[0].GetTenantId() != tenantID || memberships[0].GetTenantRole() != relationv1.Role_ROLE_OWNER {
			t.Errorf("ListMemberships() = %v, want %q as the owner of %q and nobody else", memberships, callerSubject, tenantID)
		}
	})

	t.Run("the owned tenant accepts tenant-scoped calls", func(t *testing.T) {
		p.createEvent(t, token, tenantID, "Festival")
	})

	t.Run("the owner administers the tenant", func(t *testing.T) {
		// The administrative writes re-check the membership the claim recorded.
		if err := p.changeTenantContract(tenantID, "enterprise", token); err != nil {
			t.Fatalf("ChangeTenantContract() error = %v", err)
		}
	})

	t.Run("the claim token is consumed", func(t *testing.T) {
		_, err := p.claim(t, internalJWTs(t).registration, tenantID, registration.GetOwnershipClaimToken())
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnauthenticated; got != want {
			t.Fatalf("second ClaimTenantOwnership() error code = %v, want %v", got, want)
		}
	})
}

func TestClaimTenantOwnershipOverTransportRejections(t *testing.T) {
	p := newProcess(t)
	registration := p.startRegistration(t, "Acme")
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
			_, err := p.claim(t, tt.token, tt.tenantID, tt.claimToken)
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("ClaimTenantOwnership() error code = %v, want %v", got, tt.want)
			}
		})
	}

	// None of the rejections consumed the token: the valid claim still works.
	if _, err := p.claim(t, internalJWTs(t).registration, tenantID, claimToken); err != nil {
		t.Fatalf("ClaimTenantOwnership() after rejections error = %v", err)
	}
}

// TestClaimTenantOwnershipOverTransportRejectsMissingScope checks that a
// procedure whose policy names its own token_uses still has its required
// scopes enforced: the credential is of the accepted class (registration), but
// tenant.claim was not granted to it.
func TestClaimTenantOwnershipOverTransportRejectsMissingScope(t *testing.T) {
	p := newProcess(t)
	registration := p.startRegistration(t, "Acme")

	token := p.mintRegistrationTokenWithScope(t, "tenant.read")

	_, err := p.claim(t, token, registration.GetTenant().GetTenantId(), registration.GetOwnershipClaimToken())
	if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
		t.Fatalf("ClaimTenantOwnership() error code = %v, want %v", got, want)
	}
}

// TestClaimTenantOwnershipOverTransportRollsBackWhenMembershipFails makes the
// owner membership fail for real — the claiming subject already holds a
// membership of the pending tenant, which the relation model refuses to
// duplicate — and checks that the ownership transition of the same transaction
// is rolled back with it.
func TestClaimTenantOwnershipOverTransportRollsBackWhenMembershipFails(t *testing.T) {
	p := newProcess(t)
	registration := p.startRegistration(t, "Acme")
	tenantID := registration.GetTenant().GetTenantId()

	pending, err := p.tenants.FindTenantByPublicID(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("FindTenantByPublicID() error = %v", err)
	}

	p.addTenantMember(t, pending.ID(), callerSubject, relationdomain.RoleStaff)

	_, err = p.claim(t, internalJWTs(t).registration, tenantID, registration.GetOwnershipClaimToken())
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeInternal; got != want {
		t.Fatalf("ClaimTenantOwnership() error code = %v, want %v", got, want)
	}

	stored, err := p.tenants.FindTenantByPublicID(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("FindTenantByPublicID() error = %v", err)
	}

	if got, want := stored.OwnershipState(), tenantdomain.TenantOwnershipStatePendingOwner; got != want {
		t.Fatalf("OwnershipState() after failed claim = %v, want %v", got, want)
	}

	// Once the obstacle is gone, the same token completes the claim.
	if err := p.memberships.RevokeTenantMembership(context.Background(), pending.ID(), callerSubject); err != nil {
		t.Fatalf("RevokeTenantMembership() error = %v", err)
	}

	if _, err := p.claim(t, internalJWTs(t).registration, tenantID, registration.GetOwnershipClaimToken()); err != nil {
		t.Fatalf("ClaimTenantOwnership() after recovery error = %v", err)
	}

	membership, err := p.memberships.FindMembership(context.Background(), pending.ID(), callerSubject)
	if err != nil {
		t.Fatalf("FindMembership() after recovery error = %v", err)
	}

	if got, want := membership.TenantRole(), relationdomain.RoleOwner; got != want {
		t.Errorf("TenantRole() after recovery = %v, want %v", got, want)
	}
}

func TestCreateEventOverTransport(t *testing.T) {
	p := newProcess(t)
	tenant := p.createTenant(t, "0123456789abcdef", "Event Host")
	token := p.mintTenantAccessToken(t, tenant.PublicID())

	res, err := p.tenantClient.CreateEvent(context.Background(), authorized(token, &tenantv1.CreateEventRequest{
		TenantId: tenant.PublicID(),
		Name:     "Summer Festival",
		Type:     tenantv1.EventType_EVENT_TYPE_SHORT_TERM,
	}))
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
	p := newProcess(t)
	tenant := p.createTenant(t, "0123456789abcdef", "Own Tenant")
	other := p.createTenant(t, "fedcba9876543210", "Other Tenant")
	token := p.mintTenantAccessToken(t, tenant.PublicID())

	tests := []struct {
		name     string
		tenantID string
		token    string
		want     connectrpc.Code
	}{
		{name: "missing tenant_id", tenantID: "", token: token, want: connectrpc.CodeInvalidArgument},
		{name: "tenant_id of another tenant", tenantID: other.PublicID(), token: token, want: connectrpc.CodePermissionDenied},
		{name: "tenant_id of a nonexistent tenant", tenantID: "0000000000000000", token: token, want: connectrpc.CodePermissionDenied},
		{name: "token for a nonexistent tenant", tenantID: "0000000000000000", token: p.mintTenantAccessToken(t, "0000000000000000"), want: connectrpc.CodeNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.tenantClient.CreateEvent(context.Background(), authorized(tt.token, &tenantv1.CreateEventRequest{TenantId: tt.tenantID, Name: "Festival"}))
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("CreateEvent() error code = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCreateEventOverTransportRejectsMissingScope covers the scope half of the
// generated policy table: a caller authenticated for the tenant it names, but
// whose token was not granted the scope the procedure declares, is denied
// rather than left unauthenticated.
func TestCreateEventOverTransportRejectsMissingScope(t *testing.T) {
	p := newProcess(t)
	tenant := p.createTenant(t, "0123456789abcdef", "Scope Host")
	token := p.mintTenantAccessTokenWithScope(t, tenant.PublicID(), "events.read")

	_, err := p.tenantClient.CreateEvent(context.Background(), authorized(token, &tenantv1.CreateEventRequest{TenantId: tenant.PublicID(), Name: "Festival"}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
		t.Fatalf("CreateEvent() error code = %v, want %v", got, want)
	}
}

func TestListEventsOverTransport(t *testing.T) {
	p := newProcess(t)
	tenant := p.createTenant(t, "0123456789abcdef", "List Host")
	other := p.createTenant(t, "fedcba9876543210", "Other Tenant")
	token := p.mintTenantAccessToken(t, tenant.PublicID())
	otherToken := p.mintTenantAccessToken(t, other.PublicID())

	first := p.createEvent(t, token, tenant.PublicID(), "Festival 1")
	second := p.createEvent(t, token, tenant.PublicID(), "Festival 2")
	p.createEvent(t, otherToken, other.PublicID(), "Other Festival")

	list := func(t *testing.T, includeArchived bool) []*tenantv1.Event {
		t.Helper()

		res, err := p.tenantClient.ListEvents(context.Background(), authorized(token, &tenantv1.ListEventsRequest{
			TenantId:        tenant.PublicID(),
			IncludeArchived: includeArchived,
		}))
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
	if _, err := p.tenantClient.TransitionEventStatus(context.Background(), authorized(token, &tenantv1.TransitionEventStatusRequest{
		EventId: second.GetEventId(),
		To:      tenantv1.EventStatus_EVENT_STATUS_ARCHIVED,
	})); err != nil {
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

	_, err := p.tenantClient.ListEvents(context.Background(), authorized(token, &tenantv1.ListEventsRequest{TenantId: other.PublicID()}))
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
	p := newProcess(t)
	tenant := p.createTenant(t, "0123456789abcdef", "Get Host")
	other := p.createTenant(t, "fedcba9876543210", "Other Tenant")
	token := p.mintTenantAccessToken(t, tenant.PublicID())
	created := p.createEvent(t, token, tenant.PublicID(), "Festival")

	t.Run("returns the event to a service token of its tenant", func(t *testing.T) {
		res, err := p.tenantClient.GetEvent(context.Background(), authorized(p.mintServiceToken(t, tenant.PublicID()), &tenantv1.GetEventRequest{EventId: created.GetEventId()}))
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
		{name: "rejects service token of another tenant", eventID: created.GetEventId(), token: p.mintServiceToken(t, other.PublicID()), want: connectrpc.CodePermissionDenied},
		{name: "reports unknown public IDs as not found", eventID: "0000000000000000", token: p.mintServiceToken(t, tenant.PublicID()), want: connectrpc.CodeNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.tenantClient.GetEvent(context.Background(), authorized(tt.token, &tenantv1.GetEventRequest{EventId: tt.eventID}))
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("GetEvent() error code = %v, want %v", got, tt.want)
			}
		})
	}
}

// archiveEvent walks an event to archived, which is only reachable through the
// lifecycle transitions.
func (p *process) archiveEvent(t *testing.T, token, eventID string) {
	t.Helper()

	for _, to := range []tenantv1.EventStatus{
		tenantv1.EventStatus_EVENT_STATUS_OPEN,
		tenantv1.EventStatus_EVENT_STATUS_LOCKED,
		tenantv1.EventStatus_EVENT_STATUS_CLOSED,
		tenantv1.EventStatus_EVENT_STATUS_ARCHIVED,
	} {
		if _, err := p.tenantClient.TransitionEventStatus(context.Background(), authorized(token, &tenantv1.TransitionEventStatusRequest{EventId: eventID, To: to})); err != nil {
			t.Fatalf("TransitionEventStatus(%v) error = %v", to, err)
		}
	}
}

func (p *process) observationSettings(t *testing.T, token, eventID string) (*tenantv1.ObservationSettings, error) {
	t.Helper()

	res, err := p.tenantClient.GetObservationSettings(context.Background(), authorized(token, &tenantv1.GetObservationSettingsRequest{EventId: eventID}))
	if err != nil {
		return nil, err
	}

	return res.Msg.GetSettings(), nil
}

func (p *process) updateObservationSettings(t *testing.T, token, eventID string, historyWindowDays int32) (*tenantv1.ObservationSettings, error) {
	t.Helper()

	res, err := p.tenantClient.UpdateObservationSettings(context.Background(), authorized(token, &tenantv1.UpdateObservationSettingsRequest{
		EventId:  eventID,
		Settings: &tenantv1.ObservationSettings{HistoryWindowDays: historyWindowDays},
	}))
	if err != nil {
		return nil, err
	}

	return res.Msg.GetSettings(), nil
}

func TestGetObservationSettingsOverTransport(t *testing.T) {
	p := newProcess(t)
	tenant := p.createTenant(t, "0123456789abcdef", "Observation Host")
	other := p.createTenant(t, "fedcba9876543210", "Other Tenant")
	token := p.mintTenantAccessToken(t, tenant.PublicID())
	created := p.createEvent(t, token, tenant.PublicID(), "Festival")

	t.Run("answers a machine-origin service token with the default window", func(t *testing.T) {
		// The boundary is not enforced for this read, so a service token
		// without tenant context is accepted.
		settings, err := p.observationSettings(t, internalJWTs(t).service, created.GetEventId())
		if err != nil {
			t.Fatalf("GetObservationSettings() error = %v", err)
		}

		if got, want := settings.GetHistoryWindowDays(), int32(tenantdomain.DefaultHistoryWindowDays); got != want {
			t.Errorf("HistoryWindowDays = %d, want %d", got, want)
		}
	})

	t.Run("answers for an archived event", func(t *testing.T) {
		archived := p.createEvent(t, token, tenant.PublicID(), "Archived Festival")
		p.archiveEvent(t, token, archived.GetEventId())

		settings, err := p.observationSettings(t, internalJWTs(t).service, archived.GetEventId())
		if err != nil {
			t.Fatalf("GetObservationSettings(archived) error = %v", err)
		}

		if got, want := settings.GetHistoryWindowDays(), int32(tenantdomain.DefaultHistoryWindowDays); got != want {
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
		{name: "rejects a service token of another tenant", eventID: created.GetEventId(), token: p.mintServiceToken(t, other.PublicID()), want: connectrpc.CodePermissionDenied},
		{name: "rejects a tenant_access token", eventID: created.GetEventId(), token: token, want: connectrpc.CodeUnauthenticated},
		{name: "reports unknown public IDs as not found", eventID: "0000000000000000", token: internalJWTs(t).service, want: connectrpc.CodeNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := p.observationSettings(t, tt.token, tt.eventID)
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("GetObservationSettings() error code = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUpdateObservationSettingsOverTransport(t *testing.T) {
	p := newProcess(t)
	tenant := p.createTenant(t, "0123456789abcdef", "Observation Host")
	token := p.mintTenantAccessToken(t, tenant.PublicID())
	created := p.createEvent(t, token, tenant.PublicID(), "Festival")

	settings, err := p.updateObservationSettings(t, token, created.GetEventId(), 45)
	if err != nil {
		t.Fatalf("UpdateObservationSettings() error = %v", err)
	}

	if got, want := settings.GetHistoryWindowDays(), int32(45); got != want {
		t.Errorf("HistoryWindowDays = %d, want %d", got, want)
	}

	// The change is what the service-to-service read answers with afterwards.
	stored, err := p.observationSettings(t, internalJWTs(t).service, created.GetEventId())
	if err != nil {
		t.Fatalf("GetObservationSettings() after update error = %v", err)
	}

	if got, want := stored.GetHistoryWindowDays(), int32(45); got != want {
		t.Errorf("HistoryWindowDays after update = %d, want %d", got, want)
	}

	t.Run("rejects a window shorter than one day", func(t *testing.T) {
		_, err := p.updateObservationSettings(t, token, created.GetEventId(), 0)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument; got != want {
			t.Fatalf("UpdateObservationSettings() error code = %v, want %v", got, want)
		}
	})

	t.Run("rejects a request without settings", func(t *testing.T) {
		_, err := p.tenantClient.UpdateObservationSettings(context.Background(), authorized(token, &tenantv1.UpdateObservationSettingsRequest{EventId: created.GetEventId()}))
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument; got != want {
			t.Fatalf("UpdateObservationSettings() error code = %v, want %v", got, want)
		}
	})

	t.Run("refuses an archived event", func(t *testing.T) {
		archived := p.createEvent(t, token, tenant.PublicID(), "Archived Festival")
		p.archiveEvent(t, token, archived.GetEventId())

		_, err := p.updateObservationSettings(t, token, archived.GetEventId(), 45)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
			t.Fatalf("UpdateObservationSettings(archived) error code = %v, want %v", got, want)
		}
	})
}

func TestTransitionEventStatusOverTransport(t *testing.T) {
	p := newProcess(t)
	tenant := p.createTenant(t, "0123456789abcdef", "Status Host")
	token := p.mintTenantAccessToken(t, tenant.PublicID())
	eventID := p.createEvent(t, token, tenant.PublicID(), "Status Event").GetEventId()

	transition := func(eventID string, to tenantv1.EventStatus) (*tenantv1.Event, error) {
		t.Helper()

		response, err := p.tenantClient.TransitionEventStatus(context.Background(), authorized(token, &tenantv1.TransitionEventStatusRequest{EventId: eventID, To: to}))
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
		draftID := p.createEvent(t, token, tenant.PublicID(), "Discarded Event").GetEventId()

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
	p := newProcess(t)
	tenant := p.createTenant(t, "0123456789abcdef", "Own Tenant")
	other := p.createTenant(t, "fedcba9876543210", "Other Tenant")
	otherEvent := p.createEvent(t, p.mintTenantAccessToken(t, other.PublicID()), other.PublicID(), "Other Festival")

	_, err := p.tenantClient.TransitionEventStatus(context.Background(), authorized(p.mintTenantAccessToken(t, tenant.PublicID()), &tenantv1.TransitionEventStatusRequest{
		EventId: otherEvent.GetEventId(),
		To:      tenantv1.EventStatus_EVENT_STATUS_OPEN,
	}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
		t.Fatalf("TransitionEventStatus(foreign event) error code = %v, want %v", got, want)
	}
}

func TestTransitionEventStatusOverTransportRejectsInvalidRequest(t *testing.T) {
	p := newProcess(t)

	_, err := p.tenantClient.TransitionEventStatus(context.Background(), authorized(internalJWTs(t).tenantAccess, &tenantv1.TransitionEventStatusRequest{}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeInvalidArgument; got != want {
		t.Fatalf("TransitionEventStatus() error code = %v, want %v", got, want)
	}

	_, err = p.tenantClient.TransitionEventStatus(context.Background(), authorized(internalJWTs(t).tenantAccess, &tenantv1.TransitionEventStatusRequest{
		EventId: "0000000000000000",
		To:      tenantv1.EventStatus_EVENT_STATUS_OPEN,
	}))
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeNotFound; got != want {
		t.Fatalf("TransitionEventStatus() error code = %v, want %v", got, want)
	}
}

func (p *process) changeTenantContract(tenantID, contractPlan, token string) error {
	_, err := p.tenantClient.ChangeTenantContract(context.Background(), authorized(token, &tenantv1.ChangeTenantContractRequest{TenantId: tenantID, ContractPlan: contractPlan}))

	return err
}

func (p *process) archiveTenant(tenantID, token string) (*tenantv1.Tenant, error) {
	res, err := p.tenantClient.ArchiveTenant(context.Background(), authorized(token, &tenantv1.ArchiveTenantRequest{TenantId: tenantID}))
	if err != nil {
		return nil, err
	}

	return res.Msg.GetTenant(), nil
}

func TestChangeTenantContractOverTransport(t *testing.T) {
	p := newProcess(t)
	tenant := p.createTenant(t, "0123456789abcdef", "Contract Host")
	p.addTenantMember(t, tenant.ID(), callerSubject, relationdomain.RoleOwner)
	token := p.mintTenantAccessToken(t, tenant.PublicID())

	res, err := p.tenantClient.ChangeTenantContract(context.Background(), authorized(token, &tenantv1.ChangeTenantContractRequest{TenantId: tenant.PublicID(), ContractPlan: "enterprise"}))
	if err != nil {
		t.Fatalf("ChangeTenantContract() error = %v", err)
	}

	if got, want := res.Msg.GetTenant().GetContractPlan(), "enterprise"; got != want {
		t.Errorf("Tenant.ContractPlan = %q, want %q", got, want)
	}

	if got, want := res.Msg.GetTenant().GetTenantId(), tenant.PublicID(); got != want {
		t.Errorf("Tenant.TenantId = %q, want %q", got, want)
	}

	stored, err := p.tenants.FindTenantByPublicID(context.Background(), tenant.PublicID())
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
	p := newProcess(t)
	tenant := p.createTenant(t, "0123456789abcdef", "Archive Host")
	p.addTenantMember(t, tenant.ID(), callerSubject, relationdomain.RoleOwner)
	token := p.mintTenantAccessToken(t, tenant.PublicID())
	event := p.createEvent(t, token, tenant.PublicID(), "Festival")

	archived, err := p.archiveTenant(tenant.PublicID(), token)
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
		_, err := p.tenantClient.CreateEvent(context.Background(), authorized(token, &tenantv1.CreateEventRequest{TenantId: tenant.PublicID(), Name: "Second Festival"}))
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
			t.Errorf("CreateEvent() error code = %v, want %v", got, want)
		}

		_, err = p.archiveTenant(tenant.PublicID(), token)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
			t.Errorf("second ArchiveTenant() error code = %v, want %v", got, want)
		}
	})

	t.Run("reads keep working", func(t *testing.T) {
		list, err := p.tenantClient.ListEvents(context.Background(), authorized(token, &tenantv1.ListEventsRequest{TenantId: tenant.PublicID()}))
		if err != nil {
			t.Fatalf("ListEvents() after archiving error = %v", err)
		}

		if got, want := len(list.Msg.GetEvents()), 1; got != want {
			t.Fatalf("event count = %d, want %d", got, want)
		}

		got, err := p.tenantClient.GetEvent(context.Background(), authorized(p.mintServiceToken(t, tenant.PublicID()), &tenantv1.GetEventRequest{EventId: event.GetEventId()}))
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
	p := newProcess(t)
	tenant := p.createTenant(t, "0123456789abcdef", "Contract Host")
	other := p.createTenant(t, "fedcba9876543210", "Other Tenant")
	token := p.mintTenantAccessToken(t, tenant.PublicID())
	otherToken := p.mintTenantAccessToken(t, other.PublicID())
	pending := p.startRegistration(t, "Pending Tenant").GetTenant().GetTenantId()

	t.Run("subject without a membership", func(t *testing.T) {
		// The token's scope says tenant.write, but the caller belongs to no
		// tenant, so the current permission check refuses the write.
		err := p.changeTenantContract(tenant.PublicID(), "enterprise", token)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
			t.Errorf("ChangeTenantContract() error code = %v, want %v", got, want)
		}
	})

	t.Run("role that cannot issue tenant.write", func(t *testing.T) {
		p.addTenantMember(t, tenant.ID(), callerSubject, relationdomain.RoleOwner)
		p.changeTenantRole(t, tenant.ID(), callerSubject, relationdomain.RoleStaff)

		err := p.changeTenantContract(tenant.PublicID(), "enterprise", token)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
			t.Errorf("ChangeTenantContract() error code = %v, want %v", got, want)
		}

		_, err = p.archiveTenant(tenant.PublicID(), token)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodePermissionDenied; got != want {
			t.Errorf("ArchiveTenant() error code = %v, want %v", got, want)
		}
	})

	// The caller owns the tenant from here on: what is left are the gates on
	// the request and on the tenant itself.
	p.changeTenantRole(t, tenant.ID(), callerSubject, relationdomain.RoleOwner)

	tests := []struct {
		name         string
		tenantID     string
		contractPlan string
		token        string
		want         connectrpc.Code
	}{
		{name: "missing contract plan", tenantID: tenant.PublicID(), contractPlan: "", token: token, want: connectrpc.CodeInvalidArgument},
		{name: "token of another tenant", tenantID: tenant.PublicID(), contractPlan: "enterprise", token: otherToken, want: connectrpc.CodePermissionDenied},
		{name: "pending_owner tenant", tenantID: pending, contractPlan: "enterprise", token: p.mintTenantAccessToken(t, pending), want: connectrpc.CodeFailedPrecondition},
		{name: "unknown tenant", tenantID: "0000000000000000", contractPlan: "enterprise", token: p.mintTenantAccessToken(t, "0000000000000000"), want: connectrpc.CodeNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.changeTenantContract(tt.tenantID, tt.contractPlan, tt.token)
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("ChangeTenantContract() error code = %v, want %v", got, tt.want)
			}
		})
	}

	// None of the rejections changed the tenant.
	stored, err := p.tenants.FindTenantByPublicID(context.Background(), tenant.PublicID())
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
