CREATE TABLE tenant_invitations (
    id UUID PRIMARY KEY,
    public_id VARCHAR(16) NOT NULL,
    tenant_id UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    tenant_role VARCHAR(32) NOT NULL,
    display_name VARCHAR(64) NOT NULL,
    token_hash BYTEA NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    accepted_at TIMESTAMPTZ,
    accepted_by VARCHAR(255),
    revoked_at TIMESTAMPTZ,
    CONSTRAINT tenant_invitations_public_id_key UNIQUE (public_id),
    CONSTRAINT tenant_invitations_token_hash_key UNIQUE (token_hash),
    CONSTRAINT tenant_invitations_tenant_role_check CHECK (tenant_role = 'staff'),
    CONSTRAINT tenant_invitations_display_name_check
        CHECK (btrim(display_name) <> '' AND char_length(display_name) <= 64),
    CONSTRAINT tenant_invitations_accepted_check CHECK ((accepted_at IS NULL) = (accepted_by IS NULL)),
    CONSTRAINT tenant_invitations_closed_check CHECK (accepted_at IS NULL OR revoked_at IS NULL)
);

CREATE INDEX tenant_invitations_tenant_id_created_at_idx ON tenant_invitations (tenant_id, created_at);
