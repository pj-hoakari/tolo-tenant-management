//go:generate go tool mockgen -source=../repository/tenant.go -destination=mock_tenant_repository_test.go -package=application_test

package application_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
	"go.uber.org/mock/gomock"
)

var errRelationUnavailable = errors.New("relation unavailable")

type successfulMembershipService struct{}

func (successfulMembershipService) AddTenantMember(context.Context, application.AddTenantMemberInput) error {
	return nil
}

type failingMembershipService struct{}

func (failingMembershipService) AddTenantMember(context.Context, application.AddTenantMemberInput) error {
	return errRelationUnavailable
}

func TestRegisterTenantCompensatesWhenOwnerMembershipFails(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	repository := NewMockTenantRepository(ctrl)

	var createdTenant domain.Tenant

	repository.EXPECT().CreateTenant(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, tenant domain.Tenant) error {
		createdTenant = tenant

		return nil
	})
	repository.EXPECT().DeleteTenant(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, tenantID string) error {
		if got, want := tenantID, createdTenant.ID(); got != want {
			t.Errorf("deleted tenant ID = %q, want %q", got, want)
		}

		return nil
	})
	service := application.NewTenantService(repository, failingMembershipService{})

	_, err := service.RegisterTenant(context.Background(), application.RegisterTenantInput{
		Name:         "Acme",
		ContractPlan: "standard",
	})
	if !errors.Is(err, errRelationUnavailable) {
		t.Fatalf("RegisterTenant() error = %v, want wrapping %v", err, errRelationUnavailable)
	}
}

func TestRegisterTenantValidatesInput(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	service := application.NewTenantService(NewMockTenantRepository(ctrl), successfulMembershipService{})

	_, err := service.RegisterTenant(context.Background(), application.RegisterTenantInput{ContractPlan: "standard"})
	if !errors.Is(err, application.ErrTenantNameRequired) {
		t.Errorf("RegisterTenant() error = %v, want %v", err, application.ErrTenantNameRequired)
	}

	_, err = service.RegisterTenant(context.Background(), application.RegisterTenantInput{Name: "Acme"})
	if !errors.Is(err, application.ErrTenantContractPlanRequired) {
		t.Errorf("RegisterTenant() error = %v, want %v", err, application.ErrTenantContractPlanRequired)
	}
}

func TestCreateEvent(t *testing.T) {
	t.Parallel()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", false)
	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	repo.EXPECT().FindTenantByPublicID(gomock.Any(), tenant.PublicID()).Return(tenant, nil)

	var createdEvent domain.Event

	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, event domain.Event) error {
		createdEvent = event

		return nil
	})
	service := application.NewTenantService(repo, successfulMembershipService{})

	ctx := tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID())

	event, err := service.CreateEvent(ctx, application.CreateEventInput{
		Name: "Festival",
		Type: domain.EventTypeShortTerm,
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

func TestCreateEventRejectsMissingContextTenant(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	// A tenant-scoped operation cannot proceed without a verified JWT tenant.
	service := application.NewTenantService(repo, successfulMembershipService{})

	_, err := service.CreateEvent(context.Background(), application.CreateEventInput{
		Name: "Festival",
		Type: domain.EventTypeShortTerm,
	})
	if !errors.Is(err, tenantctx.ErrMissing) {
		t.Fatalf("CreateEvent() error = %v, want %v", err, tenantctx.ErrMissing)
	}
}

func TestTransitionEventStatusRejectsMismatchedContextTenant(t *testing.T) {
	t.Parallel()

	event := domain.NewEvent("event-id", "event-public-id", "tenant-id", "tenant-public-id", "Festival", domain.EventTypeShortTerm, domain.EventStatusDraft)
	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	repo.EXPECT().FindEventByID(gomock.Any(), event.ID()).Return(event, nil)
	service := application.NewTenantService(repo, successfulMembershipService{})

	ctx := tenantctx.WithTenantPublicID(context.Background(), "other-tenant-public-id")

	_, err := service.TransitionEventStatus(ctx, application.TransitionEventStatusInput{
		EventID: event.ID(),
		To:      domain.EventStatusOpen,
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
	repo.EXPECT().FindEventByID(gomock.Any(), event.ID()).Return(event, nil)

	var updatedEventFromRepository domain.Event

	repo.EXPECT().UpdateEvent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, updatedEvent domain.Event) error {
		updatedEventFromRepository = updatedEvent

		return nil
	})
	service := application.NewTenantService(repo, successfulMembershipService{})

	ctx := tenantctx.WithTenantPublicID(context.Background(), event.TenantPublicID())

	updatedEvent, err := service.TransitionEventStatus(ctx, application.TransitionEventStatusInput{
		EventID: event.ID(),
		To:      domain.EventStatusOpen,
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

func TestAssignEventType(t *testing.T) {
	t.Parallel()

	event := domain.NewEvent("event-id", "event-public-id", "tenant-id", "tenant-public-id", "Festival", domain.EventTypeShortTerm, domain.EventStatusDraft)
	ctrl := gomock.NewController(t)
	repository := NewMockTenantRepository(ctrl)
	repository.EXPECT().FindEventByID(gomock.Any(), event.ID()).Return(event, nil)

	var updatedEventFromRepository domain.Event

	repository.EXPECT().UpdateEvent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, updatedEvent domain.Event) error {
		updatedEventFromRepository = updatedEvent

		return nil
	})
	service := application.NewTenantService(repository, successfulMembershipService{})

	ctx := tenantctx.WithTenantPublicID(context.Background(), event.TenantPublicID())

	updatedEvent, err := service.AssignEventType(ctx, application.AssignEventTypeInput{
		EventID: event.ID(),
		Type:    domain.EventTypeLongTerm,
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
	repository.EXPECT().FindEventByID(gomock.Any(), event.ID()).Return(event, nil)
	service := application.NewTenantService(repository, successfulMembershipService{})

	found, err := service.GetEvent(context.Background(), event.ID())
	if err != nil {
		t.Fatalf("GetEvent() error = %v", err)
	}

	if got, want := found, event; got != want {
		t.Errorf("GetEvent() = %#v, want %#v", got, want)
	}
}

func TestListEvents(t *testing.T) {
	t.Parallel()

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard", false)
	events := []domain.Event{
		domain.NewEvent("event-1", "event-public-id-1", tenant.ID(), tenant.PublicID(), "Festival 1", domain.EventTypeShortTerm, domain.EventStatusDraft),
		domain.NewEvent("event-2", "event-public-id-2", tenant.ID(), tenant.PublicID(), "Festival 2", domain.EventTypeLongTerm, domain.EventStatusDraft),
	}
	ctrl := gomock.NewController(t)
	repository := NewMockTenantRepository(ctrl)
	repository.EXPECT().FindTenantByPublicID(gomock.Any(), tenant.PublicID()).Return(tenant, nil)
	repository.EXPECT().ListEventsByTenantID(gomock.Any(), tenant.ID()).Return(events, nil)
	service := application.NewTenantService(repository, successfulMembershipService{})

	ctx := tenantctx.WithTenantPublicID(context.Background(), tenant.PublicID())

	got, err := service.ListEvents(ctx)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}

	if got, want := got, events; !slices.Equal(got, want) {
		t.Errorf("ListEvents() = %#v, want %#v", got, want)
	}
}
