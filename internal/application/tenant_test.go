package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/repository"
)

var errRelationUnavailable = errors.New("relation unavailable")

type tenantRepositoryStub struct {
	created repository.Tenant
	deleted string
}

func (s *tenantRepositoryStub) CreateTenant(_ context.Context, params repository.CreateTenantParams) (repository.Tenant, error) {
	s.created = repository.Tenant{
		ID:           "tenant-1",
		Name:         params.Name,
		ContractPlan: params.ContractPlan,
	}

	return s.created, nil
}

func (s *tenantRepositoryStub) DeleteTenant(_ context.Context, tenantID string) error {
	s.deleted = tenantID

	return nil
}

func (s *tenantRepositoryStub) CreateEvent(context.Context, repository.CreateEventParams) (repository.Event, error) {
	return repository.Event{}, nil
}

type failingMembershipService struct{}

func (failingMembershipService) AddTenantMember(context.Context, application.AddTenantMemberInput) error {
	return errRelationUnavailable
}

func TestRegisterTenantCompensatesWhenOwnerMembershipFails(t *testing.T) {
	t.Parallel()

	repository := &tenantRepositoryStub{}
	service := application.NewTenantService(repository, failingMembershipService{})

	_, err := service.RegisterTenant(context.Background(), application.RegisterTenantInput{
		Name:         "Acme",
		ContractPlan: "standard",
	})
	if !errors.Is(err, errRelationUnavailable) {
		t.Fatalf("RegisterTenant() error = %v, want wrapping %v", err, errRelationUnavailable)
	}

	if got, want := repository.deleted, repository.created.ID; got != want {
		t.Errorf("deleted tenant ID = %q, want %q", got, want)
	}
}
