package connect_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	relationv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/relation/v1"
)

// httpMembership is the representation the plain HTTP API answers with. It is
// declared here rather than imported, so the test reads the wire contract its
// callers depend on and not the handler's own types.
type httpMembership struct {
	UserID     string `json:"user_id"`
	TenantID   string `json:"tenant_id"`
	TenantRole string `json:"tenant_role"`
	EventRoles []struct {
		EventID string `json:"event_id"`
		Role    string `json:"role"`
	} `json:"event_roles"`
}

type httpResponse struct {
	Memberships []httpMembership `json:"memberships"`
	Error       string           `json:"error"`
}

// getMemberships calls the plain HTTP API without any credential, as its
// callers do: the endpoint carries no authentication. The tenant and the user
// are path segments, so they are escaped into the route. The raw body is
// returned next to the decoded one, so a test can pin the JSON itself.
func (f relationFixture) getMemberships(t *testing.T, tenantPublicID, userID string) (int, httpResponse, string) {
	t.Helper()

	return f.get(t, "/tenants/"+url.PathEscape(tenantPublicID)+"/users/"+url.PathEscape(userID)+"/memberships")
}

// get calls the plain HTTP API at an arbitrary path, so a test can also drive
// a path the route does not match.
func (f relationFixture) get(t *testing.T, path string) (int, httpResponse, string) {
	t.Helper()

	res, err := f.httpClient.Get(f.baseURL + path)
	if err != nil {
		t.Fatalf("GET %s error = %v", path, err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	// The mux answers a path it does not match with plain text, so a body that
	// is not JSON is left decoded as the zero value and read from the raw one.
	var decoded httpResponse
	if json.Valid(body) {
		if err := json.Unmarshal(body, &decoded); err != nil {
			t.Fatalf("decode response body %s: %v", body, err)
		}
	}

	return res.StatusCode, decoded, strings.TrimSpace(string(body))
}

// TestListMembershipsOverHTTP drives the plain HTTP transport of the
// memberships read: unauthenticated, and scoped to the tenant the request
// names rather than to a token. The tenant boundary it establishes is the same
// one the Connect transport gets from the internal JWT, so a membership of
// another tenant must not reach the answer.
func TestListMembershipsOverHTTP(t *testing.T) {
	f := newRelationFixture(t)
	ctx := context.Background()

	// The same user belongs to both tenants, so a read of one tenant that
	// leaked into the other would show.
	if _, err := f.addMember(t, f.tokenA, f.tenantA.PublicID(), "user-1", relationv1.Role_ROLE_STAFF); err != nil {
		t.Fatalf("AddTenantMember(tenant A) error = %v", err)
	}

	if _, err := f.addMember(t, f.tokenB, f.tenantB.PublicID(), "user-1", relationv1.Role_ROLE_OWNER); err != nil {
		t.Fatalf("AddTenantMember(tenant B) error = %v", err)
	}

	if _, err := f.relationClient.GrantEventRole(ctx, authorized(f.tokenA, &relationv1.GrantEventRoleRequest{EventId: f.eventA, UserId: "user-1", Role: relationv1.Role_ROLE_STAFF})); err != nil {
		t.Fatalf("GrantEventRole() error = %v", err)
	}

	t.Run("answers the membership of the named tenant", func(t *testing.T) {
		status, body, raw := f.getMemberships(t, f.tenantA.PublicID(), "user-1")
		if status != http.StatusOK {
			t.Fatalf("status = %d, want %d (body %s)", status, http.StatusOK, raw)
		}

		if len(body.Memberships) != 1 {
			t.Fatalf("memberships = %#v, want exactly the membership of tenant A", body.Memberships)
		}

		got := body.Memberships[0]
		if got.UserID != "user-1" || got.TenantID != f.tenantA.PublicID() || got.TenantRole != "staff" {
			t.Errorf("membership = %#v, want the staff membership of %q", got, f.tenantA.PublicID())
		}

		if len(got.EventRoles) != 1 || got.EventRoles[0].EventID != f.eventA || got.EventRoles[0].Role != "staff" {
			t.Errorf("event roles = %#v, want the staff role of %q", got.EventRoles, f.eventA)
		}
	})

	t.Run("the other tenant's membership does not leak", func(t *testing.T) {
		// The user is an owner of tenant B and a staff member of tenant A;
		// asking for B must answer B's membership and nothing of A's.
		status, body, raw := f.getMemberships(t, f.tenantB.PublicID(), "user-1")
		if status != http.StatusOK {
			t.Fatalf("status = %d, want %d (body %s)", status, http.StatusOK, raw)
		}

		if len(body.Memberships) != 1 {
			t.Fatalf("memberships = %#v, want exactly the membership of tenant B", body.Memberships)
		}

		got := body.Memberships[0]
		if got.TenantID != f.tenantB.PublicID() || got.TenantRole != "owner" || len(got.EventRoles) != 0 {
			t.Errorf("membership = %#v, want the owner membership of %q without event roles", got, f.tenantB.PublicID())
		}

		if strings.Contains(raw, f.tenantA.PublicID()) || strings.Contains(raw, f.eventA) {
			t.Errorf("body = %s, want nothing of tenant %q", raw, f.tenantA.PublicID())
		}
	})

	t.Run("a user who is not a member answers an empty array", func(t *testing.T) {
		status, _, raw := f.getMemberships(t, f.tenantA.PublicID(), "nobody")
		if status != http.StatusOK || raw != `{"memberships":[]}` {
			t.Errorf("status, body = %d, %s, want %d and an empty array", status, raw, http.StatusOK)
		}
	})

	t.Run("unknown tenant", func(t *testing.T) {
		status, body, raw := f.getMemberships(t, "0000000000000000", "user-1")
		if status != http.StatusNotFound || body.Error == "" {
			t.Errorf("status, body = %d, %s, want %d with a message", status, raw, http.StatusNotFound)
		}
	})

	t.Run("a path that names neither is not this route", func(t *testing.T) {
		// The tenant and the user are segments of the route, so a request that
		// leaves one out is refused by the mux before the handler runs.
		for _, path := range []string{"/tenants/" + f.tenantA.PublicID() + "/memberships", "/users/user-1/memberships"} {
			status, _, raw := f.get(t, path)
			if status != http.StatusNotFound {
				t.Errorf("GET %s status = %d, want %d (body %s)", path, status, http.StatusNotFound, raw)
			}
		}
	})
}
