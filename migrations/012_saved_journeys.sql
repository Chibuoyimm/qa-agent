ALTER TABLE journeys ADD COLUMN IF NOT EXISTS saved_plan jsonb;
ALTER TABLE chat_turns ADD COLUMN IF NOT EXISTS saved_journey_id text REFERENCES journeys(id);
