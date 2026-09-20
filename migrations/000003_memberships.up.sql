-- Relation model (auth_domain.md「不変条件」, tenant_management_spec.md「関係参照」).
-- Memberships and roles are owned by the relation side and live in the same
-- database as tenants and events, so the invariants are enforced by keys:
--   * one membership per (tenant, user): primary key of tenant_memberships
--   * event ∈ tenant: event_roles.(event_id, tenant_id) references events
--   * event-role ⇒ tenant-role: event_roles.(tenant_id, user_id) references
--     tenant_memberships; revoking the membership cascades to event roles
-- Roles are owner / staff; admin is reserved and never stored.

-- The composite key lets event_roles pin an event to its tenant.
ALTER TABLE events
    ADD CONSTRAINT events_id_tenant_id_key UNIQUE (id, tenant_id);

CREATE TABLE tenant_memberships (
    tenant_id UUID NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    user_id VARCHAR(255) NOT NULL,
    tenant_role VARCHAR(32) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT tenant_memberships_pkey PRIMARY KEY (tenant_id, user_id),
    CONSTRAINT tenant_memberships_tenant_role_check CHECK (tenant_role IN ('owner', 'staff'))
);

CREATE INDEX tenant_memberships_user_id_idx ON tenant_memberships (user_id);

CREATE TABLE event_roles (
    event_id UUID NOT NULL,
    tenant_id UUID NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    role VARCHAR(32) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT event_roles_pkey PRIMARY KEY (event_id, user_id),
    CONSTRAINT event_roles_role_check CHECK (role IN ('owner', 'staff')),
    CONSTRAINT event_roles_event_fkey
        FOREIGN KEY (event_id, tenant_id) REFERENCES events (id, tenant_id) ON DELETE CASCADE,
    CONSTRAINT event_roles_membership_fkey
        FOREIGN KEY (tenant_id, user_id) REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE CASCADE
);

CREATE INDEX event_roles_tenant_id_user_id_idx ON event_roles (tenant_id, user_id);
