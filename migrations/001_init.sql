CREATE TABLE IF NOT EXISTS projects (
    id text PRIMARY KEY,
    name text NOT NULL,
    base_url text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS scenarios (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id),
    name text NOT NULL,
    description text NOT NULL,
    expected_outcome text NOT NULL,
    approved boolean NOT NULL,
    steps jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS scenarios_project_created ON scenarios(project_id, created_at, id);

CREATE TABLE IF NOT EXISTS runs (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id),
    base_url text NOT NULL,
    mode text NOT NULL CHECK (mode IN ('advisory', 'blocking')),
    status text NOT NULL CHECK (status IN ('queued', 'running', 'passed', 'failed', 'blocked', 'error', 'cancelled')),
    gate text NOT NULL CHECK (gate IN ('pending', 'pass', 'warn', 'fail')),
    scenarios jsonb NOT NULL,
    results jsonb NOT NULL DEFAULT '[]'::jsonb,
    worker_id text,
    lease_token text,
    lease_expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz
);
CREATE INDEX IF NOT EXISTS runs_claim ON runs(created_at, id) WHERE status = 'queued';
CREATE INDEX IF NOT EXISTS runs_project_created ON runs(project_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS runs_expiry ON runs(lease_expires_at) WHERE status = 'running';
