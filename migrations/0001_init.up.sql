CREATE TABLE IF NOT EXISTS tasks (
    id           SERIAL PRIMARY KEY,
    description  TEXT NOT NULL,
    due_at       TIMESTAMPTZ,
    done         BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

-- Tracks which calendar events already got a reminder sent, so the
-- periodic reminder job never notifies twice for the same event.
CREATE TABLE IF NOT EXISTS reminder_log (
    event_id    TEXT PRIMARY KEY,
    event_start TIMESTAMPTZ NOT NULL,
    sent_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Tracks daily summary runs so a restart near 08:00 can detect a missed
-- run and send a catch-up summary instead of staying silent.
CREATE TABLE IF NOT EXISTS summary_log (
    run_date DATE PRIMARY KEY,
    sent_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    status   TEXT NOT NULL
);
