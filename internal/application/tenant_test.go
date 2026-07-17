package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/domain"
)

var errRelationUnavailable = errors.New("relation unavailable")

type tenantRepositoryStub struct {
	created domain.Tenant
	deleted string
}

func (s *tenantRepositoryStub) CreateTenant(_ context.Context, tenant domain.Tenant) error {
	s.created = tenant

	return nil
}

func (s *tenantRepositoryStub) DeleteTenant(_ context.Context, tenantID string) error {
	s.deleted = tenantID

	return nil
}

func (s *tenantRepositoryStub) FindTenantByPublicID(context.Context, string) (domain.Tenant, error) {
	return domain.Tenant{}, nil
}

func (s *tenantRepositoryStub) CreateEvent(context.Context, domain.Event) error {
	return nil
}

func (s *tenantRepositoryStub) FindEventByID(context.Context, string) (domain.Event, error) {
	return domain.Event{}, nil
}

func (s *tenantRepositoryStub) UpdateEvent(context.Context, domain.Event) error {
	return nil
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

	if got, want := repository.deleted, repository.created.ID(); got != want {
		t.Errorf("deleted tenant ID = %q, want %q", got, want)
	}
}
