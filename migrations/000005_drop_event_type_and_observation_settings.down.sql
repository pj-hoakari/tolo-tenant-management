ALTER TABLE events
    ADD COLUMN event_type VARCHAR(32) NOT NULL DEFAULT 'unspecified',
    ADD COLUMN history_window_days INTEGER NOT NULL DEFAULT 30,
    ADD CONSTRAINT events_history_window_days_check CHECK (history_window_days > 0);

ALTER TABLE events
    ALTER COLUMN event_type DROP DEFAULT;
