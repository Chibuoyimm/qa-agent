CREATE TABLE IF NOT EXISTS repository_snapshots (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id),
    repository text NOT NULL,
    ref text NOT NULL,
    role text NOT NULL CHECK (role IN ('frontend', 'backend')),
    commit_sha text NOT NULL,
    content_sha256 text NOT NULL,
    files jsonb NOT NULL,
    file_count integer NOT NULL CHECK (file_count BETWEEN 1 AND 20),
    total_bytes integer NOT NULL CHECK (total_bytes BETWEEN 0 AND 40000),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS repository_snapshots_project_created
    ON repository_snapshots(project_id, created_at DESC, id DESC);
