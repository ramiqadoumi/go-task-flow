-- Task core records
CREATE TABLE IF NOT EXISTS tasks (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    type         VARCHAR(100) NOT NULL,
    payload      JSONB        NOT NULL,
    status       VARCHAR(50)  NOT NULL DEFAULT 'PENDING',
    priority     INTEGER      NOT NULL DEFAULT 0,
    attempts     INTEGER      NOT NULL DEFAULT 0,
    max_retries  INTEGER      NOT NULL DEFAULT 3,
    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    scheduled_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_tasks_status         ON tasks(status);
CREATE INDEX IF NOT EXISTS idx_tasks_type_created   ON tasks(type, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_tasks_scheduled_at   ON tasks(scheduled_at) WHERE scheduled_at IS NOT NULL;
