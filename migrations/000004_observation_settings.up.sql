-- Observation settings (tenant_management_spec.md「観測設定値」).
-- The settings of an event consist of history_window_days only; further values
-- are added when they are needed. Existing events take the default window.
ALTER TABLE events
    ADD COLUMN history_window_days INTEGER NOT NULL DEFAULT 30,
    ADD CONSTRAINT events_history_window_days_check CHECK (history_window_days > 0);
