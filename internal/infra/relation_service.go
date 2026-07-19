package infra

import (
	"context"

	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
)

type RelationTransport interface {
	AddTenantMember(context.Context, application.AddTenantMemberInput) error
}

type RelationService struct {
	transport RelationTransport
}

func NewRelationService() *RelationService {
	return NewRelationServiceWithTransport(noopRelationTransport{})
}

func NewRelationServiceWithTransport(transport RelationTransport) *RelationService {
	return &RelationService{transport: transport}
}

func (s *RelationService) AddTenantMember(ctx context.Context, input application.AddTenantMemberInput) error {
	return s.transport.AddTenantMember(ctx, input)
}

type noopRelationTransport struct{}

func (noopRelationTransport) AddTenantMember(context.Context, application.AddTenantMemberInput) error {
	return nil
}
