// Package tenant provides the Tenant Management Connect service.
package tenant

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	tenantv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1"
	"github.com/pj-hoakari/tolo-tenant-management/gen/tolo/tenant/v1/tenantv1connect"
)

var errNotImplemented = errors.New("tenant service implementation is not configured")

type Service struct {
	tenantv1connect.UnimplementedTenantServiceHandler
}

func NewService() *Service {
	return &Service{
		UnimplementedTenantServiceHandler: tenantv1connect.UnimplementedTenantServiceHandler{},
	}
}

func (s *Service) RegisterTenant(context.Context, *connect.Request[tenantv1.RegisterTenantRequest]) (*connect.Response[tenantv1.RegisterTenantResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errNotImplemented)
}

func (s *Service) ChangeTenantContract(context.Context, *connect.Request[tenantv1.ChangeTenantContractRequest]) (*connect.Response[tenantv1.ChangeTenantContractResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errNotImplemented)
}

func (s *Service) ArchiveTenant(context.Context, *connect.Request[tenantv1.ArchiveTenantRequest]) (*connect.Response[tenantv1.ArchiveTenantResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errNotImplemented)
}

func (s *Service) CreateEvent(context.Context, *connect.Request[tenantv1.CreateEventRequest]) (*connect.Response[tenantv1.CreateEventResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errNotImplemented)
}

func (s *Service) AssignEventType(context.Context, *connect.Request[tenantv1.AssignEventTypeRequest]) (*connect.Response[tenantv1.AssignEventTypeResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errNotImplemented)
}

func (s *Service) TransitionEventStatus(context.Context, *connect.Request[tenantv1.TransitionEventStatusRequest]) (*connect.Response[tenantv1.TransitionEventStatusResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errNotImplemented)
}

func (s *Service) GetEvent(context.Context, *connect.Request[tenantv1.GetEventRequest]) (*connect.Response[tenantv1.GetEventResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errNotImplemented)
}

func (s *Service) ListEvents(context.Context, *connect.Request[tenantv1.ListEventsRequest]) (*connect.Response[tenantv1.ListEventsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errNotImplemented)
}
