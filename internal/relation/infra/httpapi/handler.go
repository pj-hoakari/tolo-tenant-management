// Package httpapi serves the memberships of one user over plain HTTP, next to
// the Connect services of the same process.
//
// The callers of this API — today the identity provider — hold no internal
// JWT, so there is no verified claim to scope the read by and nothing in the
// process restricts who may call it: reachability has to be limited by the
// network or by the gateway in front of it. The tenant boundary is therefore
// named by the request: the handler binds the tenant to the context and the
// same use case the Connect transport calls answers inside it, so the boundary
// is enforced in one place for both transports.
//
// The transport is plain HTTP rather than Connect because these callers are
// not Connect clients; the API therefore carries its own JSON representation
// and depends on neither the generated types nor the Connect transport of
// RelationAdminService.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	connectrpc "connectrpc.com/connect"

	infraconnect "github.com/pj-hoakari/tolo-tenant-management/internal/infra/connect"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/repository"
	tenantrepository "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

// ListMembershipsPattern is the route pattern of the memberships listing. The
// tenant and the user are path segments rather than query parameters, so the
// two things the read cannot be answered without are part of the route itself
// and cannot be left off: a request that omits either does not reach the
// handler at all. The method is part of the pattern too, so the mux answers
// any other method with 405 by itself.
const ListMembershipsPattern = "GET /tenants/{tenant_id}/users/{user_id}/memberships"

// The fixed messages the API answers with. The parameters are named as the
// route names them, so a caller reads the message against the request it sent;
// an internal failure says nothing at all, its cause staying in the log.
const (
	messageTenantRequired = "tenant_id is required"
	messageUserRequired   = "user_id is required"
	messageTimeout        = "request timed out"
	messageInternal       = "internal error"
)

// membershipLister is the slice of the relation use cases this API serves. It
// is the same operation the Connect transport calls, answered inside the
// tenant the context is bound to.
type membershipLister interface {
	ListMemberships(context.Context, application.ListMembershipsInput) ([]domain.Membership, error)
}

// Mount registers the API on the process mux. The callers hold no internal
// JWT, so the route is unauthenticated: the auth interceptor and the
// process-wide Connect interceptors do not apply to it.
func Mount(useCases membershipLister) infraconnect.Mount {
	return func(mux *http.ServeMux, _ infraconnect.AuthInterceptor, _ ...connectrpc.Interceptor) error {
		mux.HandleFunc(ListMembershipsPattern, listMemberships(useCases))

		return nil
	}
}

// membershipsResponse is the body of a successful listing. The slice is always
// written, so a caller never has to tell an empty listing from a null.
type membershipsResponse struct {
	Memberships []membership `json:"memberships"`
}

// membership is one membership as the API answers it. Identifiers are the
// public ones, as everywhere on a transport.
type membership struct {
	UserID     string      `json:"user_id"`
	TenantID   string      `json:"tenant_id"`
	TenantRole string      `json:"tenant_role"`
	EventRoles []eventRole `json:"event_roles"`
}

// eventRole is one role held on an event of the membership's tenant.
type eventRole struct {
	EventID string `json:"event_id"`
	Role    string `json:"role"`
}

// errorResponse is the body of every refused or failed request.
type errorResponse struct {
	Error string `json:"error"`
}

// listMemberships answers the membership of one user in one tenant. Both are
// required — the tenant because it is what the read is scoped to, the user
// because it is what is being asked about — and the route says so: a request
// that leaves either segment out is refused by the mux before this runs. The
// emptiness checks below therefore only catch a segment that is present but
// holds nothing but whitespace.
func listMemberships(useCases membershipLister) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		tenantPublicID := strings.TrimSpace(r.PathValue("tenant_id"))
		userID := strings.TrimSpace(r.PathValue("user_id"))

		if tenantPublicID == "" {
			writeJSON(ctx, w, http.StatusBadRequest, errorResponse{Error: messageTenantRequired})

			return
		}

		if userID == "" {
			writeJSON(ctx, w, http.StatusBadRequest, errorResponse{Error: messageUserRequired})

			return
		}

		// The request names the tenant this read is scoped to, in place of the
		// claim a Connect call would carry. Everything below the transport
		// then reads the boundary the same way, down to the repository's
		// ownership check on each row it hands back.
		ctx = tenantctx.WithTenantPublicID(ctx, tenantPublicID)

		memberships, err := useCases.ListMemberships(ctx, application.ListMembershipsInput{
			// The user filter is the one that is answered within the tenant of
			// the context; naming the tenant here as well would be ambiguous.
			TenantPublicID: "",
			UserID:         userID,
		})
		if err != nil {
			writeError(ctx, w, err)

			return
		}

		writeJSON(ctx, w, http.StatusOK, membershipsResponse{Memberships: membershipsBody(memberships)})
	}
}

// membershipsBody converts the domain models to the wire representation. Both
// slices are built with make, so an empty one is written as [] and not null.
func membershipsBody(memberships []domain.Membership) []membership {
	body := make([]membership, 0, len(memberships))

	for _, m := range memberships {
		roles := m.EventRoles()
		eventRoles := make([]eventRole, 0, len(roles))

		for _, role := range roles {
			eventRoles = append(eventRoles, eventRole{
				EventID: role.EventPublicID(),
				Role:    role.Role().String(),
			})
		}

		body = append(body, membership{
			UserID:     m.UserID(),
			TenantID:   m.TenantPublicID(),
			TenantRole: m.TenantRole().String(),
			EventRoles: eventRoles,
		})
	}

	return body
}

// writeError maps a use case failure onto a status code. A failure the caller
// can do nothing about is logged with its cause and answered with a fixed
// message, so that no internal detail leaves the service; a cancelled request
// is the client going away, so nothing is written at all.
//
// The failures of the filter and of the tenant boundary are deliberately not
// mapped: the handler has already refused an empty parameter and bound the
// tenant itself, so the use case can only report one of those if this
// transport is wrong about how it calls it. That is an internal fault, and it
// is reported as one.
func writeError(ctx context.Context, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tenantrepository.ErrTenantNotFound), errors.Is(err, repository.ErrTenantNotFound):
		writeJSON(ctx, w, http.StatusNotFound, errorResponse{Error: err.Error()})
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, context.DeadlineExceeded):
		writeJSON(ctx, w, http.StatusGatewayTimeout, errorResponse{Error: messageTimeout})
	default:
		slog.ErrorContext(ctx, "internal error", "error", err)
		writeJSON(ctx, w, http.StatusInternalServerError, errorResponse{Error: messageInternal})
	}
}

// writeJSON writes the body as JSON. A write that fails has already committed
// the status line, so the failure is only reported to the log.
func writeJSON(ctx context.Context, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.ErrorContext(ctx, "response write failed", "error", err)
	}
}
