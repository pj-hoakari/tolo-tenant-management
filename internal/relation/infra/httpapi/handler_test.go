package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/application"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
	"github.com/pj-hoakari/tolo-tenant-management/internal/relation/infra/httpapi"
	tenantrepository "github.com/pj-hoakari/tolo-tenant-management/internal/tenant/repository"
	"github.com/pj-hoakari/tolo-tenant-management/internal/tenantctx"
)

// fakeUseCases stands in for the relation use cases. It records what the
// handler asked for and what tenant the context it was handed is scoped to,
// which is how the boundary the handler establishes is observed.
type fakeUseCases struct {
	list        func(context.Context, application.ListMembershipsInput) ([]domain.Membership, error)
	input       application.ListMembershipsInput
	boundTenant string
	boundOK     bool
}

func (f *fakeUseCases) ListMemberships(ctx context.Context, input application.ListMembershipsInput) ([]domain.Membership, error) {
	f.input = input
	f.boundTenant, f.boundOK = tenantctx.TenantPublicIDFromContext(ctx)

	return f.list(ctx, input)
}

// membershipsPath builds the route of one user's memberships in one tenant, as
// a caller would.
func membershipsPath(tenantPublicID, userID string) string {
	return "/tenants/" + url.PathEscape(tenantPublicID) + "/users/" + url.PathEscape(userID) + "/memberships"
}

// serve mounts the API and answers one request against it. Everything goes
// through a mux, because the route is where the parameters come from: the
// method matching, the path segments and the refusal of a path that names
// neither are all the mux's doing.
func serve(t *testing.T, useCases *fakeUseCases, method, target string) *httptest.ResponseRecorder {
	t.Helper()

	mux := http.NewServeMux()
	if err := httpapi.Mount(useCases)(mux, nil); err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, httptest.NewRequest(method, target, nil))

	return recorder
}

// answering returns use cases that answer memberships, never an error.
func answering(memberships ...domain.Membership) *fakeUseCases {
	return &fakeUseCases{
		list: func(context.Context, application.ListMembershipsInput) ([]domain.Membership, error) {
			return memberships, nil
		},
	}
}

// failing returns use cases that fail with err.
func failing(err error) *fakeUseCases {
	return &fakeUseCases{
		list: func(context.Context, application.ListMembershipsInput) ([]domain.Membership, error) {
			return nil, err
		},
	}
}

// unreachable returns use cases the test expects never to be called.
func unreachable(t *testing.T) *fakeUseCases {
	t.Helper()

	return &fakeUseCases{
		list: func(context.Context, application.ListMembershipsInput) ([]domain.Membership, error) {
			t.Error("use cases called for a request that should not have reached them")

			return nil, nil
		},
	}
}

// TestListMembershipsScopesTheReadToTheNamedTenant is the point of this
// transport: the tenant the path names becomes the tenant of the context, and
// the read itself is the plain user filter.
func TestListMembershipsScopesTheReadToTheNamedTenant(t *testing.T) {
	t.Parallel()

	useCases := answering()

	recorder := serve(t, useCases, http.MethodGet, membershipsPath("tenant-public-id", "user-1"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	if !useCases.boundOK || useCases.boundTenant != "tenant-public-id" {
		t.Errorf("context tenant = %q, %v, want the tenant the path named", useCases.boundTenant, useCases.boundOK)
	}

	// The tenant travels on the context, not in the filter: naming both would
	// be an ambiguous filter.
	want := application.ListMembershipsInput{TenantPublicID: "", UserID: "user-1"}
	if useCases.input != want {
		t.Errorf("input = %#v, want %#v", useCases.input, want)
	}
}

func TestListMembershipsAnswersJSON(t *testing.T) {
	t.Parallel()

	membership := domain.NewMembership("user-1", "tenant-id", "tenant-public-id", domain.RoleOwner, []domain.EventRole{
		domain.NewEventRole("event-id", "event-public-id", domain.RoleStaff),
	})

	recorder := serve(t, answering(membership), http.MethodGet, membershipsPath("tenant-public-id", "user-1"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body %s)", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want JSON", got)
	}

	var body struct {
		Memberships []struct {
			UserID     string `json:"user_id"`
			TenantID   string `json:"tenant_id"`
			TenantRole string `json:"tenant_role"`
			EventRoles []struct {
				EventID string `json:"event_id"`
				Role    string `json:"role"`
			} `json:"event_roles"`
		} `json:"memberships"`
	}

	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body %s: %v", recorder.Body.String(), err)
	}

	if len(body.Memberships) != 1 {
		t.Fatalf("memberships = %#v, want one", body.Memberships)
	}

	got := body.Memberships[0]
	if got.UserID != "user-1" || got.TenantID != "tenant-public-id" || got.TenantRole != "owner" {
		t.Errorf("membership = %#v, want the owner membership by public ID", got)
	}

	// The internal tenant ID is not part of the representation.
	if strings.Contains(recorder.Body.String(), "tenant-id\"") {
		t.Errorf("body = %s, want no internal tenant ID", recorder.Body.String())
	}

	if len(got.EventRoles) != 1 || got.EventRoles[0].EventID != "event-public-id" || got.EventRoles[0].Role != "staff" {
		t.Errorf("event roles = %#v, want the staff role of the event's public ID", got.EventRoles)
	}
}

// TestListMembershipsAnswersEmptyArrays pins that nothing is ever written as
// null: a caller may index the arrays without a nil check.
func TestListMembershipsAnswersEmptyArrays(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		memberships []domain.Membership
		want        string
	}{
		{name: "not a member", memberships: nil, want: `{"memberships":[]}`},
		{
			name:        "no event roles",
			memberships: []domain.Membership{domain.NewMembership("user-1", "tenant-id", "tenant-public-id", domain.RoleStaff, nil)},
			want:        `{"memberships":[{"user_id":"user-1","tenant_id":"tenant-public-id","tenant_role":"staff","event_roles":[]}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder := serve(t, answering(tt.memberships...), http.MethodGet, membershipsPath("tenant-public-id", "user-1"))

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
			}

			if got := strings.TrimSpace(recorder.Body.String()); got != tt.want {
				t.Errorf("body = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestListMembershipsRequiresBothInThePath pins that the two parameters cannot
// be left off: a path missing either does not route at all, so the use cases
// are never reached and the refusal costs nothing.
func TestListMembershipsRequiresBothInThePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		want   int
	}{
		{name: "no user segment", target: "/tenants/tenant-public-id/memberships", want: http.StatusNotFound},
		{name: "no user value", target: "/tenants/tenant-public-id/users/memberships", want: http.StatusNotFound},
		{name: "no tenant segment", target: "/users/user-1/memberships", want: http.StatusNotFound},
		{name: "neither", target: "/memberships", want: http.StatusNotFound},
		// A path holding an empty segment is not this route: the mux cleans it
		// away and redirects rather than routing it here.
		{name: "empty user segment", target: "/tenants/tenant-public-id/users//memberships", want: http.StatusTemporaryRedirect},
		{name: "empty tenant segment", target: "/tenants//users/user-1/memberships", want: http.StatusTemporaryRedirect},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder := serve(t, unreachable(t), http.MethodGet, tt.target)

			if recorder.Code != tt.want {
				t.Errorf("status = %d, want %d (body %s)", recorder.Code, tt.want, recorder.Body.String())
			}
		})
	}
}

// TestListMembershipsRefusesBlankSegments covers what the route cannot: a
// segment that is present, and therefore routes, but names nothing.
func TestListMembershipsRefusesBlankSegments(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		target string
		want   string
	}{
		{name: "blank tenant", target: membershipsPath("   ", "user-1"), want: "tenant_id is required"},
		{name: "blank user", target: membershipsPath("tenant-public-id", "   "), want: "user_id is required"},
		{name: "both blank names the tenant first", target: membershipsPath(" ", " "), want: "tenant_id is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder := serve(t, unreachable(t), http.MethodGet, tt.target)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}

			if got := strings.TrimSpace(recorder.Body.String()); got != `{"error":"`+tt.want+`"}` {
				t.Errorf("body = %s, want the %q message", got, tt.want)
			}
		})
	}
}

func TestListMembershipsMapsFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		want     int
		wantBody string
	}{
		{name: "unknown tenant", err: tenantrepository.ErrTenantNotFound, want: http.StatusNotFound, wantBody: tenantrepository.ErrTenantNotFound.Error()},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: http.StatusGatewayTimeout, wantBody: "request timed out"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder := serve(t, failing(tt.err), http.MethodGet, membershipsPath("tenant-public-id", "user-1"))

			if recorder.Code != tt.want {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, tt.want, recorder.Body.String())
			}

			var body struct {
				Error string `json:"error"`
			}

			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %s: %v", recorder.Body.String(), err)
			}

			if body.Error != tt.wantBody {
				t.Errorf("error = %q, want %q", body.Error, tt.wantBody)
			}
		})
	}
}

// TestListMembershipsHidesInternalFailures pins the error policy: the cause of
// a failure the client can do nothing about stays server-side. The filter and
// boundary failures belong here too, because the handler has already made them
// unreachable — one reaching it would mean this transport is at fault.
func TestListMembershipsHidesInternalFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{name: "repository failure", err: errors.New("dial tcp 10.0.0.1:5432: connection refused")},
		{name: "filter not accepted", err: application.ErrFilterRequired},
		{name: "tenant missing from context", err: tenantctx.ErrMissing},
		{name: "tenant boundary violation", err: tenantctx.ErrMismatch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder := serve(t, failing(tt.err), http.MethodGet, membershipsPath("tenant-public-id", "user-1"))

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
			}

			if body := strings.TrimSpace(recorder.Body.String()); body != `{"error":"internal error"}` {
				t.Errorf("body = %s, want the fixed internal error", body)
			}
		})
	}
}

// TestListMembershipsRejectsOtherMethods pins that the route is a read: the mux
// answers anything else itself, so the use cases are never reached.
func TestListMembershipsRejectsOtherMethods(t *testing.T) {
	t.Parallel()

	recorder := serve(t, unreachable(t), http.MethodPost, membershipsPath("tenant-public-id", "user-1"))

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}
