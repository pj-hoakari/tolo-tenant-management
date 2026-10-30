package connect_test

import (
	"context"
	"testing"
	"time"

	connectrpc "connectrpc.com/connect/v2"
	"github.com/google/uuid"

	internaljwt "github.com/pj-hoakari/internal-jwt-handling"
	"github.com/pj-hoakari/internal-jwt-handling/jwtgen"
	relationv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/relation/v1"
	"github.com/pj-hoakari/tolo-tenant-management/internal/onetimetoken"
	relationdomain "github.com/pj-hoakari/tolo-tenant-management/internal/relation/domain"
)

func (f relationFixture) mintJoinToken(t *testing.T, subject string) string {
	t.Helper()

	return f.mint(t, jwtgen.Config{
		TokenUse: internaljwt.TokenUseRegistration,
		Subject:  subject,
		Scope:    "tenant.join",
		KeyID:    f.jwks.nextKeyID("join-key-" + subject),
	})
}

func (f relationFixture) createInvitation(t *testing.T, token, tenantID string, role relationv1.Role, displayName string) (*relationv1.CreateTenantInvitationResponse, error) {
	t.Helper()

	return f.relationClient.CreateTenantInvitation(authorized(context.Background(), token), &relationv1.CreateTenantInvitationRequest{TenantId: tenantID, TenantRole: role, DisplayName: displayName})
}

func (f relationFixture) mustCreateInvitation(t *testing.T, displayName string) *relationv1.CreateTenantInvitationResponse {
	t.Helper()

	res, err := f.createInvitation(t, f.tokenA, f.tenantA.PublicID(), relationv1.Role_ROLE_STAFF, displayName)
	if err != nil {
		t.Fatalf("CreateTenantInvitation(%q) error = %v", displayName, err)
	}

	return res
}

func (f relationFixture) acceptInvitation(t *testing.T, subject, tenantID, invitationToken string) (*relationv1.Membership, error) {
	t.Helper()

	res, err := f.relationClient.AcceptTenantInvitation(authorized(context.Background(), f.mintJoinToken(t, subject)), &relationv1.AcceptTenantInvitationRequest{TenantId: tenantID, InvitationToken: invitationToken})
	if err != nil {
		return nil, err
	}

	return res.GetMembership(), nil
}

func (f relationFixture) listInvitations(t *testing.T, tenantID string) []*relationv1.TenantInvitation {
	t.Helper()

	res, err := f.relationClient.ListTenantInvitations(authorized(context.Background(), f.tokenA), &relationv1.ListTenantInvitationsRequest{TenantId: tenantID})
	if err != nil {
		t.Fatalf("ListTenantInvitations(%q) error = %v", tenantID, err)
	}

	return res.GetInvitations()
}

func (f relationFixture) revokeInvitation(t *testing.T, tenantID, invitationID string) (*relationv1.TenantInvitation, error) {
	t.Helper()

	res, err := f.relationClient.RevokeTenantInvitation(authorized(context.Background(), f.tokenA), &relationv1.RevokeTenantInvitationRequest{TenantId: tenantID, InvitationId: invitationID})
	if err != nil {
		return nil, err
	}

	return res.GetInvitation(), nil
}

func TestTenantInvitationOverTransport(t *testing.T) {
	f := newRelationFixture(t)
	createdAt := time.Now()

	created := f.mustCreateInvitation(t, "  Hanako  ")
	invitation := created.GetInvitation()

	if !publicIDPattern.MatchString(invitation.GetInvitationId()) {
		t.Errorf("InvitationId = %q, want a 16-character hex public ID", invitation.GetInvitationId())
	}

	if !claimTokenPattern.MatchString(created.GetInvitationToken()) {
		t.Errorf("InvitationToken = %q, want 32 random bytes in base64url", created.GetInvitationToken())
	}

	if invitation.GetTenantId() != f.tenantA.PublicID() || invitation.GetTenantRole() != relationv1.Role_ROLE_STAFF || invitation.GetDisplayName() != "Hanako" {
		t.Errorf("Invitation = %v, want a staff invitation of %q for Hanako", invitation, f.tenantA.PublicID())
	}

	if expiresAt, want := invitation.GetExpiresAt().AsTime(), createdAt.Add(7*24*time.Hour); expiresAt.Before(want.Add(-time.Minute)) || expiresAt.After(want.Add(time.Minute)) {
		t.Errorf("ExpiresAt = %v, want seven days after creation (%v)", expiresAt, want)
	}

	acceptedAt := time.Now()

	membership, err := f.acceptInvitation(t, "invitee-1", f.tenantA.PublicID(), created.GetInvitationToken())
	if err != nil {
		t.Fatalf("AcceptTenantInvitation() error = %v", err)
	}

	if membership.GetUserId() != "invitee-1" || membership.GetTenantId() != f.tenantA.PublicID() || membership.GetTenantRole() != relationv1.Role_ROLE_STAFF || membership.GetDisplayName() != "Hanako" {
		t.Errorf("Membership = %v, want invitee-1 as staff Hanako of %q", membership, f.tenantA.PublicID())
	}

	listed := membershipOf(f.listMemberships(t, f.tokenA, f.tenantA.PublicID()), "invitee-1")
	if listed == nil {
		t.Fatal("ListMemberships() does not list the invitee")
	}

	if listed.GetTenantRole() != relationv1.Role_ROLE_STAFF || listed.GetDisplayName() != "Hanako" {
		t.Errorf("listed membership = %v, want staff Hanako", listed)
	}

	if joinedAt := listed.GetJoinedAt().AsTime(); joinedAt.Before(acceptedAt.Add(-time.Minute)) || joinedAt.After(time.Now().Add(time.Minute)) {
		t.Errorf("JoinedAt = %v, want the time of acceptance (%v)", joinedAt, acceptedAt)
	}

	if got := f.listInvitations(t, f.tenantA.PublicID()); len(got) != 0 {
		t.Errorf("ListTenantInvitations() after acceptance = %v, want none", got)
	}

	t.Run("the invitation token is consumed", func(t *testing.T) {
		_, err := f.acceptInvitation(t, "invitee-2", f.tenantA.PublicID(), created.GetInvitationToken())
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeUnauthenticated; got != want {
			t.Fatalf("second AcceptTenantInvitation() error code = %v, want %v", got, want)
		}
	})
}

func TestAcceptTenantInvitationOverTransportKeepsTheTokenForAMember(t *testing.T) {
	f := newRelationFixture(t)
	created := f.mustCreateInvitation(t, "Hanako")

	_, err := f.acceptInvitation(t, callerSubject, f.tenantA.PublicID(), created.GetInvitationToken())
	if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
		t.Fatalf("AcceptTenantInvitation(member) error code = %v, want %v", got, want)
	}

	owner := membershipOf(f.listMemberships(t, f.tokenA, f.tenantA.PublicID()), callerSubject)
	if owner.GetTenantRole() != relationv1.Role_ROLE_OWNER {
		t.Errorf("caller membership = %v, want it left as owner", owner)
	}

	if got := f.listInvitations(t, f.tenantA.PublicID()); len(got) != 1 {
		t.Errorf("ListTenantInvitations() = %v, want the invitation still open", got)
	}

	if _, err := f.acceptInvitation(t, "invitee-1", f.tenantA.PublicID(), created.GetInvitationToken()); err != nil {
		t.Fatalf("AcceptTenantInvitation(non-member) error = %v", err)
	}
}

func TestAcceptTenantInvitationOverTransportRejections(t *testing.T) {
	f := newRelationFixture(t)
	created := f.mustCreateInvitation(t, "Hanako")
	invitationToken := created.GetInvitationToken()

	tests := []struct {
		name            string
		token           string
		tenantID        string
		invitationToken string
		want            connectrpc.Code
	}{
		{name: "missing bearer token", token: "", tenantID: f.tenantA.PublicID(), invitationToken: invitationToken, want: connectrpc.CodeUnauthenticated},
		{name: "tenant_access token", token: f.tokenA, tenantID: f.tenantA.PublicID(), invitationToken: invitationToken, want: connectrpc.CodeUnauthenticated},
		{name: "registration token without tenant.join", token: f.mintRegistrationTokenWithScope(t, "tenant.read"), tenantID: f.tenantA.PublicID(), invitationToken: invitationToken, want: connectrpc.CodePermissionDenied},
		{name: "wrong invitation token", token: f.mintJoinToken(t, "invitee-1"), tenantID: f.tenantA.PublicID(), invitationToken: invitationToken + "x", want: connectrpc.CodeUnauthenticated},
		{name: "invitation token of another tenant", token: f.mintJoinToken(t, "invitee-1"), tenantID: f.tenantB.PublicID(), invitationToken: invitationToken, want: connectrpc.CodeUnauthenticated},
		{name: "unknown tenant", token: f.mintJoinToken(t, "invitee-1"), tenantID: "0000000000000000", invitationToken: invitationToken, want: connectrpc.CodeNotFound},
		{name: "missing invitation token", token: f.mintJoinToken(t, "invitee-1"), tenantID: f.tenantA.PublicID(), invitationToken: "", want: connectrpc.CodeInvalidArgument},
		{name: "missing tenant", token: f.mintJoinToken(t, "invitee-1"), tenantID: "", invitationToken: invitationToken, want: connectrpc.CodeInvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.relationClient.AcceptTenantInvitation(authorized(context.Background(), tt.token), &relationv1.AcceptTenantInvitationRequest{TenantId: tt.tenantID, InvitationToken: tt.invitationToken})
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("AcceptTenantInvitation() error code = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("archived tenant", func(t *testing.T) {
		if _, err := f.archiveTenant(f.tenantA.PublicID(), f.tokenA); err != nil {
			t.Fatalf("ArchiveTenant() error = %v", err)
		}

		_, err := f.acceptInvitation(t, "invitee-1", f.tenantA.PublicID(), invitationToken)
		if got, want := connectrpc.CodeOf(err), connectrpc.CodeFailedPrecondition; got != want {
			t.Fatalf("AcceptTenantInvitation(archived tenant) error code = %v, want %v", got, want)
		}
	})
}

func TestCreateTenantInvitationOverTransportRejections(t *testing.T) {
	f := newRelationFixture(t)
	staffTenant := f.createTenant(t, "ccccccccccccccc1", "Gamma")
	f.addTenantMember(t, staffTenant.ID(), callerSubject, relationdomain.RoleStaff)

	tests := []struct {
		name        string
		token       string
		tenantID    string
		role        relationv1.Role
		displayName string
		want        connectrpc.Code
	}{
		{name: "owner role", token: f.tokenA, tenantID: f.tenantA.PublicID(), role: relationv1.Role_ROLE_OWNER, displayName: "Hanako", want: connectrpc.CodeInvalidArgument},
		{name: "reserved admin role", token: f.tokenA, tenantID: f.tenantA.PublicID(), role: relationv1.Role_ROLE_ADMIN, displayName: "Hanako", want: connectrpc.CodeInvalidArgument},
		{name: "unspecified role", token: f.tokenA, tenantID: f.tenantA.PublicID(), role: relationv1.Role_ROLE_UNSPECIFIED, displayName: "Hanako", want: connectrpc.CodeInvalidArgument},
		{name: "missing display name", token: f.tokenA, tenantID: f.tenantA.PublicID(), role: relationv1.Role_ROLE_STAFF, displayName: "", want: connectrpc.CodeInvalidArgument},
		{name: "another tenant", token: f.tokenA, tenantID: f.tenantB.PublicID(), role: relationv1.Role_ROLE_STAFF, displayName: "Hanako", want: connectrpc.CodePermissionDenied},
		{name: "missing scope", token: f.mintToken(t, f.tenantA.PublicID(), "tenant.read"), tenantID: f.tenantA.PublicID(), role: relationv1.Role_ROLE_STAFF, displayName: "Hanako", want: connectrpc.CodePermissionDenied},
		{name: "staff caller", token: f.mintTenantAccessToken(t, staffTenant.PublicID()), tenantID: staffTenant.PublicID(), role: relationv1.Role_ROLE_STAFF, displayName: "Hanako", want: connectrpc.CodePermissionDenied},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.createInvitation(t, tt.token, tt.tenantID, tt.role, tt.displayName)
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("CreateTenantInvitation() error code = %v, want %v", got, tt.want)
			}
		})
	}

	if got := f.listInvitations(t, f.tenantA.PublicID()); len(got) != 0 {
		t.Errorf("ListTenantInvitations() = %v, want none of the refused invitations", got)
	}
}

func TestRevokeTenantInvitationOverTransport(t *testing.T) {
	f := newRelationFixture(t)
	created := f.mustCreateInvitation(t, "Hanako")
	invitationID := created.GetInvitation().GetInvitationId()

	revoked, err := f.revokeInvitation(t, f.tenantA.PublicID(), invitationID)
	if err != nil {
		t.Fatalf("RevokeTenantInvitation() error = %v", err)
	}

	if revoked.GetInvitationId() != invitationID || revoked.GetDisplayName() != "Hanako" {
		t.Errorf("RevokeTenantInvitation() = %v, want the revoked invitation", revoked)
	}

	if _, err := f.acceptInvitation(t, "invitee-1", f.tenantA.PublicID(), created.GetInvitationToken()); connectrpc.CodeOf(err) != connectrpc.CodeUnauthenticated {
		t.Errorf("AcceptTenantInvitation(revoked) error = %v, want unauthenticated", err)
	}

	accepted := f.mustCreateInvitation(t, "Taro")
	if _, err := f.acceptInvitation(t, "invitee-2", f.tenantA.PublicID(), accepted.GetInvitationToken()); err != nil {
		t.Fatalf("AcceptTenantInvitation() error = %v", err)
	}

	foreign, err := f.createInvitation(t, f.tokenB, f.tenantB.PublicID(), relationv1.Role_ROLE_STAFF, "Jiro")
	if err != nil {
		t.Fatalf("CreateTenantInvitation(tenant B) error = %v", err)
	}

	tests := []struct {
		name         string
		invitationID string
		want         connectrpc.Code
	}{
		{name: "already revoked", invitationID: invitationID, want: connectrpc.CodeFailedPrecondition},
		{name: "already accepted", invitationID: accepted.GetInvitation().GetInvitationId(), want: connectrpc.CodeFailedPrecondition},
		{name: "unknown invitation", invitationID: "0000000000000000", want: connectrpc.CodeNotFound},
		{name: "invitation of another tenant", invitationID: foreign.GetInvitation().GetInvitationId(), want: connectrpc.CodeNotFound},
		{name: "missing invitation", invitationID: "", want: connectrpc.CodeInvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.revokeInvitation(t, f.tenantA.PublicID(), tt.invitationID)
			if got := connectrpc.CodeOf(err); got != tt.want {
				t.Fatalf("RevokeTenantInvitation() error code = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestListTenantInvitationsOverTransport(t *testing.T) {
	f := newRelationFixture(t)

	expired := relationdomain.NewInvitation(uuid.Must(uuid.NewV7()).String(), "eeeeeeeeeeeeeee1", f.tenantA.ID(), f.tenantA.PublicID(), relationdomain.RoleStaff, mustDisplayName(t, "Expired"), time.Now().Add(-time.Minute), relationdomain.InvitationPending)

	_, hash, err := onetimetoken.New()
	if err != nil {
		t.Fatalf("onetimetoken.New() error = %v", err)
	}

	if err := f.invitations.CreateInvitation(context.Background(), expired, hash); err != nil {
		t.Fatalf("CreateInvitation(expired) error = %v", err)
	}

	first := f.mustCreateInvitation(t, "First")

	revoked := f.mustCreateInvitation(t, "Revoked")
	if _, err := f.revokeInvitation(t, f.tenantA.PublicID(), revoked.GetInvitation().GetInvitationId()); err != nil {
		t.Fatalf("RevokeTenantInvitation() error = %v", err)
	}

	accepted := f.mustCreateInvitation(t, "Accepted")
	if _, err := f.acceptInvitation(t, "invitee-1", f.tenantA.PublicID(), accepted.GetInvitationToken()); err != nil {
		t.Fatalf("AcceptTenantInvitation() error = %v", err)
	}

	second := f.mustCreateInvitation(t, "Second")

	if _, err := f.createInvitation(t, f.tokenB, f.tenantB.PublicID(), relationv1.Role_ROLE_STAFF, "Other tenant"); err != nil {
		t.Fatalf("CreateTenantInvitation(tenant B) error = %v", err)
	}

	assertListed := func(t *testing.T) {
		t.Helper()

		listed := f.listInvitations(t, f.tenantA.PublicID())

		want := []string{first.GetInvitation().GetInvitationId(), second.GetInvitation().GetInvitationId()}
		if len(listed) != len(want) {
			t.Fatalf("ListTenantInvitations() = %v, want First and Second only", listed)
		}

		for i, invitation := range listed {
			if invitation.GetInvitationId() != want[i] {
				t.Errorf("ListTenantInvitations()[%d] = %v, want %q", i, invitation, want[i])
			}
		}
	}

	assertListed(t)

	t.Run("archived tenant keeps answering", func(t *testing.T) {
		if _, err := f.archiveTenant(f.tenantA.PublicID(), f.tokenA); err != nil {
			t.Fatalf("ArchiveTenant() error = %v", err)
		}

		assertListed(t)

		if _, err := f.createInvitation(t, f.tokenA, f.tenantA.PublicID(), relationv1.Role_ROLE_STAFF, "Late"); connectrpc.CodeOf(err) != connectrpc.CodeFailedPrecondition {
			t.Errorf("CreateTenantInvitation(archived) error = %v, want failed_precondition", err)
		}

		if _, err := f.revokeInvitation(t, f.tenantA.PublicID(), first.GetInvitation().GetInvitationId()); connectrpc.CodeOf(err) != connectrpc.CodeFailedPrecondition {
			t.Errorf("RevokeTenantInvitation(archived) error = %v, want failed_precondition", err)
		}
	})
}

func mustDisplayName(t *testing.T, s string) relationdomain.DisplayName {
	t.Helper()

	name, err := relationdomain.ParseDisplayName(s)
	if err != nil {
		t.Fatalf("ParseDisplayName(%q) error = %v", s, err)
	}

	return name
}
