//go:generate go tool mockgen -source=../repository/tenant.go -destination=mock_tenant_repository_test.go -package=application_test

package application_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
	"go.uber.org/mock/gomock"
)

// passthroughTransactor runs the unit of work directly; the repository mocks
// do not observe transactions.
type passthroughTransactor struct{}

func (passthroughTransactor) WithinTransaction(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

// membershipRecorder records owner memberships and can be made to fail.
type membershipRecorder struct {
	err      error
	tenantID string
	userID   string
	calls    int
}

func (r *membershipRecorder) AddOwner(_ context.Context, tenantID, userID string) error {
	r.calls++
	r.tenantID, r.userID = tenantID, userID

	return r.err
}

// permissionStub stands in for the relation side's current-permission check.
type permissionStub struct {
	err      error
	tenantID string
	scope    string
	calls    int
	allowed  bool
}

func (s *permissionStub) Allowed(_ context.Context, tenantID, scope string) (bool, error) {
	s.calls++
	s.tenantID, s.scope = tenantID, scope

	return s.allowed, s.err
}

func newService(repo *MockTenantRepository, options ...application.Option) *application.TenantService {
	return application.NewTenantService(repo, passthroughTransactor{}, &membershipRecorder{}, &permissionStub{allowed: true}, options...)
}

func TestCreateEvent(t *testing.T) {
	t.Parallel()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, false)
	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	repo.EXPECT().FindTenantByPublicID(gomock.Any(), tenant.PublicID()).Return(tenant, nil)

	var createdEvent domain.Event

	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, event domain.Event) error {
		createdEvent = event

		return nil
	})
	service := newService(repo)

	ctx := tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID())

	event, err := service.CreateEvent(ctx, application.CreateEventInput{
		TenantPublicID: tenant.PublicID(),
		Name:           "Festival",
		Type:           domain.EventTypeShortTerm,
	})
	if err != nil {
		t.Fatalf("CreateEvent() error = %v", err)
	}

	if got, want := event.TenantID(), tenant.ID(); got != want {
		t.Errorf("TenantID() = %q, want %q", got, want)
	}

	if got, want := event.TenantPublicID(), tenant.PublicID(); got != want {
		t.Errorf("TenantPublicID() = %q, want %q", got, want)
	}

	if got, want := event.Status(), domain.EventStatusDraft; got != want {
		t.Errorf("Status() = %q, want %q", got, want)
	}

	if got, want := createdEvent, event; got != want {
		t.Errorf("created event = %#v, want %#v", got, want)
	}
}

func TestCreateEventKeepsUnspecifiedType(t *testing.T) {
	t.Parallel()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, false)
	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	repo.EXPECT().FindTenantByPublicID(gomock.Any(), tenant.PublicID()).Return(tenant, nil)
	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).Return(nil)
	service := newService(repo)

	ctx := tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID())

	event, err := service.CreateEvent(ctx, application.CreateEventInput{TenantPublicID: tenant.PublicID(), Name: "Festival"})
	if err != nil {
		t.Fatalf("CreateEvent() error = %v", err)
	}

	if got, want := event.Type(), domain.EventTypeUnspecified; got != want {
		t.Errorf("Type() = %v, want %v", got, want)
	}
}

func TestCreateEventRequiresTenantID(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	service := newService(NewMockTenantRepository(ctrl))

	ctx := tenantctx.WithTenantPublicID(context.Background(), "tenant-public-id")

	_, err := service.CreateEvent(ctx, application.CreateEventInput{Name: "Festival"})
	if !errors.Is(err, application.ErrTenantIDRequired) {
		t.Fatalf("CreateEvent() error = %v, want %v", err, application.ErrTenantIDRequired)
	}
}

func TestCreateEventRejectsPendingOwnerTenant(t *testing.T) {
	t.Parallel()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStatePendingOwner, false)
	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	repo.EXPECT().FindTenantByPublicID(gomock.Any(), tenant.PublicID()).Return(tenant, nil)
	service := newService(repo)

	ctx := tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID())

	_, err := service.CreateEvent(ctx, application.CreateEventInput{TenantPublicID: tenant.PublicID(), Name: "Festival"})
	if !errors.Is(err, application.ErrTenantPendingOwner) {
		t.Fatalf("CreateEvent() error = %v, want %v", err, application.ErrTenantPendingOwner)
	}
}

func TestCreateEventRejectsMissingContextTenant(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	// A tenant-scoped operation cannot proceed without a verified JWT tenant.
	service := newService(NewMockTenantRepository(ctrl))

	_, err := service.CreateEvent(context.Background(), application.CreateEventInput{
		TenantPublicID: "tenant-public-id",
		Name:           "Festival",
		Type:           domain.EventTypeShortTerm,
	})
	if !errors.Is(err, tenantctx.ErrMissing) {
		t.Fatalf("CreateEvent() error = %v, want %v", err, tenantctx.ErrMissing)
	}
}

func TestCreateEventRejectsMismatchedRequestTenant(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	// The requested tenant differs from the authenticated one, so the use case
	// refuses before touching the repository.
	service := newService(NewMockTenantRepository(ctrl))

	ctx := tenantctx.WithTenantPublicID(context.Background(), "tenant-public-id")

	_, err := service.CreateEvent(ctx, application.CreateEventInput{
		TenantPublicID: "other-tenant-public-id",
		Name:           "Festival",
	})
	if !errors.Is(err, tenantctx.ErrMismatch) {
		t.Fatalf("CreateEvent() error = %v, want %v", err, tenantctx.ErrMismatch)
	}
}

func TestTransitionEventStatusRejectsMismatchedContextTenant(t *testing.T) {
	t.Parallel()

	event := domain.NewEvent("event-id", "event-public-id", "tenant-id", "tenant-public-id", "Festival", domain.EventTypeShortTerm, domain.EventStatusDraft)
	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	repo.EXPECT().FindEventByPublicID(gomock.Any(), event.PublicID()).Return(event, nil)
	service := newService(repo)

	ctx := tenantctx.WithTenantPublicID(context.Background(), "other-tenant-public-id")

	_, err := service.TransitionEventStatus(ctx, application.TransitionEventStatusInput{
		EventPublicID: event.PublicID(),
		To:            domain.EventStatusOpen,
	})
	if !errors.Is(err, tenantctx.ErrMismatch) {
		t.Fatalf("TransitionEventStatus() error = %v, want %v", err, tenantctx.ErrMismatch)
	}
}

func TestTransitionEventStatus(t *testing.T) {
	t.Parallel()

	event := domain.NewEvent("event-id", "event-public-id", "tenant-id", "tenant-public-id", "Festival", domain.EventTypeShortTerm, domain.EventStatusDraft)
	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	repo.EXPECT().FindEventByPublicID(gomock.Any(), event.PublicID()).Return(event, nil)

	var updatedEventFromRepository domain.Event

	repo.EXPECT().UpdateEvent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, updatedEvent domain.Event) error {
		updatedEventFromRepository = updatedEvent

		return nil
	})
	service := newService(repo)

	ctx := tenantctx.WithTenantPublicID(context.Background(), event.TenantPublicID())

	updatedEvent, err := service.TransitionEventStatus(ctx, application.TransitionEventStatusInput{
		EventPublicID: event.PublicID(),
		To:            domain.EventStatusOpen,
	})
	if err != nil {
		t.Fatalf("TransitionEventStatus() error = %v", err)
	}

	if got, want := updatedEvent.Status(), domain.EventStatusOpen; got != want {
		t.Errorf("updated status = %q, want %q", got, want)
	}

	if got, want := updatedEventFromRepository, updatedEvent; got != want {
		t.Errorf("updated event = %#v, want %#v", got, want)
	}
}

func TestTransitionEventStatusValidatesInput(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	service := newService(NewMockTenantRepository(ctrl))
	ctx := tenantctx.WithTenantPublicID(context.Background(), "tenant-public-id")

	_, err := service.TransitionEventStatus(ctx, application.TransitionEventStatusInput{To: domain.EventStatusOpen})
	if !errors.Is(err, application.ErrEventIDRequired) {
		t.Errorf("TransitionEventStatus() error = %v, want %v", err, application.ErrEventIDRequired)
	}

	_, err = service.TransitionEventStatus(ctx, application.TransitionEventStatusInput{EventPublicID: "event-public-id"})
	if !errors.Is(err, application.ErrEventStatusRequired) {
		t.Errorf("TransitionEventStatus() error = %v, want %v", err, application.ErrEventStatusRequired)
	}
}

func TestAssignEventType(t *testing.T) {
	t.Parallel()

	event := domain.NewEvent("event-id", "event-public-id", "tenant-id", "tenant-public-id", "Festival", domain.EventTypeShortTerm, domain.EventStatusDraft)
	ctrl := gomock.NewController(t)
	repository := NewMockTenantRepository(ctrl)
	repository.EXPECT().FindEventByPublicID(gomock.Any(), event.PublicID()).Return(event, nil)

	var updatedEventFromRepository domain.Event

	repository.EXPECT().UpdateEvent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, updatedEvent domain.Event) error {
		updatedEventFromRepository = updatedEvent

		return nil
	})
	service := newService(repository)

	ctx := tenantctx.WithTenantPublicID(context.Background(), event.TenantPublicID())

	updatedEvent, err := service.AssignEventType(ctx, application.AssignEventTypeInput{
		EventPublicID: event.PublicID(),
		Type:          domain.EventTypeLongTerm,
	})
	if err != nil {
		t.Fatalf("AssignEventType() error = %v", err)
	}

	if got, want := updatedEvent.Type(), domain.EventTypeLongTerm; got != want {
		t.Errorf("updated type = %v, want %v", got, want)
	}

	if got, want := updatedEventFromRepository, updatedEvent; got != want {
		t.Errorf("updated event = %#v, want %#v", got, want)
	}
}

func TestGetEvent(t *testing.T) {
	t.Parallel()

	event := domain.NewEvent("event-id", "event-public-id", "tenant-id", "tenant-public-id", "Festival", domain.EventTypeShortTerm, domain.EventStatusDraft)
	ctrl := gomock.NewController(t)
	repository := NewMockTenantRepository(ctrl)
	repository.EXPECT().FindEventByPublicID(gomock.Any(), event.PublicID()).Return(event, nil)
	service := newService(repository)

	ctx := tenantctx.WithTenantPublicID(context.Background(), event.TenantPublicID())

	found, err := service.GetEvent(ctx, event.PublicID())
	if err != nil {
		t.Fatalf("GetEvent() error = %v", err)
	}

	if got, want := found, event; got != want {
		t.Errorf("GetEvent() = %#v, want %#v", got, want)
	}
}

func TestGetEventEnforcesTenantBoundary(t *testing.T) {
	t.Parallel()

	event := domain.NewEvent("event-id", "event-public-id", "tenant-id", "tenant-public-id", "Festival", domain.EventTypeShortTerm, domain.EventStatusDraft)

	tests := []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "other tenant", ctx: tenantctx.WithTenantPublicID(context.Background(), "other-tenant-public-id"), want: tenantctx.ErrMismatch},
		{name: "no tenant context", ctx: context.Background(), want: tenantctx.ErrMissing},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			repository := NewMockTenantRepository(ctrl)
			repository.EXPECT().FindEventByPublicID(gomock.Any(), event.PublicID()).Return(event, nil)
			service := newService(repository)

			if _, err := service.GetEvent(tt.ctx, event.PublicID()); !errors.Is(err, tt.want) {
				t.Fatalf("GetEvent() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestListEvents(t *testing.T) {
	t.Parallel()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, false)
	events := []domain.Event{
		domain.NewEvent("event-1", "event-public-id-1", tenant.ID(), tenant.PublicID(), "Festival 1", domain.EventTypeShortTerm, domain.EventStatusDraft),
		domain.NewEvent("event-2", "event-public-id-2", tenant.ID(), tenant.PublicID(), "Festival 2", domain.EventTypeLongTerm, domain.EventStatusDraft),
	}

	for _, includeArchived := range []bool{false, true} {
		t.Run(fmt.Sprintf("include archived = %t", includeArchived), func(t *testing.T) {
			t.Parallel()

			// The caller's flag reaches the repository unchanged, and the
			// listing is always capped at the limit the spec sets.
			wantFilter := repository.ListEventsFilter{IncludeArchived: includeArchived, Limit: application.MaxListEvents}

			ctrl := gomock.NewController(t)
			repo := NewMockTenantRepository(ctrl)
			repo.EXPECT().FindTenantByPublicID(gomock.Any(), tenant.PublicID()).Return(tenant, nil)
			repo.EXPECT().ListEventsByTenantID(gomock.Any(), tenant.ID(), wantFilter).Return(events, nil)
			service := newService(repo)

			ctx := tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID())

			got, err := service.ListEvents(ctx, tenant.PublicID(), includeArchived)
			if err != nil {
				t.Fatalf("ListEvents() error = %v", err)
			}

			if want := events; !slices.Equal(got, want) {
				t.Errorf("ListEvents() = %#v, want %#v", got, want)
			}
		})
	}
}

func TestListEventsCapMatchesTheSpec(t *testing.T) {
	t.Parallel()

	if got, want := application.MaxListEvents, 1000; got != want {
		t.Errorf("MaxListEvents = %d, want %d", got, want)
	}
}

func TestListEventsRejectsMismatchedRequestTenant(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	service := newService(NewMockTenantRepository(ctrl))

	ctx := tenantctx.WithTenantPublicID(context.Background(), "tenant-public-id")

	_, err := service.ListEvents(ctx, "other-tenant-public-id", false)
	if !errors.Is(err, tenantctx.ErrMismatch) {
		t.Fatalf("ListEvents() error = %v, want %v", err, tenantctx.ErrMismatch)
	}
}

func TestStartTenantRegistration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)

	var (
		createdTenant domain.Tenant
		createdClaim  domain.OwnershipClaim
	)

	gomock.InOrder(
		// Expired registrations are swept before the new one is stored.
		repo.EXPECT().DeleteExpiredPendingTenants(gomock.Any(), now).Return(int64(0), nil),
		repo.EXPECT().CreatePendingTenant(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, tenant domain.Tenant, claim domain.OwnershipClaim) error {
			createdTenant, createdClaim = tenant, claim

			return nil
		}),
	)
	service := newService(repo, application.WithClock(func() time.Time { return now }), application.WithOwnershipClaimTTL(time.Hour))

	registration, err := service.StartTenantRegistration(context.Background(), application.StartTenantRegistrationInput{Name: "Acme", ContractPlan: "standard"})
	if err != nil {
		t.Fatalf("StartTenantRegistration() error = %v", err)
	}

	if got, want := registration.Tenant, createdTenant; got != want {
		t.Errorf("registration tenant = %#v, want stored %#v", got, want)
	}

	if got, want := createdTenant.OwnershipState(), domain.TenantOwnershipStatePendingOwner; got != want {
		t.Errorf("OwnershipState() = %v, want %v", got, want)
	}

	if !createdClaim.TokenHash.Matches(registration.ClaimToken) {
		t.Error("stored token hash does not match the returned claim token")
	}

	if got, want := registration.ExpiresAt, now.Add(time.Hour); !got.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", got, want)
	}

	if got, want := createdClaim.ExpiresAt, registration.ExpiresAt; !got.Equal(want) {
		t.Errorf("stored ExpiresAt = %v, want %v", got, want)
	}
}

func TestStartTenantRegistrationValidatesInput(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	service := newService(NewMockTenantRepository(ctrl))

	_, err := service.StartTenantRegistration(context.Background(), application.StartTenantRegistrationInput{ContractPlan: "standard"})
	if !errors.Is(err, application.ErrTenantNameRequired) {
		t.Errorf("StartTenantRegistration() error = %v, want %v", err, application.ErrTenantNameRequired)
	}

	_, err = service.StartTenantRegistration(context.Background(), application.StartTenantRegistrationInput{Name: "Acme"})
	if !errors.Is(err, application.ErrTenantContractPlanRequired) {
		t.Errorf("StartTenantRegistration() error = %v, want %v", err, application.ErrTenantContractPlanRequired)
	}
}

func TestClaimTenantOwnership(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	token, hash, err := domain.NewOwnershipClaimToken()
	if err != nil {
		t.Fatalf("NewOwnershipClaimToken() error = %v", err)
	}

	pending := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStatePendingOwner, false)
	claim := domain.OwnershipClaim{TokenHash: hash, ExpiresAt: now.Add(time.Hour)}
	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	memberships := &membershipRecorder{}

	var marked domain.Tenant

	gomock.InOrder(
		repo.EXPECT().FindTenantByPublicIDForUpdate(gomock.Any(), pending.PublicID()).Return(pending, claim, nil),
		repo.EXPECT().MarkTenantOwned(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, tenant domain.Tenant) error {
			marked = tenant

			if memberships.calls != 1 {
				t.Error("MarkTenantOwned() called before the owner membership was recorded")
			}

			return nil
		}),
	)
	service := application.NewTenantService(repo, passthroughTransactor{}, memberships, &permissionStub{allowed: true}, application.WithClock(func() time.Time { return now }))

	ctx := tenantctx.WithSubject(context.Background(), "user-1")

	owned, err := service.ClaimTenantOwnership(ctx, application.ClaimTenantOwnershipInput{TenantPublicID: pending.PublicID(), ClaimToken: token})
	if err != nil {
		t.Fatalf("ClaimTenantOwnership() error = %v", err)
	}

	if got, want := owned.OwnershipState(), domain.TenantOwnershipStateOwned; got != want {
		t.Errorf("OwnershipState() = %v, want %v", got, want)
	}

	if got, want := marked, owned; got != want {
		t.Errorf("marked tenant = %#v, want %#v", got, want)
	}

	if memberships.tenantID != pending.ID() || memberships.userID != "user-1" {
		t.Errorf("owner membership = (%q, %q), want (%q, %q)", memberships.tenantID, memberships.userID, pending.ID(), "user-1")
	}
}

func TestClaimTenantOwnershipRejections(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	token, hash, err := domain.NewOwnershipClaimToken()
	if err != nil {
		t.Fatalf("NewOwnershipClaimToken() error = %v", err)
	}

	pending := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStatePendingOwner, false)
	owned := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, false)
	live := domain.OwnershipClaim{TokenHash: hash, ExpiresAt: now.Add(time.Hour)}
	subject := tenantctx.WithSubject(context.Background(), "user-1")

	tests := []struct {
		name   string
		ctx    context.Context
		tenant domain.Tenant
		claim  domain.OwnershipClaim
		token  string
		lookup bool
		want   error
	}{
		{name: "wrong token", ctx: subject, tenant: pending, claim: live, token: token + "x", lookup: true, want: application.ErrOwnershipClaimRejected},
		{name: "expired claim", ctx: subject, tenant: pending, claim: domain.OwnershipClaim{TokenHash: hash, ExpiresAt: now}, token: token, lookup: true, want: application.ErrOwnershipClaimRejected},
		{name: "already owned", ctx: subject, tenant: owned, claim: domain.OwnershipClaim{}, token: token, lookup: true, want: application.ErrOwnershipClaimRejected},
		{name: "no subject", ctx: context.Background(), tenant: pending, claim: live, token: token, lookup: false, want: tenantctx.ErrSubjectMissing},
		{name: "no token", ctx: subject, tenant: pending, claim: live, token: "", lookup: false, want: application.ErrOwnershipClaimTokenRequired},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			repo := NewMockTenantRepository(ctrl)
			memberships := &membershipRecorder{}

			if tt.lookup {
				repo.EXPECT().FindTenantByPublicIDForUpdate(gomock.Any(), pending.PublicID()).Return(tt.tenant, tt.claim, nil)
			}

			service := application.NewTenantService(repo, passthroughTransactor{}, memberships, &permissionStub{allowed: true}, application.WithClock(func() time.Time { return now }))

			_, err := service.ClaimTenantOwnership(tt.ctx, application.ClaimTenantOwnershipInput{TenantPublicID: pending.PublicID(), ClaimToken: tt.token})
			if !errors.Is(err, tt.want) {
				t.Fatalf("ClaimTenantOwnership() error = %v, want %v", err, tt.want)
			}

			if memberships.calls != 0 {
				t.Errorf("owner membership recorded %d times on a rejected claim, want 0", memberships.calls)
			}
		})
	}
}

func TestClaimTenantOwnershipDoesNotMarkOwnedWhenMembershipFails(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)

	token, hash, err := domain.NewOwnershipClaimToken()
	if err != nil {
		t.Fatalf("NewOwnershipClaimToken() error = %v", err)
	}

	pending := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStatePendingOwner, false)
	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	repo.EXPECT().FindTenantByPublicIDForUpdate(gomock.Any(), pending.PublicID()).Return(pending, domain.OwnershipClaim{TokenHash: hash, ExpiresAt: now.Add(time.Hour)}, nil)
	// MarkTenantOwned must not be called: the transaction fails before it.
	errStore := errors.New("membership store down")
	service := application.NewTenantService(repo, passthroughTransactor{}, &membershipRecorder{err: errStore}, &permissionStub{allowed: true}, application.WithClock(func() time.Time { return now }))

	_, err = service.ClaimTenantOwnership(tenantctx.WithSubject(context.Background(), "user-1"), application.ClaimTenantOwnershipInput{TenantPublicID: pending.PublicID(), ClaimToken: token})
	if !errors.Is(err, errStore) {
		t.Fatalf("ClaimTenantOwnership() error = %v, want %v", err, errStore)
	}
}

func TestChangeTenantContract(t *testing.T) {
	t.Parallel()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, false)
	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	permissions := &permissionStub{allowed: true}

	var stored domain.Tenant

	gomock.InOrder(
		repo.EXPECT().FindTenantByPublicIDForUpdate(gomock.Any(), tenant.PublicID()).Return(tenant, domain.OwnershipClaim{}, nil),
		repo.EXPECT().UpdateTenant(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, updated domain.Tenant) error {
			stored = updated

			if permissions.calls != 1 {
				t.Error("UpdateTenant() called before the caller's current permission was checked")
			}

			return nil
		}),
	)
	service := application.NewTenantService(repo, passthroughTransactor{}, &membershipRecorder{}, permissions)

	ctx := tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID())

	updated, err := service.ChangeTenantContract(ctx, application.ChangeTenantContractInput{TenantPublicID: tenant.PublicID(), ContractPlan: "enterprise"})
	if err != nil {
		t.Fatalf("ChangeTenantContract() error = %v", err)
	}

	if got, want := updated.ContractPlan(), "enterprise"; got != want {
		t.Errorf("ContractPlan() = %q, want %q", got, want)
	}

	if got, want := stored, updated; got != want {
		t.Errorf("stored tenant = %#v, want %#v", got, want)
	}

	// The permission is re-checked for the tenant's internal ID and the scope
	// the administrative writes require.
	if permissions.tenantID != tenant.ID() || permissions.scope != application.ScopeTenantWrite {
		t.Errorf("permission check = (%q, %q), want (%q, %q)", permissions.tenantID, permissions.scope, tenant.ID(), application.ScopeTenantWrite)
	}
}

func TestChangeTenantContractRequiresContractPlan(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	// The plan is validated before the tenant is even looked up.
	service := newService(NewMockTenantRepository(ctrl))

	ctx := tenantctx.WithTenantPublicID(context.Background(), "tenant-public-id")

	_, err := service.ChangeTenantContract(ctx, application.ChangeTenantContractInput{TenantPublicID: "tenant-public-id"})
	if !errors.Is(err, application.ErrTenantContractPlanRequired) {
		t.Fatalf("ChangeTenantContract() error = %v, want %v", err, application.ErrTenantContractPlanRequired)
	}
}

func TestArchiveTenant(t *testing.T) {
	t.Parallel()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, false)
	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	permissions := &permissionStub{allowed: true}

	var stored domain.Tenant

	gomock.InOrder(
		repo.EXPECT().FindTenantByPublicIDForUpdate(gomock.Any(), tenant.PublicID()).Return(tenant, domain.OwnershipClaim{}, nil),
		repo.EXPECT().UpdateTenant(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, updated domain.Tenant) error {
			stored = updated

			if permissions.calls != 1 {
				t.Error("UpdateTenant() called before the caller's current permission was checked")
			}

			return nil
		}),
	)
	service := application.NewTenantService(repo, passthroughTransactor{}, &membershipRecorder{}, permissions)

	ctx := tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID())

	archived, err := service.ArchiveTenant(ctx, application.ArchiveTenantInput{TenantPublicID: tenant.PublicID()})
	if err != nil {
		t.Fatalf("ArchiveTenant() error = %v", err)
	}

	if !archived.Archived() {
		t.Error("Archived() = false, want true")
	}

	// The soft delete keeps the identifier, the name and the plan.
	if got, want := archived, domain.NewTenant(tenant.ID(), tenant.PublicID(), tenant.Name(), tenant.ContractPlan(), tenant.OwnershipState(), true); got != want {
		t.Errorf("archived tenant = %#v, want %#v", got, want)
	}

	if got, want := stored, archived; got != want {
		t.Errorf("stored tenant = %#v, want %#v", got, want)
	}
}

// TestAdministrativeTenantWriteRejections covers the gates both administrative
// writes share: the state of the tenant and the caller's current permission.
// UpdateTenant is expected on none of them, so a rejected write reaching the
// repository fails the test.
func TestAdministrativeTenantWriteRejections(t *testing.T) {
	t.Parallel()

	owned := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, false)
	pending := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStatePendingOwner, false)
	archived := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", domain.TenantOwnershipStateOwned, true)
	errPermissions := errors.New("membership store down")

	rpcs := []struct {
		name string
		call func(context.Context, *application.TenantService, string) error
	}{
		{name: "ChangeTenantContract", call: func(ctx context.Context, service *application.TenantService, tenantPublicID string) error {
			_, err := service.ChangeTenantContract(ctx, application.ChangeTenantContractInput{TenantPublicID: tenantPublicID, ContractPlan: "enterprise"})

			return err
		}},
		{name: "ArchiveTenant", call: func(ctx context.Context, service *application.TenantService, tenantPublicID string) error {
			_, err := service.ArchiveTenant(ctx, application.ArchiveTenantInput{TenantPublicID: tenantPublicID})

			return err
		}},
	}

	tests := []struct {
		name          string
		tenant        domain.Tenant
		permissionErr error
		requestID     string
		allowed       bool
		lookup        bool
		want          error
	}{
		{name: "pending owner", tenant: pending, requestID: owned.PublicID(), allowed: true, lookup: true, want: application.ErrTenantPendingOwner},
		{name: "archived tenant", tenant: archived, requestID: owned.PublicID(), allowed: true, lookup: true, want: repository.ErrTenantArchived},
		{name: "revoked or downgraded caller", tenant: owned, requestID: owned.PublicID(), allowed: false, lookup: true, want: application.ErrPermissionDenied},
		{name: "permission check fails", tenant: owned, permissionErr: errPermissions, requestID: owned.PublicID(), allowed: false, lookup: true, want: errPermissions},
		{name: "tenant other than the authenticated one", tenant: owned, requestID: "other-tenant-public-id", allowed: true, lookup: false, want: tenantctx.ErrMismatch},
		{name: "missing tenant ID", tenant: owned, requestID: "", allowed: true, lookup: false, want: application.ErrTenantIDRequired},
	}

	for _, tt := range tests {
		for _, rpc := range rpcs {
			t.Run(tt.name+"/"+rpc.name, func(t *testing.T) {
				t.Parallel()

				ctrl := gomock.NewController(t)
				repo := NewMockTenantRepository(ctrl)

				if tt.lookup {
					repo.EXPECT().FindTenantByPublicIDForUpdate(gomock.Any(), owned.PublicID()).Return(tt.tenant, domain.OwnershipClaim{}, nil)
				}

				service := application.NewTenantService(repo, passthroughTransactor{}, &membershipRecorder{}, &permissionStub{allowed: tt.allowed, err: tt.permissionErr})
				ctx := tenantctx.WithTenantPublicID(context.Background(), owned.PublicID())

				if err := rpc.call(ctx, service, tt.requestID); !errors.Is(err, tt.want) {
					t.Fatalf("%s() error = %v, want %v", rpc.name, err, tt.want)
				}
			})
		}
	}
}
