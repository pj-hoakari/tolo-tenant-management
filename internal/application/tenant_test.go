//go:generate go tool mockgen -source=../repository/tenant.go -destination=mock_tenant_repository_test.go -package=application_test

package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
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

	tenant := domain.NewTenant("tenant-id", "tenant-public-id", "Acme", "standard")
	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	repo.EXPECT().FindTenantByPublicID(gomock.Any(), tenant.PublicID()).Return(tenant, nil)

	var createdEvent domain.Event

	repo.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, event domain.Event) error {
		createdEvent = event

		return nil
	})
	service := application.NewTenantService(repo, successfulMembershipService{})

	event, err := service.CreateEvent(context.Background(), application.CreateEventInput{
		TenantID: tenant.PublicID(),
		Name:     "Festival",
		Type:     "short_term",
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

func TestTransitionEventStatus(t *testing.T) {
	t.Parallel()

	event := domain.NewEvent("event-id", "event-public-id", "tenant-id", "tenant-public-id", "Festival", "short_term")
	ctrl := gomock.NewController(t)
	repo := NewMockTenantRepository(ctrl)
	repo.EXPECT().FindEventByID(gomock.Any(), event.ID()).Return(event, nil)

	var updatedEventFromRepository domain.Event

	repo.EXPECT().UpdateEvent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, updatedEvent domain.Event) error {
		updatedEventFromRepository = updatedEvent

		return nil
	})
	service := application.NewTenantService(repo, successfulMembershipService{})

	updatedEvent, err := service.TransitionEventStatus(context.Background(), application.TransitionEventStatusInput{
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
