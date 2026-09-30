CREATE TABLE IF NOT EXISTS releases (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id),
    deployment_key text NOT NULL,
    base_url text NOT NULL,
    mode text NOT NULL CHECK (mode IN ('advisory', 'blocking')),
    scenario_ids jsonb NOT NULL,
    repositories jsonb NOT NULL,
    request_sha256 text NOT NULL,
    run_id text NOT NULL UNIQUE REFERENCES runs(id) DEFERRABLE INITIALLY DEFERRED,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE(project_id, deployment_key)
);
CREATE INDEX IF NOT EXISTS releases_project_created ON releases(project_id, created_at DESC, id DESC);
