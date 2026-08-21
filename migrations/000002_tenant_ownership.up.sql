-- Tenant ownership state (tenant_management_spec.md「テナント」「オンボーディング」).
-- pending_owner tenants are created anonymously and hold the hash of a one-time
-- ownership claim token until ClaimTenantOwnership consumes it. Existing tenants
-- were all created with an owner and are therefore owned.
ALTER TABLE tenants
    ADD COLUMN ownership_state VARCHAR(32),
    ADD COLUMN ownership_claim_token_hash BYTEA,
    ADD COLUMN ownership_claim_expires_at TIMESTAMPTZ;

UPDATE tenants SET ownership_state = 'owned';

ALTER TABLE tenants
    ALTER COLUMN ownership_state SET NOT NULL,
    ADD CONSTRAINT tenants_ownership_state_check
        CHECK (ownership_state IN ('pending_owner', 'owned')),
    -- The claim token exists exactly while the tenant is pending; claiming
    -- ownership consumes it.
    ADD CONSTRAINT tenants_ownership_claim_check
        CHECK (
            (ownership_state = 'pending_owner'
                AND ownership_claim_token_hash IS NOT NULL
                AND ownership_claim_expires_at IS NOT NULL)
            OR (ownership_state = 'owned'
                AND ownership_claim_token_hash IS NULL
                AND ownership_claim_expires_at IS NULL)
        );

-- Expired pending tenants are swept by their expiry.
CREATE INDEX tenants_pending_owner_expires_at_idx
    ON tenants (ownership_claim_expires_at)
    WHERE ownership_state = 'pending_owner';
