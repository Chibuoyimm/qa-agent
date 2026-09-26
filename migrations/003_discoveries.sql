CREATE TABLE IF NOT EXISTS discoveries (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id),
    base_url text NOT NULL,
    start_path text NOT NULL,
    max_pages integer NOT NULL CHECK (max_pages BETWEEN 1 AND 5),
    setup_scenario jsonb,
    status text NOT NULL CHECK (status IN ('queued', 'running', 'completed', 'error', 'cancelled')),
    result jsonb,
    error text NOT NULL DEFAULT '',
    worker_id text,
    lease_token text,
    lease_expires_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz
);
CREATE INDEX IF NOT EXISTS discoveries_claim ON discoveries(created_at,id) WHERE status='queued';
CREATE INDEX IF NOT EXISTS discoveries_project_created ON discoveries(project_id,created_at DESC,id DESC);
CREATE INDEX IF NOT EXISTS discoveries_expiry ON discoveries(lease_expires_at) WHERE status='running';
