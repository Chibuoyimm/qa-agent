CREATE TABLE IF NOT EXISTS journeys (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id),
    name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE UNIQUE INDEX IF NOT EXISTS journeys_project_name ON journeys(project_id,lower(name));
CREATE TABLE IF NOT EXISTS testing_tasks (
    id text PRIMARY KEY,
    project_id text NOT NULL REFERENCES projects(id),
    journey_id text NOT NULL REFERENCES journeys(id),
    title text NOT NULL,
    request text NOT NULL,
    request_sha256 text NOT NULL,
    discovery_id text REFERENCES discoveries(id),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX IF NOT EXISTS testing_tasks_project_created ON testing_tasks(project_id,created_at DESC,id);
ALTER TABLE chat_turns ADD COLUMN IF NOT EXISTS task_id text REFERENCES testing_tasks(id);
DROP INDEX IF EXISTS chat_pending_project;
CREATE UNIQUE INDEX IF NOT EXISTS chat_pending_scope ON chat_turns(project_id,COALESCE(task_id,'')) WHERE status='pending';
ALTER TABLE scenarios ADD COLUMN IF NOT EXISTS journey_id text REFERENCES journeys(id);
ALTER TABLE scenarios ADD COLUMN IF NOT EXISTS task_id text REFERENCES testing_tasks(id);
ALTER TABLE runs ADD COLUMN IF NOT EXISTS task_id text REFERENCES testing_tasks(id);
ALTER TABLE runs ADD COLUMN IF NOT EXISTS source_run_id text REFERENCES runs(id);
CREATE TABLE IF NOT EXISTS task_approvals (
    task_id text NOT NULL REFERENCES testing_tasks(id),
    turn_id text NOT NULL REFERENCES chat_turns(id),
    input_sha256 text NOT NULL,
    scenario_ids jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(task_id,turn_id)
);
CREATE TABLE IF NOT EXISTS finding_reviews (
    run_id text NOT NULL REFERENCES runs(id),
    scenario_id text NOT NULL,
    status text NOT NULL,
    owner text NOT NULL DEFAULT '',
    reason text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(run_id,scenario_id)
);
