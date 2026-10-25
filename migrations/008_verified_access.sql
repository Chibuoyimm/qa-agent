CREATE TABLE IF NOT EXISTS verified_app_access (
    project_id text PRIMARY KEY REFERENCES projects(id),
    discovery_id text NOT NULL REFERENCES discoveries(id)
);
