ALTER TABLE tenants
    DROP CONSTRAINT tenants_name_key,
    ADD COLUMN subdomain VARCHAR(63),
    ADD CONSTRAINT tenants_subdomain_key UNIQUE (subdomain),
    ADD CONSTRAINT tenants_subdomain_check CHECK ((ownership_state = 'owned') = (subdomain IS NOT NULL));
