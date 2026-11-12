-- Personal access codes for participants (replace email login).
ALTER TABLE participants ADD COLUMN code TEXT;

-- Backfill codes for existing rows, then enforce uniqueness and presence.
UPDATE participants
SET code = substr(md5(gen_random_uuid()::text || id), 1, 16)
WHERE code IS NULL;

ALTER TABLE participants ALTER COLUMN code SET NOT NULL;
CREATE UNIQUE INDEX idx_participants_code ON participants(code);

-- One-time email login codes are gone together with email login.
DROP TABLE IF EXISTS login_codes;
