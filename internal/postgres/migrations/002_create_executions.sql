-- Execution audit log — one row per attempt
CREATE TABLE IF NOT EXISTS task_executions (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id     UUID        NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    worker_id   VARCHAR(100) NOT NULL,
    attempt     INTEGER      NOT NULL DEFAULT 1,
    status      VARCHAR(50)  NOT NULL,
    duration_ms INTEGER,
    error       TEXT,
    executed_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_task_executions_task_id ON task_executions(task_id);

-- Scheduled job registry
CREATE TABLE IF NOT EXISTS scheduled_jobs (
    id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(255) UNIQUE NOT NULL,
    cron_expr   VARCHAR(100) NOT NULL,
    task_type   VARCHAR(100) NOT NULL,
    payload     JSONB        NOT NULL,
    enabled     BOOLEAN      NOT NULL DEFAULT TRUE,
    last_run_at TIMESTAMPTZ,
    next_run_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_scheduled_jobs_next_run ON scheduled_jobs(next_run_at) WHERE enabled = TRUE;
