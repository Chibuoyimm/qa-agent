ALTER TABLE chat_turns ADD COLUMN IF NOT EXISTS journey_setup text[];
ALTER TABLE task_approvals ADD COLUMN IF NOT EXISTS base_url text;
ALTER TABLE task_approvals ADD COLUMN IF NOT EXISTS draft_indices integer[];
CREATE INDEX IF NOT EXISTS chat_flow_revisions ON chat_turns(project_id,(proposal->>'saved_flow_id'),sequence DESC) WHERE proposal->>'flow_version'=id;
CREATE INDEX IF NOT EXISTS chat_saved_flow_runs ON chat_turns(run_id) WHERE run_id IS NOT NULL AND saved_journey_id IS NOT NULL;
