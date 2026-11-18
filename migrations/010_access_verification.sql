ALTER TABLE verified_app_access ALTER COLUMN discovery_id DROP NOT NULL;
ALTER TABLE verified_app_access ADD COLUMN IF NOT EXISTS requested_discovery_id text REFERENCES discoveries(id);
