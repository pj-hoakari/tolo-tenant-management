ALTER TABLE tenant_memberships
    RENAME COLUMN created_at TO joined_at;

ALTER TABLE tenant_memberships
    ADD COLUMN display_name VARCHAR(64);

UPDATE tenant_memberships SET display_name = LEFT(user_id, 64);

ALTER TABLE tenant_memberships
    ALTER COLUMN display_name SET NOT NULL,
    ADD CONSTRAINT tenant_memberships_display_name_check
        CHECK (btrim(display_name) <> '' AND char_length(display_name) <= 64);
