CREATE TABLE tenants (
    id UUID PRIMARY KEY,
    public_id VARCHAR(16) NOT NULL,
    name VARCHAR(255) NOT NULL,
    contract_plan VARCHAR(255) NOT NULL,
    archived BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT tenants_name_key UNIQUE (name),
    CONSTRAINT tenants_public_id_key UNIQUE (public_id)
);

CREATE TABLE events (
    id UUID PRIMARY KEY,
    public_id VARCHAR(16) NOT NULL,
    tenant_id UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    tenant_public_id VARCHAR(16) NOT NULL,
    name VARCHAR(255) NOT NULL,
    event_type VARCHAR(32) NOT NULL,
    status VARCHAR(32) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT events_public_id_key UNIQUE (public_id)
);

CREATE INDEX events_tenant_id_id_idx ON events (tenant_id, id);
