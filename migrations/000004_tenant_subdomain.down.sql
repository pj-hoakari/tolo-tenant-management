ALTER TABLE tenants
    DROP CONSTRAINT IF EXISTS tenants_subdomain_check,
    DROP CONSTRAINT IF EXISTS tenants_subdomain_key,
    DROP COLUMN IF EXISTS subdomain,
    ADD CONSTRAINT tenants_name_key UNIQUE (name);
