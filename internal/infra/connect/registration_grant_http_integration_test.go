package connect_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	relationv1 "github.com/pj-hoakari/tolo-tenant-management/gen/tolo/relation/v1"
	"github.com/pj-hoakari/tolo-tenant-management/internal/onetimetoken"
)

func grantBody(t *testing.T, key, token string) string {
	t.Helper()

	body, err := json.Marshal(map[string]string{key: token})
	if err != nil {
		t.Fatalf("encode grant body: %v", err)
	}

	return string(body)
}

func (p *process) postGrant(ctx context.Context, tenantPublicID, body string) (int, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/tenants/"+url.PathEscape(tenantPublicID)+"/registration-grants/verify", strings.NewReader(body))
	if err != nil {
		return 0, "", err
	}

	res, err := p.httpClient.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return 0, "", err
	}

	return res.StatusCode, strings.TrimSpace(string(raw)), nil
}

func (p *process) expectGrantVerdict(t *testing.T, tenantPublicID, key, token string, want bool) {
	t.Helper()

	status, raw, err := p.postGrant(context.Background(), tenantPublicID, grantBody(t, key, token))
	if err != nil {
		t.Fatalf("POST registration grant verify error = %v", err)
	}

	if wantBody := fmt.Sprintf(`{"valid":%t}`, want); status != http.StatusOK || raw != wantBody {
		t.Fatalf("status, body = %d, %s, want %d, %s", status, raw, http.StatusOK, wantBody)
	}
}

func TestVerifyRegistrationGrantOverHTTP(t *testing.T) {
	f := newRelationFixture(t)

	t.Run("an open invitation stays valid until it is accepted", func(t *testing.T) {
		token := f.mustCreateInvitation(t, "Hanako").GetInvitationToken()

		f.expectGrantVerdict(t, f.tenantA.PublicID(), "invitation_token", token, true)
		f.expectGrantVerdict(t, f.tenantA.PublicID(), "invitation_token", token, true)

		if _, err := f.acceptInvitation(t, "invitee-1", f.tenantA.PublicID(), token); err != nil {
			t.Fatalf("AcceptTenantInvitation() after verifying error = %v", err)
		}

		f.expectGrantVerdict(t, f.tenantA.PublicID(), "invitation_token", token, false)
	})

	t.Run("a revoked invitation is invalid", func(t *testing.T) {
		created := f.mustCreateInvitation(t, "Taro")
		if _, err := f.revokeInvitation(t, f.tenantA.PublicID(), created.GetInvitation().GetInvitationId()); err != nil {
			t.Fatalf("RevokeTenantInvitation() error = %v", err)
		}

		f.expectGrantVerdict(t, f.tenantA.PublicID(), "invitation_token", created.GetInvitationToken(), false)
	})

	t.Run("an invitation is valid only in its own tenant", func(t *testing.T) {
		foreign, err := f.createInvitation(t, f.tokenB, f.tenantB.PublicID(), relationv1.Role_ROLE_STAFF, "Jiro")
		if err != nil {
			t.Fatalf("CreateTenantInvitation(tenant B) error = %v", err)
		}

		f.expectGrantVerdict(t, f.tenantB.PublicID(), "invitation_token", foreign.GetInvitationToken(), true)
		f.expectGrantVerdict(t, f.tenantA.PublicID(), "invitation_token", foreign.GetInvitationToken(), false)
	})

	t.Run("an unknown tenant is invalid", func(t *testing.T) {
		token := f.mustCreateInvitation(t, "Saburo").GetInvitationToken()

		f.expectGrantVerdict(t, "0000000000000000", "invitation_token", token, false)
	})

	t.Run("an invitation token is not an ownership claim token", func(t *testing.T) {
		token := f.mustCreateInvitation(t, "Shiro").GetInvitationToken()

		f.expectGrantVerdict(t, f.tenantA.PublicID(), "invitation_token", token, true)
		f.expectGrantVerdict(t, f.tenantA.PublicID(), "ownership_claim_token", token, false)
	})

	t.Run("an ownership claim token is valid until the claim succeeds", func(t *testing.T) {
		registration := f.startRegistration(t, "Acme")
		tenantID := registration.GetTenant().GetTenantId()
		claimToken := registration.GetOwnershipClaimToken()

		f.expectGrantVerdict(t, tenantID, "ownership_claim_token", claimToken, true)
		f.expectGrantVerdict(t, tenantID, "ownership_claim_token", claimToken+"x", false)

		if _, err := f.claim(t, internalJWTs(t).registration, tenantID, claimToken, "acme", "Hanako"); err != nil {
			t.Fatalf("ClaimTenantOwnership() after verifying error = %v", err)
		}

		f.expectGrantVerdict(t, tenantID, "ownership_claim_token", claimToken, false)
	})

	t.Run("a verify does not wait for the rows a consuming write holds", func(t *testing.T) {
		token := f.mustCreateInvitation(t, "Goro").GetInvitationToken()
		body := grantBody(t, "invitation_token", token)
		ctx := context.Background()

		var (
			status int
			raw    string
			err    error
		)

		lockErr := f.tenants.WithinTransaction(ctx, func(locked context.Context) error {
			if _, _, err := f.tenants.FindTenantByPublicIDForUpdate(locked, f.tenantA.PublicID()); err != nil {
				return err
			}

			if _, err := f.invitations.FindInvitationByTokenForUpdate(locked, f.tenantA.ID(), onetimetoken.HashOf(token)); err != nil {
				return err
			}

			requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()

			status, raw, err = f.postGrant(requestCtx, f.tenantA.PublicID(), body)

			return nil
		})
		if lockErr != nil {
			t.Fatalf("locking transaction error = %v", lockErr)
		}

		if err != nil || status != http.StatusOK || raw != `{"valid":true}` {
			t.Fatalf("verify while locked = %d, %s, %v, want %d, {\"valid\":true}", status, raw, err, http.StatusOK)
		}
	})
}
