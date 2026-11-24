package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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
	verify      func(context.Context, application.VerifyRegistrationGrantInput) (bool, error)
	input       application.ListMembershipsInput
	grant       application.VerifyRegistrationGrantInput
	boundTenant string
	boundOK     bool
}

func (f *fakeUseCases) ListMemberships(ctx context.Context, input application.ListMembershipsInput) ([]domain.Membership, error) {
	f.input = input
	f.boundTenant, f.boundOK = tenantctx.TenantPublicIDFromContext(ctx)

	return f.list(ctx, input)
}

func (f *fakeUseCases) VerifyRegistrationGrant(ctx context.Context, input application.VerifyRegistrationGrantInput) (bool, error) {
	f.grant = input
	f.boundTenant, f.boundOK = tenantctx.TenantPublicIDFromContext(ctx)

	return f.verify(ctx, input)
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

	return serveRequest(t, useCases, httptest.NewRequest(method, target, nil))
}

func serveRequest(t *testing.T, useCases *fakeUseCases, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()

	mux := http.NewServeMux()
	if err := httpapi.Mount(useCases)(mux, nil); err != nil {
		t.Fatalf("Mount() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, request)

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
		verify: func(context.Context, application.VerifyRegistrationGrantInput) (bool, error) {
			t.Error("use cases called for a request that should not have reached them")

			return false, nil
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

	membership := domain.NewMembership("user-1", "tenant-id", "tenant-public-id", domain.RoleOwner, domain.DisplayName{}, time.Time{}, []domain.EventRole{
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
			memberships: []domain.Membership{domain.NewMembership("user-1", "tenant-id", "tenant-public-id", domain.RoleStaff, domain.DisplayName{}, time.Time{}, nil)},
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

func verifyGrant(t *testing.T, useCases *fakeUseCases, tenantPublicID, body string) *httptest.ResponseRecorder {
	t.Helper()

	target := "/tenants/" + url.PathEscape(tenantPublicID) + "/registration-grants/verify"

	return serveRequest(t, useCases, httptest.NewRequest(http.MethodPost, target, strings.NewReader(body)))
}

func verdict(valid bool, err error) *fakeUseCases {
	return &fakeUseCases{
		verify: func(context.Context, application.VerifyRegistrationGrantInput) (bool, error) {
			return valid, err
		},
	}
}

func TestVerifyRegistrationGrantAnswersTheVerdict(t *testing.T) {
	t.Parallel()

	for _, valid := range []bool{true, false} {
		want := `{"valid":false}`
		if valid {
			want = `{"valid":true}`
		}

		t.Run(want, func(t *testing.T) {
			t.Parallel()

			recorder := verifyGrant(t, verdict(valid, nil), "tenant-public-id", `{"invitation_token":"grant-token"}`)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, http.StatusOK, recorder.Body.String())
			}

			if got := strings.TrimSpace(recorder.Body.String()); got != want {
				t.Errorf("body = %s, want %s", got, want)
			}

			if got := recorder.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Errorf("Content-Type = %q, want JSON", got)
			}

			if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
		})
	}
}

func TestVerifyRegistrationGrantAsksInTheNamedTenant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		kind application.RegistrationGrantKind
		want string
	}{
		{name: "invitation token", body: `{"invitation_token":"invitation-grant"}`, kind: application.InvitationGrant, want: "invitation-grant"},
		{name: "ownership claim token", body: `{"ownership_claim_token":"claim-grant"}`, kind: application.OwnershipClaimGrant, want: "claim-grant"},
		{name: "token kept as sent", body: `{"invitation_token":" spaced "}`, kind: application.InvitationGrant, want: " spaced "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			useCases := verdict(true, nil)

			recorder := verifyGrant(t, useCases, "tenant-public-id", tt.body)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, http.StatusOK, recorder.Body.String())
			}

			want := application.VerifyRegistrationGrantInput{TenantPublicID: "tenant-public-id", Kind: tt.kind, Token: tt.want}
			if useCases.grant != want {
				t.Errorf("input = %#v, want %#v", useCases.grant, want)
			}

			if !useCases.boundOK || useCases.boundTenant != "tenant-public-id" {
				t.Errorf("context tenant = %q, %v, want the tenant the path named", useCases.boundTenant, useCases.boundOK)
			}
		})
	}
}

func TestVerifyRegistrationGrantRefusesMalformedRequests(t *testing.T) {
	t.Parallel()

	const (
		malformed = "request body is malformed"
		required  = "exactly one of invitation_token and ownership_claim_token is required"
	)

	tests := []struct {
		name   string
		tenant string
		body   string
		want   string
	}{
		{name: "empty body", tenant: "tenant-public-id", body: "", want: malformed},
		{name: "not JSON", tenant: "tenant-public-id", body: "invitation_token=grant-token", want: malformed},
		{name: "unknown field", tenant: "tenant-public-id", body: `{"invitation_token":"grant-token","tenant_id":"other"}`, want: malformed},
		{name: "trailing data", tenant: "tenant-public-id", body: `{"invitation_token":"grant-token"}{}`, want: malformed},
		{name: "more than 4 KiB", tenant: "tenant-public-id", body: `{"invitation_token":"` + strings.Repeat("a", 4<<10) + `"}`, want: malformed},
		{name: "both tokens", tenant: "tenant-public-id", body: `{"invitation_token":"grant-token","ownership_claim_token":"grant-token"}`, want: required},
		{name: "neither token", tenant: "tenant-public-id", body: `{}`, want: required},
		{name: "empty token", tenant: "tenant-public-id", body: `{"invitation_token":""}`, want: required},
		{name: "blank tenant", tenant: "   ", body: `{"invitation_token":"grant-token"}`, want: "tenant_id is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			recorder := verifyGrant(t, unreachable(t), tt.tenant, tt.body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d (body %s)", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}

			if got := strings.TrimSpace(recorder.Body.String()); got != `{"error":"`+tt.want+`"}` {
				t.Errorf("body = %s, want the %q message", got, tt.want)
			}
		})
	}
}

func TestVerifyRegistrationGrantTimesOut(t *testing.T) {
	t.Parallel()

	recorder := verifyGrant(t, verdict(false, context.DeadlineExceeded), "tenant-public-id", `{"invitation_token":"grant-token"}`)

	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d (body %s)", recorder.Code, http.StatusGatewayTimeout, recorder.Body.String())
	}

	if got := strings.TrimSpace(recorder.Body.String()); got != `{"error":"request timed out"}` {
		t.Errorf("body = %s, want the timeout message", got)
	}
}

func TestVerifyRegistrationGrantRejectsOtherMethods(t *testing.T) {
	t.Parallel()

	recorder := serve(t, unreachable(t), http.MethodGet, "/tenants/tenant-public-id/registration-grants/verify")

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
}

func TestVerifyRegistrationGrantKeepsTheTokenOutOfFailures(t *testing.T) {
	var logs bytes.Buffer

	previous, previousWriter, previousFlags := slog.Default(), log.Writer(), log.Flags()

	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})

	const token = "secret-grant-token"

	recorder := verifyGrant(t, verdict(false, errors.New("dial tcp 10.0.0.1:5432: connection refused")), "tenant-public-id", `{"invitation_token":"`+token+`"}`)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}

	if body := strings.TrimSpace(recorder.Body.String()); body != `{"error":"internal error"}` {
		t.Errorf("body = %s, want the fixed internal error", body)
	}

	if !strings.Contains(logs.String(), "connection refused") {
		t.Fatalf("log = %q, want the internal error recorded", logs.String())
	}

	if strings.Contains(logs.String(), token) {
		t.Errorf("log = %q, want no token", logs.String())
	}
}
