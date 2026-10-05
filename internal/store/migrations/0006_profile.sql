-- Profile details a user edits themselves. Both are optional; the UI falls
-- back to the username.
ALTER TABLE users
    ADD COLUMN display_name TEXT NOT NULL DEFAULT '',
    ADD COLUMN email        TEXT NOT NULL DEFAULT '';
