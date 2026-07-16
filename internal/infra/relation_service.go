package infra

import (
	"context"
	"errors"

	connectrpc "connectrpc.com/connect"

	"github.com/pj-hoakari/tolo-tenant-management/internal/application"
)

var ErrRelationAuthorizationMissing = errors.New("relation call authorization is missing")

// RelationTransport is the transport contract to be implemented by the
// generated Relation Connect client when its API is available.
type RelationTransport interface {
	AddTenantMember(context.Context, string, application.AddTenantMemberInput) error
}

// RelationService forwards Relation requests through the configured transport.
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
	callInfo, ok := connectrpc.CallInfoForHandlerContext(ctx)
	if !ok {
		return ErrRelationAuthorizationMissing
	}

	authorization := callInfo.RequestHeader().Get("Authorization")
	if authorization == "" {
		return ErrRelationAuthorizationMissing
	}

	return s.transport.AddTenantMember(ctx, authorization, input)
}

// noopRelationTransport keeps the application runnable until the Relation
// Connect API is available. It deliberately receives the authorization value
// so replacing it with a generated client preserves JWT pass-through.
type noopRelationTransport struct{}

func (noopRelationTransport) AddTenantMember(context.Context, string, application.AddTenantMemberInput) error {
	return nil
}
