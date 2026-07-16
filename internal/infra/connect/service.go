package connect

import (
	"context"
	"errors"

	connectrpc "connectrpc.com/connect"

	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
)

var errNotImplemented = errors.New("tenant service implementation is not configured")

// Service is the skeleton Connect transport implementation of TenantService.
type Service struct {
	tenantv1connect.UnimplementedTenantServiceHandler
}

func NewService() *Service {
	return &Service{
		UnimplementedTenantServiceHandler: tenantv1connect.UnimplementedTenantServiceHandler{},
	}
}

func (s *Service) RegisterTenant(context.Context, *connectrpc.Request[tenantv1.RegisterTenantRequest]) (*connectrpc.Response[tenantv1.RegisterTenantResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
}

func (s *Service) ChangeTenantContract(context.Context, *connectrpc.Request[tenantv1.ChangeTenantContractRequest]) (*connectrpc.Response[tenantv1.ChangeTenantContractResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
}

func (s *Service) ArchiveTenant(context.Context, *connectrpc.Request[tenantv1.ArchiveTenantRequest]) (*connectrpc.Response[tenantv1.ArchiveTenantResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
}

func (s *Service) CreateEvent(context.Context, *connectrpc.Request[tenantv1.CreateEventRequest]) (*connectrpc.Response[tenantv1.CreateEventResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
}

func (s *Service) AssignEventType(context.Context, *connectrpc.Request[tenantv1.AssignEventTypeRequest]) (*connectrpc.Response[tenantv1.AssignEventTypeResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
}

func (s *Service) TransitionEventStatus(context.Context, *connectrpc.Request[tenantv1.TransitionEventStatusRequest]) (*connectrpc.Response[tenantv1.TransitionEventStatusResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
}

func (s *Service) GetEvent(context.Context, *connectrpc.Request[tenantv1.GetEventRequest]) (*connectrpc.Response[tenantv1.GetEventResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
}

func (s *Service) ListEvents(context.Context, *connectrpc.Request[tenantv1.ListEventsRequest]) (*connectrpc.Response[tenantv1.ListEventsResponse], error) {
	return nil, connectrpc.NewError(connectrpc.CodeUnimplemented, errNotImplemented)
}
