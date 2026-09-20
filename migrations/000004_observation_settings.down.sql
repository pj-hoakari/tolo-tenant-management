ALTER TABLE events
    DROP CONSTRAINT IF EXISTS events_history_window_days_check,
    DROP COLUMN IF EXISTS history_window_days;
