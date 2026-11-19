CREATE TABLE IF NOT EXISTS project_repository_sources (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    provider text NOT NULL,
    repository text NOT NULL,
    ref text NOT NULL,
    role text NOT NULL CHECK (role IN ('frontend','backend')),
    included boolean NOT NULL DEFAULT true,
    credential bytea
);
CREATE UNIQUE INDEX IF NOT EXISTS project_repository_identity ON project_repository_sources(project_id,provider,lower(repository));
CREATE TABLE IF NOT EXISTS repository_surveys (
    task_id text NOT NULL REFERENCES testing_tasks(id) ON DELETE CASCADE,
    batch_id text NOT NULL,
    source_id text NOT NULL,
    survey jsonb NOT NULL,
    PRIMARY KEY (task_id, batch_id, source_id)
);
