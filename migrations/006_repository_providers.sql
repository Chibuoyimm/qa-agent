ALTER TABLE repository_snapshots ADD COLUMN IF NOT EXISTS provider text NOT NULL DEFAULT 'github' CHECK (provider IN ('github', 'azure'));
