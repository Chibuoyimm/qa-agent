CREATE TABLE IF NOT EXISTS chat_turns (
    id text PRIMARY KEY,
    sequence bigserial UNIQUE NOT NULL,
    project_id text NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    fingerprint text NOT NULL,
    prompt text NOT NULL,
    status text NOT NULL CHECK (status IN ('pending', 'completed', 'error')),
    reply text NOT NULL DEFAULT '',
    proposal jsonb,
    run_id text REFERENCES runs(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX IF NOT EXISTS chat_project_sequence ON chat_turns(project_id, sequence DESC);
CREATE UNIQUE INDEX IF NOT EXISTS chat_pending_project ON chat_turns(project_id) WHERE status = 'pending';
