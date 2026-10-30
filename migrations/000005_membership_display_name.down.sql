ALTER TABLE tenant_memberships
    DROP CONSTRAINT IF EXISTS tenant_memberships_display_name_check,
    DROP COLUMN IF EXISTS display_name;

ALTER TABLE tenant_memberships
    RENAME COLUMN joined_at TO created_at;
