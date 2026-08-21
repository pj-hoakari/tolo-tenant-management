DROP INDEX IF EXISTS tenants_pending_owner_expires_at_idx;

ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_ownership_claim_check,
    DROP CONSTRAINT IF EXISTS tenants_ownership_state_check,
    DROP COLUMN IF EXISTS ownership_claim_expires_at,
    DROP COLUMN IF EXISTS ownership_claim_token_hash,
    DROP COLUMN IF EXISTS ownership_state;
