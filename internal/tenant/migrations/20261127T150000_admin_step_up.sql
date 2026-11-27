-- The admin step-up (P15-01, decision 0643). admin_until is when the admin
-- area closes again for this session: empty until the person proves their
-- password or a passkey once more, and never past expires_at. The admin
-- door refuses a session whose admin_until is empty or past.
ALTER TABLE signin_sessions ADD COLUMN admin_until TEXT NOT NULL DEFAULT '';
