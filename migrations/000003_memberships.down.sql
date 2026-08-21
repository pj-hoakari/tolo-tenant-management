DROP TABLE IF EXISTS event_roles;
DROP TABLE IF EXISTS tenant_memberships;

ALTER TABLE events
    DROP CONSTRAINT IF EXISTS events_id_tenant_id_key;
